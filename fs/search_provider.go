package fs

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

type snapshotSearchIndexer interface {
	loadSnapshot(map[string][]byte) error
}

type searchRecord struct {
	data   []byte
	folded []byte
	ascii  bool
}

type memorySearchProvider struct {
	mu           sync.RWMutex
	records      map[string]searchRecord
	currentGen   uint64
	indexedGen   uint64
	capabilities SearchCapabilities
}

func newMemorySearchProvider() *memorySearchProvider {
	return &memorySearchProvider{
		records: make(map[string]searchRecord),
		capabilities: SearchCapabilities{
			LiteralSearch:           true,
			IgnoreCaseLiteralSearch: true,
			RootRestriction:         true,
			IncludeGlobs:            true,
			ExcludeGlobs:            true,
			Offsets:                 true,
			ApproximateResults:      false,
			VerifiedResults:         true,
			GenerationTracking:      true,
		},
	}
}

func (p *memorySearchProvider) Search(ctx context.Context, query *SearchQuery) (SearchResult, error) { //nolint:contextcheck // nil-guard replaces nil ctx with background
	if ctx == nil {
		ctx = context.Background()
	}
	if query == nil {
		return SearchResult{Status: p.IndexStatus()}, fmt.Errorf("fs: search query is required")
	}
	root := Clean(query.Root)
	if query.Literal == "" {
		return SearchResult{Status: p.IndexStatus()}, fmt.Errorf("fs: search query literal is required")
	}

	p.mu.RLock()
	status := IndexStatus{
		CurrentGeneration: p.currentGen,
		IndexedGeneration: p.indexedGen,
		Backend:           "memory",
		Synchronous:       true,
	}
	keys := make([]string, 0, len(p.records))
	for name := range p.records {
		if err := ctx.Err(); err != nil {
			p.mu.RUnlock()
			return SearchResult{Status: status}, err
		}
		keys = append(keys, name)
	}
	if err := ctx.Err(); err != nil {
		p.mu.RUnlock()
		return SearchResult{Status: status}, err
	}
	slices.Sort(keys)

	records := make([]struct {
		path   string
		record searchRecord
	}, 0, len(keys))
	for _, name := range keys {
		if err := ctx.Err(); err != nil {
			p.mu.RUnlock()
			return SearchResult{Status: status}, err
		}
		records = append(records, struct {
			path   string
			record searchRecord
		}{
			path:   name,
			record: p.records[name],
		})
	}
	p.mu.RUnlock()

	queryLiteral := []byte(query.Literal)
	foldedLiteral := foldASCIIBytes(queryLiteral)
	stale := status.CurrentGeneration != status.IndexedGeneration
	hits := make([]SearchHit, 0)

	for _, entry := range records {
		if err := ctx.Err(); err != nil {
			return SearchResult{
				Hits:   hits,
				Status: status,
			}, err
		}
		if !pathWithinSearchRoot(entry.path, root) {
			continue
		}
		if !searchPathMatchesGlobs(root, entry.path, query.IncludeGlobs, query.ExcludeGlobs) {
			continue
		}

		matched := false
		var offsets []int64
		if query.IgnoreCase {
			matched, offsets = searchRecordContainsIgnoreCase(entry.record, queryLiteral, foldedLiteral, query.WantOffsets)
		} else {
			matched, offsets = searchRecordContains(entry.record, queryLiteral, query.WantOffsets)
		}
		if !matched {
			continue
		}

		hit := SearchHit{
			Path:        entry.path,
			Offsets:     offsets,
			Approximate: false,
			Verified:    true,
			Stale:       stale,
		}
		hits = append(hits, hit)
		if query.Limit > 0 && len(hits) >= query.Limit {
			return SearchResult{
				Hits:      hits,
				Status:    status,
				Truncated: true,
			}, nil
		}
	}

	return SearchResult{
		Hits:   hits,
		Status: status,
	}, nil
}

func (p *memorySearchProvider) SearchCapabilities() SearchCapabilities {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capabilities
}

func (p *memorySearchProvider) IndexStatus() IndexStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return IndexStatus{
		CurrentGeneration: p.currentGen,
		IndexedGeneration: p.indexedGen,
		Backend:           "memory",
		Synchronous:       true,
	}
}

