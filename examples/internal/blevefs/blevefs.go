package blevefs

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	blevemapping "github.com/blevesearch/bleve/v2/mapping"
	blevequery "github.com/blevesearch/bleve/v2/search/query"
	gbfs "github.com/ewhauser/gbash/fs"
)

type bleveDoc struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	ContentFolded string `json:"content_folded"`
}

type bleveProvider struct {
	mu           sync.RWMutex
	index        bleve.Index
	records      map[string][]byte
	currentGen   uint64
	indexedGen   uint64
	capabilities gbfs.SearchCapabilities
	initErr      error
}

// NewFactory returns a searchable filesystem factory backed by Bleve.
//
// When base is nil, it defaults to an in-memory filesystem.
func NewFactory(base gbfs.Factory) gbfs.Factory {
	return gbfs.NewSearchableFactory(base, newBleveProvider)
}

func newBleveProvider() gbfs.SearchIndexer {
	provider := &bleveProvider{
		records:      make(map[string][]byte),
		capabilities: newBleveCapabilities(),
	}

	index, err := bleve.NewMemOnly(newBleveIndexMapping())
	if err != nil {
		provider.initErr = fmt.Errorf("create bleve index: %w", err)
		return provider
	}
	provider.index = index
	return provider
}

// NewIndexMapping returns the Bleve mapping used by the example provider and
// by the benchmark artifact builder.
func NewIndexMapping() blevemapping.IndexMapping {
	return newBleveIndexMapping()
}

// NewDocument returns the indexed document shape used by the example provider
// and by the benchmark artifact builder.
func NewDocument(name string, data []byte) any {
	return newBleveDoc(name, data)
}

