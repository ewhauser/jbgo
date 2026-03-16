package blevefs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"github.com/blevesearch/bleve/v2"
	blevequery "github.com/blevesearch/bleve/v2/search/query"
	gbfs "github.com/ewhauser/gbash/fs"
)

// NewPreparedFactory returns a filesystem factory that reuses a prebuilt
// Bleve artifact for search while delegating file operations to base.
func NewPreparedFactory(base gbfs.Factory, artifactDir string, docCount int) gbfs.Factory {
	if base == nil {
		base = gbfs.Memory()
	}
	return gbfs.FactoryFunc(func(ctx context.Context) (gbfs.FileSystem, error) {
		fsys, err := base.New(ctx)
		if err != nil {
			return nil, err
		}
		provider, err := openArtifactProvider(fsys, artifactDir, docCount)
		if err != nil {
			_ = closeIfPossible(fsys)
			return nil, err
		}
		return &preparedSearchFS{
			FileSystem: fsys,
			provider:   provider,
		}, nil
	})
}

type preparedSearchFS struct {
	gbfs.FileSystem
	provider *artifactProvider
}

func (s *preparedSearchFS) SearchProviderForPath(string) (gbfs.SearchProvider, bool) {
	if s == nil || s.provider == nil {
		return nil, false
	}
	return s.provider, true
}

func (s *preparedSearchFS) Close() error {
	var errs []error
	if s.provider != nil {
		if err := s.provider.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := closeIfPossible(s.FileSystem); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

type artifactProvider struct {
	mu           sync.RWMutex
	index        bleve.Index
	fsys         gbfs.FileSystem
	docCount     int
	capabilities gbfs.SearchCapabilities
}

func openArtifactProvider(fsys gbfs.FileSystem, artifactDir string, docCount int) (*artifactProvider, error) {
	index, err := bleve.Open(artifactDir)
	if err != nil {
		return nil, fmt.Errorf("open bleve artifact: %w", err)
	}
	return &artifactProvider{
		index:        index,
		fsys:         fsys,
		docCount:     docCount,
		capabilities: newBleveCapabilities(),
	}, nil
}

func (p *artifactProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.index == nil {
		return nil
	}
	err := p.index.Close()
	p.index = nil
	return err
}

func (p *artifactProvider) Search(ctx context.Context, query *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	status := newBleveIndexStatus(1, 1)
	if p.index == nil {
		return gbfs.SearchResult{Status: status}, fmt.Errorf("blevefs: artifact index is closed")
	}
	if query == nil {
		return gbfs.SearchResult{Status: status}, fmt.Errorf("blevefs: search query is required")
	}
	if query.Literal == "" {
		return gbfs.SearchResult{Status: status}, fmt.Errorf("blevefs: search query literal is required")
	}

	root := gbfs.Clean(query.Root)
	field := "content"
	needle := query.Literal
	if query.IgnoreCase {
		field = "content_folded"
		needle = string(foldASCIIBytes([]byte(query.Literal)))
	}

	pattern := "(?s).*" + regexp.QuoteMeta(needle) + ".*"
	compiledQuery := blevequery.NewRegexpQuery(pattern)
	compiledQuery.SetField(field)

	size := p.docCount
	if size <= 0 {
		size = 1
	}
	request := bleve.NewSearchRequestOptions(compiledQuery, size, 0, false)
	result, err := p.index.SearchInContext(ctx, request)
	if err != nil {
		return gbfs.SearchResult{Status: status}, err
	}

	hits := make([]gbfs.SearchHit, 0, len(result.Hits))
	for _, match := range result.Hits {
		if err := ctx.Err(); err != nil {
			return gbfs.SearchResult{Hits: hits, Status: status}, err
		}
		name := gbfs.Clean(match.ID)
		if !pathWithinSearchRoot(name, root) {
			continue
		}
		if !searchPathMatchesGlobs(root, name, query.IncludeGlobs, query.ExcludeGlobs) {
			continue
		}
		data, err := readArtifactFile(ctx, p.fsys, name)
		if err != nil {
			return gbfs.SearchResult{Hits: hits, Status: status}, err
		}
		matched, offsets := containsLiteral(data, []byte(query.Literal), query.IgnoreCase, query.WantOffsets)
		if !matched {
			continue
		}
		hits = append(hits, gbfs.SearchHit{
			Path:     name,
			Offsets:  offsets,
			Verified: true,
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		return hits[i].Path < hits[j].Path
	})

	truncated := false
	if query.Limit > 0 && len(hits) > query.Limit {
		hits = hits[:query.Limit]
		truncated = true
	}

	return gbfs.SearchResult{
		Hits:      hits,
		Status:    status,
		Truncated: truncated,
	}, nil
}

func (p *artifactProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return p.capabilities
}

func (p *artifactProvider) IndexStatus() gbfs.IndexStatus {
	return newBleveIndexStatus(1, 1)
}

func readArtifactFile(ctx context.Context, fsys gbfs.FileSystem, name string) ([]byte, error) {
	file, err := fsys.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(file); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func closeIfPossible(fsys gbfs.FileSystem) error {
	type closer interface {
		Close() error
	}
	c, ok := fsys.(closer)
	if !ok {
		return nil
	}
	return c.Close()
}