func (p *memorySearchProvider) ApplySearchMutation(_ context.Context, mutation *SearchMutation) error {
	if mutation == nil {
		return fmt.Errorf("fs: search mutation is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	switch mutation.Kind {
	case SearchMutationWrite:
		p.records[Clean(mutation.Path)] = newSearchRecord(mutation.Data)
	case SearchMutationRemove:
		removeSearchPrefix(p.records, Clean(mutation.Path))
	case SearchMutationRename:
		renameSearchPrefix(p.records, Clean(mutation.OldPath), Clean(mutation.NewPath))
	case SearchMutationMetadata:
		// Generation-only mutation.
	default:
		return fmt.Errorf("fs: unsupported search mutation kind %q", mutation.Kind)
	}

	p.currentGen++
	p.indexedGen = p.currentGen
	return nil
}

func (p *memorySearchProvider) loadSnapshot(snapshot map[string][]byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	records := make(map[string]searchRecord, len(snapshot))
	for name, data := range snapshot {
		records[Clean(name)] = newSearchRecord(data)
	}
	p.records = records
	p.currentGen = 0
	p.indexedGen = 0
	return nil
}

func newSearchRecord(data []byte) searchRecord {
	copied := append([]byte(nil), data...)
	return searchRecord{
		data:   copied,
		folded: foldASCIIBytes(copied),
		ascii:  isASCIIBytes(copied),
	}
}

func searchRecordContains(record searchRecord, literal []byte, wantOffsets bool) (matched bool, offsets []int64) {
	if !wantOffsets {
		return bytes.Contains(record.data, literal), nil
	}
	offsets = literalOffsets(record.data, literal)
	return len(offsets) > 0, offsets
}

func searchRecordContainsIgnoreCase(record searchRecord, literal, foldedLiteral []byte, wantOffsets bool) (matched bool, offsets []int64) {
	useASCIIFolding := record.ascii || !utf8.Valid(record.data) || !utf8.Valid(literal)
	if useASCIIFolding {
		if !wantOffsets {
			return bytes.Contains(record.folded, foldedLiteral), nil
		}
		offsets = literalOffsets(record.folded, foldedLiteral)
		return len(offsets) > 0, offsets
	}
	if !wantOffsets {
		return containsEqualFoldUTF8(record.data, string(literal)), nil
	}
	offsets = equalFoldOffsetsUTF8(record.data, string(literal))
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

func removeSearchPrefix(records map[string]searchRecord, prefix string) {
	for name := range records {
		if pathWithinSearchRoot(name, prefix) {
			delete(records, name)
		}
	}
}

func renameSearchPrefix(records map[string]searchRecord, oldPrefix, newPrefix string) {
	if oldPrefix == newPrefix {
		return
	}
	renamed := make(map[string]searchRecord)
	toDelete := make([]string, 0)
	for name, record := range records {
		if !pathWithinSearchRoot(name, oldPrefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, oldPrefix)
		target := Clean(newPrefix + suffix)
		renamed[target] = record
		toDelete = append(toDelete, name)
	}
	for _, name := range toDelete {
		delete(records, name)
	}
	maps.Copy(records, renamed)
}

type unsupportedSearchProvider struct{}

func (unsupportedSearchProvider) Search(context.Context, *SearchQuery) (SearchResult, error) {
	return SearchResult{}, ErrSearchUnsupported
}

func (unsupportedSearchProvider) SearchCapabilities() SearchCapabilities {
	return SearchCapabilities{}
}

func (unsupportedSearchProvider) IndexStatus() IndexStatus {
	return IndexStatus{Backend: "unsupported"}
}

func pathWithinSearchRoot(name, root string) bool {
	root = Clean(root)
	name = Clean(name)
	if root == "/" {
		return true
	}
	return name == root || strings.HasPrefix(name, root+"/")
}

func searchPathMatchesGlobs(root, pathValue string, includeGlobs, excludeGlobs []string) bool {
	root = Clean(root)
	pathValue = Clean(pathValue)
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
	if slices.ContainsFunc(targets, re.MatchString) {
		return true
	}
	return false
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