func (p *bleveProvider) Search(ctx context.Context, query *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	status := p.indexStatusLocked()
	if p.initErr != nil {
		return gbfs.SearchResult{Status: status}, p.initErr
	}
	if query == nil {
		return gbfs.SearchResult{Status: status}, fmt.Errorf("blevefs: search query is required")
	}
	if query.Literal == "" {
		return gbfs.SearchResult{Status: status}, fmt.Errorf("blevefs: search query literal is required")
	}
	if len(p.records) == 0 {
		return gbfs.SearchResult{Status: status}, nil
	}

	root := gbfs.Clean(query.Root)
	field := "content"
	needle := query.Literal
	if query.IgnoreCase {
		field = "content_folded"
		needle = string(foldASCIIBytes([]byte(query.Literal)))
	}

	pattern := "(?s).*" + regexp.QuoteMeta(needle) + ".*"
	bleveQuery := blevequery.NewRegexpQuery(pattern)
	bleveQuery.SetField(field)

	request := bleve.NewSearchRequestOptions(bleveQuery, len(p.records), 0, false)
	result, err := p.index.SearchInContext(ctx, request)
	if err != nil {
		return gbfs.SearchResult{Status: status}, err
	}

	hits := make([]gbfs.SearchHit, 0, len(result.Hits))
	for _, match := range result.Hits {
		if err := ctx.Err(); err != nil {
			return gbfs.SearchResult{
				Hits:   hits,
				Status: status,
			}, err
		}
		name := gbfs.Clean(match.ID)
		if !pathWithinSearchRoot(name, root) {
			continue
		}
		if !searchPathMatchesGlobs(root, name, query.IncludeGlobs, query.ExcludeGlobs) {
			continue
		}
		data, ok := p.records[name]
		if !ok {
			continue
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

func (p *bleveProvider) SearchCapabilities() gbfs.SearchCapabilities {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capabilities
}

func (p *bleveProvider) IndexStatus() gbfs.IndexStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.indexStatusLocked()
}

func (p *bleveProvider) ApplySearchMutation(_ context.Context, mutation *gbfs.SearchMutation) error {
	if mutation == nil {
		return fmt.Errorf("blevefs: search mutation is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.initErr != nil {
		return p.initErr
	}

	switch mutation.Kind {
	case gbfs.SearchMutationWrite:
		name := gbfs.Clean(mutation.Path)
		data := append([]byte(nil), mutation.Data...)
		if err := p.index.Index(name, newBleveDoc(name, data)); err != nil {
			return err
		}
		p.records[name] = data
	case gbfs.SearchMutationRemove:
		prefix := gbfs.Clean(mutation.Path)
		toDelete := make([]string, 0)
		for name := range p.records {
			if pathWithinSearchRoot(name, prefix) {
				toDelete = append(toDelete, name)
			}
		}
		if err := p.deletePathsLocked(toDelete); err != nil {
			return err
		}
	case gbfs.SearchMutationRename:
		oldPrefix := gbfs.Clean(mutation.OldPath)
		newPrefix := gbfs.Clean(mutation.NewPath)
		if oldPrefix != newPrefix {
			if err := p.renamePrefixLocked(oldPrefix, newPrefix); err != nil {
				return err
			}
		}
	case gbfs.SearchMutationMetadata:
		// Generation-only mutation.
	default:
		return fmt.Errorf("blevefs: unsupported search mutation kind %q", mutation.Kind)
	}

	p.currentGen++
	p.indexedGen = p.currentGen
	return nil
}

func (p *bleveProvider) indexStatusLocked() gbfs.IndexStatus {
	return newBleveIndexStatus(p.currentGen, p.indexedGen)
}

func (p *bleveProvider) deletePathsLocked(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	batch := p.index.NewBatch()
	for _, name := range paths {
		batch.Delete(name)
	}
	if err := p.index.Batch(batch); err != nil {
		return err
	}
	for _, name := range paths {
		delete(p.records, name)
	}
	return nil
}

func (p *bleveProvider) renamePrefixLocked(oldPrefix, newPrefix string) error {
	toDelete := make([]string, 0)
	type renamedRecord struct {
		name string
		data []byte
	}
	renamed := make([]renamedRecord, 0)
	for name, data := range p.records {
		if !pathWithinSearchRoot(name, oldPrefix) {
			continue
		}
		target := gbfs.Clean(newPrefix + strings.TrimPrefix(name, oldPrefix))
		toDelete = append(toDelete, name)
		renamed = append(renamed, renamedRecord{
			name: target,
			data: append([]byte(nil), data...),
		})
	}
	if len(toDelete) == 0 {
		return nil
	}
	sort.Strings(toDelete)
	sort.Slice(renamed, func(i, j int) bool {
		return renamed[i].name < renamed[j].name
	})

	batch := p.index.NewBatch()
	for _, name := range toDelete {
		batch.Delete(name)
	}
	for _, entry := range renamed {
		if err := batch.Index(entry.name, newBleveDoc(entry.name, entry.data)); err != nil {
			return err
		}
	}
	if err := p.index.Batch(batch); err != nil {
		return err
	}
	for _, name := range toDelete {
		delete(p.records, name)
	}
	for _, entry := range renamed {
		p.records[entry.name] = entry.data
	}
	return nil
}

func newBleveDoc(name string, data []byte) bleveDoc {
	return bleveDoc{
		Path:          name,
		Content:       string(data),
		ContentFolded: string(foldASCIIBytes(data)),
	}
}

func containsLiteral(data, literal []byte, ignoreCase, wantOffsets bool) (matched bool, offsets []int64) {
	if !ignoreCase {
		if !wantOffsets {
			return bytes.Contains(data, literal), nil
		}
		offsets := literalOffsets(data, literal)
		return len(offsets) > 0, offsets
	}

	useASCIIFolding := !utf8.Valid(data) || !utf8.Valid(literal) || isASCIIBytes(data)
	foldedLiteral := foldASCIIBytes(literal)
	if useASCIIFolding {
		foldedData := foldASCIIBytes(data)
		if !wantOffsets {
			return bytes.Contains(foldedData, foldedLiteral), nil
		}
		offsets := literalOffsets(foldedData, foldedLiteral)
		return len(offsets) > 0, offsets
	}

	if !wantOffsets {
		return containsEqualFoldUTF8(data, string(literal)), nil
	}
	offsets = equalFoldOffsetsUTF8(data, string(literal))
	return len(offsets) > 0, offsets
}

func literalOffsets(data, literal []byte) []int64 {
	if len(literal) == 0 {
		return nil
	}
	offsets := make([]int64, 0, 1)
	for start := 0; start <= len(data)-len(literal); {
		idx := bytes.Index(data[start:], literal)
		if idx < 0 {
			break
		}
		abs := start + idx
		offsets = append(offsets, int64(abs))
		start = abs + 1
	}
	return offsets
}

func containsEqualFoldUTF8(data []byte, literal string) bool {
	return len(equalFoldOffsetsUTF8(data, literal)) > 0
}

func equalFoldOffsetsUTF8(data []byte, literal string) []int64 {
	if literal == "" || len(data) == 0 {
		return nil
	}
	literalRunes := utf8.RuneCountInString(literal)
	offsets := make([]int64, 0, 1)
	for i := 0; i < len(data); {
		end := advanceRunes(data, i, literalRunes)
		if end >= 0 && strings.EqualFold(string(data[i:end]), literal) {
			offsets = append(offsets, int64(i))
		}
		_, size := utf8.DecodeRune(data[i:])
		if size <= 0 {
			break
		}
		i += size
	}
	return offsets
}

func advanceRunes(data []byte, start, count int) int {
	if count == 0 {
		return start
	}
	pos := start
	for range count {
		if pos >= len(data) {
			return -1
		}
		_, size := utf8.DecodeRune(data[pos:])
		if size <= 0 {
			return -1
		}
		pos += size
	}
	return pos
}

func foldASCIIBytes(data []byte) []byte {
	folded := make([]byte, len(data))
	copy(folded, data)
	for i := range folded {
		if folded[i] >= 'A' && folded[i] <= 'Z' {
			folded[i] += 'a' - 'A'
		}
	}
	return folded
}

func isASCIIBytes(data []byte) bool {
	for _, b := range data {
		if b >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func pathWithinSearchRoot(name, root string) bool {
	root = gbfs.Clean(root)
	name = gbfs.Clean(name)
	if root == "/" {
		return true
	}
	return name == root || strings.HasPrefix(name, root+"/")
}

func searchPathMatchesGlobs(root, pathValue string, includeGlobs, excludeGlobs []string) bool {
	root = gbfs.Clean(root)
	pathValue = gbfs.Clean(pathValue)
	relative := strings.TrimPrefix(pathValue, root)
	relative = strings.TrimPrefix(relative, "/")
	base := path.Base(pathValue)

	if len(includeGlobs) > 0 {
		include := false
		for _, glob := range includeGlobs {
			if searchGlobMatches(glob, relative, base) {
				include = true
				break
			}
		}
		if !include {
			return false
		}
	}

	for _, glob := range excludeGlobs {
		if searchGlobMatches(glob, relative, base) {
			return false
		}
	}

	return true
}

func searchGlobMatches(glob, relative, base string) bool {
	pattern := strings.TrimSpace(glob)
	if pattern == "" {
		return false
	}
	targets := []string{relative}
	if !strings.Contains(pattern, "/") {
		targets = append(targets, base)
	}
	if rooted, ok := strings.CutPrefix(pattern, "/"); ok {
		pattern = rooted
		targets = []string{relative}
	}
	re, err := searchGlobRegexp(pattern)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(targets, re.MatchString)
}

func searchGlobRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				if i+2 < len(glob) && glob[i+2] == '/' {
					b.WriteString(`(?:.*/)?`)
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			j := i + 1
			if j < len(glob) && glob[j] == '!' {
				j++
			}
			if j < len(glob) && glob[j] == ']' {
				j++
			}
			for j < len(glob) && glob[j] != ']' {
				j++
			}
			if j >= len(glob) {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i : j+1]
			if strings.HasPrefix(class, "[!") {
				class = "[^" + class[2:]
			}
			b.WriteString(class)
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func newBleveCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:           true,
		IgnoreCaseLiteralSearch: true,
		RootRestriction:         true,
		IncludeGlobs:            true,
		ExcludeGlobs:            true,
		Offsets:                 true,
		ApproximateResults:      false,
		VerifiedResults:         true,
		GenerationTracking:      true,
	}
}

func newBleveIndexMapping() *blevemapping.IndexMappingImpl {
	indexMapping := bleve.NewIndexMapping()
	docMapping := blevemapping.NewDocumentMapping()

	pathField := blevemapping.NewKeywordFieldMapping()
	pathField.Store = false
	docMapping.AddFieldMappingsAt("path", pathField)

	contentField := blevemapping.NewKeywordFieldMapping()
	contentField.Store = false
	docMapping.AddFieldMappingsAt("content", contentField)

	foldedField := blevemapping.NewKeywordFieldMapping()
	foldedField.Store = false
	docMapping.AddFieldMappingsAt("content_folded", foldedField)

	indexMapping.DefaultMapping = docMapping
	return indexMapping
}

func newBleveIndexStatus(currentGen, indexedGen uint64) gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: currentGen,
		IndexedGeneration: indexedGen,
		Backend:           "bleve",
		Synchronous:       true,
	}
}
