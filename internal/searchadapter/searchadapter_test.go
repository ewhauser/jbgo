package searchadapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestSearchUsesIndexWhenSupportedAndFresh(t *testing.T) {
	fsys, err := gbfs.NewSearchableFileSystem(context.Background(), seededMemory(t, map[string]string{
		"/workspace/a.txt": "needle\n",
		"/workspace/b.txt": "other\n",
	}), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		WantOffsets:   true,
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("UsedIndex = false, want true")
	}
	if result.Truncated {
		t.Fatal("Truncated = true, want false")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/a.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
	if got, want := result.Hits[0].Offsets, []int64{0}; !slices.Equal(got, want) {
		t.Fatalf("Offsets = %v, want %v", got, want)
	}
}

func TestSearchResolvesRelativeRootsFromFilesystemCWD(t *testing.T) {
	fsys, err := gbfs.NewSearchableFileSystem(context.Background(), seededMemory(t, map[string]string{
		"/workspace/docs/a.txt": "needle\n",
		"/docs/b.txt":           "needle\n",
	}), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	if err := fsys.Chdir("/workspace"); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"docs"},
		Literal:       "needle",
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("UsedIndex = false, want true")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/docs/a.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchFallbackWhenUnsupported(t *testing.T) {
	fsys := seededMemory(t, map[string]string{
		"/workspace/a.txt":      "needle\n",
		"/workspace/skip/b.txt": "needle\n",
		"/workspace/c.log":      "needle\n",
	})

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IncludeGlobs:  []string{"**/*.txt"},
		ExcludeGlobs:  []string{"skip/**"},
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.UsedIndex {
		t.Fatal("UsedIndex = true, want false")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/a.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchFallbackSkipsSpecialFiles(t *testing.T) {
	fsys := fallbackFixtureFS{}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/"},
		Literal:       "needle",
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.UsedIndex {
		t.Fatal("UsedIndex = true, want false")
	}
	if got, want := hitPaths(result.Hits), []string{"/regular.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchFallbackWhenIneligible(t *testing.T) {
	fsys, err := gbfs.NewSearchableFileSystem(context.Background(), seededMemory(t, map[string]string{
		"/workspace/a.txt": "needle\n",
	}), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: false,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.UsedIndex {
		t.Fatal("UsedIndex = true, want false")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/a.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchFallbackWhenProviderLacksRequestedCapabilities(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		query     Query
		wantPaths []string
	}{
		{
			name: "ignore-case",
			files: map[string]string{
				"/workspace/a.txt": "Needle\n",
			},
			query: Query{
				Roots:         []string{"/workspace"},
				Literal:       "needle",
				IgnoreCase:    true,
				IndexEligible: true,
			},
			wantPaths: []string{"/workspace/a.txt"},
		},
		{
			name: "include-globs",
			files: map[string]string{
				"/workspace/a.txt": "needle\n",
				"/workspace/b.log": "needle\n",
			},
			query: Query{
				Roots:         []string{"/workspace"},
				Literal:       "needle",
				IncludeGlobs:  []string{"**/*.txt"},
				IndexEligible: true,
			},
			wantPaths: []string{"/workspace/a.txt"},
		},
		{
			name: "exclude-globs",
			files: map[string]string{
				"/workspace/a.txt":      "needle\n",
				"/workspace/skip/b.txt": "needle\n",
			},
			query: Query{
				Roots:         []string{"/workspace"},
				Literal:       "needle",
				ExcludeGlobs:  []string{"skip/**"},
				IndexEligible: true,
			},
			wantPaths: []string{"/workspace/a.txt"},
		},
		{
			name: "offsets",
			files: map[string]string{
				"/workspace/a.txt": "needle\n",
			},
			query: Query{
				Roots:         []string{"/workspace"},
				Literal:       "needle",
				WantOffsets:   true,
				IndexEligible: true,
			},
			wantPaths: []string{"/workspace/a.txt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys := searchCapableFS{
				FileSystem: seededMemory(t, tc.files),
				provider: limitedCapabilitiesProvider{
					hits: []gbfs.SearchHit{{Path: "/workspace/wrong.txt", Verified: true}},
				},
			}

			result, err := Search(context.Background(), fsys, &tc.query, nil)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if result.UsedIndex {
				t.Fatal("UsedIndex = true, want false")
			}
			if got := hitPaths(result.Hits); !slices.Equal(got, tc.wantPaths) {
				t.Fatalf("Paths = %v, want %v", got, tc.wantPaths)
			}
		})
	}
}

func TestSearchFallbackWhenProviderIsStale(t *testing.T) {
	mem := seededMemory(t, map[string]string{
		"/workspace/live.txt": "needle\n",
	})
	fsys := searchCapableFS{
		FileSystem: mem,
		provider: staleProvider{
			hits: []gbfs.SearchHit{{Path: "/workspace/wrong.txt", Verified: true}},
		},
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.UsedIndex {
		t.Fatal("UsedIndex = true, want false")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/live.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchFallbackWhenProviderBecomesStaleAfterSearch(t *testing.T) {
	mem := seededMemory(t, map[string]string{
		"/workspace/live.txt": "needle\n",
	})
	fsys := searchCapableFS{
		FileSystem: mem,
		provider: staleAfterSearchProvider{
			hits: []gbfs.SearchHit{{Path: "/workspace/wrong.txt", Verified: true}},
		},
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.UsedIndex {
		t.Fatal("UsedIndex = true, want false")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/live.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchVerifierFiltersIndexedCandidates(t *testing.T) {
	fsys, err := gbfs.NewSearchableFileSystem(context.Background(), seededMemory(t, map[string]string{
		"/workspace/a.txt": "needle\n",
		"/workspace/b.txt": "needle\n",
	}), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: true,
	}, func(_ context.Context, hit gbfs.SearchHit) (bool, error) {
		return hit.Path == "/workspace/b.txt", nil
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("UsedIndex = false, want true")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/b.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchVerifierAppliesLimitAfterFiltering(t *testing.T) {
	fsys := searchCapableFS{
		FileSystem: seededMemory(t, map[string]string{
			"/workspace/a.txt": "needle\n",
			"/workspace/b.txt": "needle\n",
		}),
		provider: fixedHitsProvider{
			hits: []gbfs.SearchHit{
				{Path: "/workspace/a.txt", Verified: true},
				{Path: "/workspace/b.txt", Verified: true},
			},
		},
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		Limit:         1,
		IndexEligible: true,
	}, func(_ context.Context, hit gbfs.SearchHit) (bool, error) {
		return hit.Path == "/workspace/b.txt", nil
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("UsedIndex = false, want true")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/b.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestSearchFallbackWhenProviderWrapsUnsupported(t *testing.T) {
	fsys := searchCapableFS{
		FileSystem: seededMemory(t, map[string]string{
			"/workspace/a.txt": "needle\n",
		}),
		provider: wrappedUnsupportedProvider{},
	}

	result, err := Search(context.Background(), fsys, &Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.UsedIndex {
		t.Fatal("UsedIndex = true, want false")
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/a.txt"}; !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}

func TestRegexpPrefilterLiterals(t *testing.T) {
	tests := []struct {
		name       string
		pattern    string
		want       []string
		wantUsable bool
	}{
		{
			name:       "fixed-string-alternation",
			pattern:    "(?:a\\.c|foo)",
			want:       []string{"a.c", "foo"},
			wantUsable: true,
		},
		{
			name:       "concat-picks-mandatory-literal",
			pattern:    "foo.*bar",
			want:       []string{"bar"},
			wantUsable: true,
		},
		{
			name:       "alternation-unions-branches",
			pattern:    "foo|bar",
			want:       []string{"bar", "foo"},
			wantUsable: true,
		},
		{
			name:       "nested-alternation-preserves-branch-union",
			pattern:    "(?:abcd|wxyz)e|zzz",
			want:       []string{"abcd", "wxyz", "zzz"},
			wantUsable: true,
		},
		{
			name:       "optional-pattern-falls-back",
			pattern:    "a*",
			want:       nil,
			wantUsable: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)
			got, ok := RegexpPrefilterLiterals(re)
			if ok != tc.wantUsable {
				t.Fatalf("RegexpPrefilterLiterals(%q) usable = %v, want %v", tc.pattern, ok, tc.wantUsable)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("RegexpPrefilterLiterals(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestRegexpPrefilterLiteralsRejectsBroadAlternations(t *testing.T) {
	re := regexp.MustCompile("aa|bb|cc|dd|ee|ff|gg|hh|ii")

	got, ok := RegexpPrefilterLiterals(re)
	if ok {
		t.Fatalf("RegexpPrefilterLiterals() usable = true, want false; got %v", got)
	}
	if got != nil {
		t.Fatalf("RegexpPrefilterLiterals() = %v, want nil", got)
	}
}

func TestPrefilterCandidatesUsesLiteralUnionAndEligibleIntersection(t *testing.T) {
	provider := &literalHitsProvider{
		hitsByLiteral: map[string][]gbfs.SearchHit{
			"bar": {
				{Path: "/workspace/bar.txt", Verified: true},
				{Path: "/workspace/outside.txt", Verified: true},
			},
			"foo": {
				{Path: "/workspace/foo.txt", Verified: true},
			},
		},
	}

	result, err := PrefilterCandidates(context.Background(), provider, "/workspace", []string{
		"/workspace/foo.txt",
		"/workspace/miss.txt",
	}, regexp.MustCompile("foo|bar"), false)
	if err != nil {
		t.Fatalf("PrefilterCandidates() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("UsedIndex = false, want true")
	}
	if got, want := result.Literals, []string{"bar", "foo"}; !slices.Equal(got, want) {
		t.Fatalf("Literals = %v, want %v", got, want)
	}
	if got, want := sortedCandidatePaths(result.CandidatePaths), []string{"/workspace/foo.txt"}; !slices.Equal(got, want) {
		t.Fatalf("CandidatePaths = %v, want %v", got, want)
	}
	if got, want := provider.queries, []string{"bar", "foo"}; !slices.Equal(got, want) {
		t.Fatalf("provider queries = %v, want %v", got, want)
	}
}

func TestPrefilterCandidatesFallsBackWhenProviderIsStale(t *testing.T) {
	result, err := PrefilterCandidates(context.Background(), staleProvider{}, "/workspace", []string{"/workspace/a.txt"}, regexp.MustCompile("needle"), false)
	if err != nil {
		t.Fatalf("PrefilterCandidates() error = %v", err)
	}
	if result.UsedIndex {
		t.Fatal("UsedIndex = true, want false")
	}
}

func TestPrefilterCandidatesUsesIgnoreCaseWhenRegexpContainsInlineFlag(t *testing.T) {
	provider := &caseAwareLiteralProvider{}

	result, err := PrefilterCandidates(context.Background(), provider, "/workspace", []string{
		"/workspace/hit.txt",
	}, regexp.MustCompile("(?i)Needle"), false)
	if err != nil {
		t.Fatalf("PrefilterCandidates() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("UsedIndex = false, want true")
	}
	if got, want := sortedCandidatePaths(result.CandidatePaths), []string{"/workspace/hit.txt"}; !slices.Equal(got, want) {
		t.Fatalf("CandidatePaths = %v, want %v", got, want)
	}
	if len(provider.queries) != 1 {
		t.Fatalf("query count = %d, want 1", len(provider.queries))
	}
	if !provider.queries[0].IgnoreCase {
		t.Fatal("query IgnoreCase = false, want true")
	}
}

type searchCapableFS struct {
	gbfs.FileSystem
	provider gbfs.SearchProvider
}

func (f searchCapableFS) SearchProviderForPath(string) (gbfs.SearchProvider, bool) {
	return f.provider, true
}

type staleProvider struct {
	hits []gbfs.SearchHit
}

func (p staleProvider) Search(context.Context, *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	return gbfs.SearchResult{
		Hits: p.hits,
		Status: gbfs.IndexStatus{
			CurrentGeneration: 2,
			IndexedGeneration: 1,
			Backend:           "stale-test",
		},
	}, nil
}

func (staleProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (staleProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 2,
		IndexedGeneration: 1,
		Backend:           "stale-test",
	}
}

type staleAfterSearchProvider struct {
	hits []gbfs.SearchHit
}

func (p staleAfterSearchProvider) Search(context.Context, *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	return gbfs.SearchResult{
		Hits: p.hits,
		Status: gbfs.IndexStatus{
			CurrentGeneration: 2,
			IndexedGeneration: 1,
			Backend:           "stale-after-search",
		},
	}, nil
}

func (staleAfterSearchProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (staleAfterSearchProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "stale-after-search",
	}
}

type wrappedUnsupportedProvider struct{}

func (wrappedUnsupportedProvider) Search(context.Context, *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	return gbfs.SearchResult{}, fmt.Errorf("wrapped unsupported: %w", gbfs.ErrSearchUnsupported)
}

func (wrappedUnsupportedProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (wrappedUnsupportedProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "wrapped-unsupported",
	}
}

type fixedHitsProvider struct {
	hits []gbfs.SearchHit
}

func (p fixedHitsProvider) Search(_ context.Context, query *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	hits := append([]gbfs.SearchHit(nil), p.hits...)
	if query != nil && query.Limit > 0 && len(hits) > query.Limit {
		return gbfs.SearchResult{
			Hits:      hits[:query.Limit],
			Truncated: true,
			Status: gbfs.IndexStatus{
				CurrentGeneration: 1,
				IndexedGeneration: 1,
				Backend:           "fixed-hits",
			},
		}, nil
	}
	return gbfs.SearchResult{
		Hits: hits,
		Status: gbfs.IndexStatus{
			CurrentGeneration: 1,
			IndexedGeneration: 1,
			Backend:           "fixed-hits",
		},
	}, nil
}

func (fixedHitsProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (fixedHitsProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "fixed-hits",
	}
}

type limitedCapabilitiesProvider struct {
	hits []gbfs.SearchHit
}

func (p limitedCapabilitiesProvider) Search(_ context.Context, query *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	hits := append([]gbfs.SearchHit(nil), p.hits...)
	if query != nil && query.Limit > 0 && len(hits) > query.Limit {
		hits = hits[:query.Limit]
	}
	return gbfs.SearchResult{
		Hits: hits,
		Status: gbfs.IndexStatus{
			CurrentGeneration: 1,
			IndexedGeneration: 1,
			Backend:           "limited-capabilities",
		},
	}, nil
}

func (limitedCapabilitiesProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (limitedCapabilitiesProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "limited-capabilities",
	}
}

type caseAwareLiteralProvider struct {
	queries []gbfs.SearchQuery
}

func (p *caseAwareLiteralProvider) Search(_ context.Context, query *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	if query != nil {
		p.queries = append(p.queries, *query)
	}
	hits := []gbfs.SearchHit{}
	if query != nil && query.IgnoreCase && query.Literal == "NEEDLE" {
		hits = append(hits, gbfs.SearchHit{Path: "/workspace/hit.txt", Verified: true})
	}
	return gbfs.SearchResult{
		Hits: hits,
		Status: gbfs.IndexStatus{
			CurrentGeneration: 1,
			IndexedGeneration: 1,
			Backend:           "case-aware-literal",
		},
	}, nil
}

func (*caseAwareLiteralProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:           true,
		IgnoreCaseLiteralSearch: true,
		RootRestriction:         true,
	}
}

func (*caseAwareLiteralProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "case-aware-literal",
	}
}

type literalHitsProvider struct {
	hitsByLiteral map[string][]gbfs.SearchHit
	queries       []string
}

func (p *literalHitsProvider) Search(_ context.Context, query *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	p.queries = append(p.queries, query.Literal)
	hits := append([]gbfs.SearchHit(nil), p.hitsByLiteral[query.Literal]...)
	return gbfs.SearchResult{
		Hits: hits,
		Status: gbfs.IndexStatus{
			CurrentGeneration: 1,
			IndexedGeneration: 1,
			Backend:           "literal-hits",
		},
	}, nil
}

func (*literalHitsProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (*literalHitsProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "literal-hits",
	}
}

func seededMemory(t *testing.T, files map[string]string) gbfs.FileSystem {
	t.Helper()
	mem := gbfs.NewMemory()
	for name, contents := range files {
		writeFile(t, mem, name, contents)
	}
	return mem
}

func writeFile(t *testing.T, fsys gbfs.FileSystem, name, contents string) {
	t.Helper()
	if err := fsys.MkdirAll(context.Background(), path.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", name, err)
	}
	file, err := fsys.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	if _, err := io.WriteString(file, contents); err != nil {
		t.Fatalf("WriteString(%q) error = %v", name, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q) error = %v", name, err)
	}
}

func hitPaths(hits []gbfs.SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Path)
	}
	return out
}

func sortedCandidatePaths(paths map[string]struct{}) []string {
	out := make([]string, 0, len(paths))
	for name := range paths {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

type fallbackFixtureFS struct{}

func (fallbackFixtureFS) Open(_ context.Context, name string) (gbfs.File, error) {
	switch gbfs.Clean(name) {
	case "/regular.txt":
		info := fallbackFixtureInfo{name: "regular.txt", mode: 0o644, size: int64(len("needle\n"))}
		return fallbackFixtureFile{Reader: bytes.NewReader([]byte("needle\n")), info: info}, nil
	case "/special.pipe":
		return nil, fmt.Errorf("special file should not be opened")
	case "/special-link":
		return nil, fmt.Errorf("special symlink target should not be opened")
	default:
		return nil, &os.PathError{Op: "open", Path: gbfs.Clean(name), Err: stdfs.ErrNotExist}
	}
}

func (fs fallbackFixtureFS) OpenFile(ctx context.Context, name string, _ int, _ stdfs.FileMode) (gbfs.File, error) {
	return fs.Open(ctx, name)
}

func (fallbackFixtureFS) Stat(_ context.Context, name string) (stdfs.FileInfo, error) {
	switch gbfs.Clean(name) {
	case "/":
		return fallbackFixtureInfo{name: "/", mode: stdfs.ModeDir | 0o755}, nil
	case "/regular.txt":
		return fallbackFixtureInfo{name: "regular.txt", mode: 0o644, size: int64(len("needle\n"))}, nil
	case "/special.pipe":
		return fallbackFixtureInfo{name: "special.pipe", mode: stdfs.ModeNamedPipe}, nil
	case "/special-link":
		return fallbackFixtureInfo{name: "special-link", mode: stdfs.ModeNamedPipe}, nil
	default:
		return nil, &os.PathError{Op: "stat", Path: gbfs.Clean(name), Err: stdfs.ErrNotExist}
	}
}

func (fallbackFixtureFS) Lstat(_ context.Context, name string) (stdfs.FileInfo, error) {
	switch gbfs.Clean(name) {
	case "/":
		return fallbackFixtureInfo{name: "/", mode: stdfs.ModeDir | 0o755}, nil
	case "/regular.txt":
		return fallbackFixtureInfo{name: "regular.txt", mode: 0o644, size: int64(len("needle\n"))}, nil
	case "/special.pipe":
		return fallbackFixtureInfo{name: "special.pipe", mode: stdfs.ModeNamedPipe}, nil
	case "/special-link":
		return fallbackFixtureInfo{name: "special-link", mode: stdfs.ModeSymlink | 0o777, size: int64(len("special.pipe"))}, nil
	default:
		return nil, &os.PathError{Op: "lstat", Path: gbfs.Clean(name), Err: stdfs.ErrNotExist}
	}
}

func (fallbackFixtureFS) ReadDir(_ context.Context, name string) ([]stdfs.DirEntry, error) {
	if gbfs.Clean(name) != "/" {
		return nil, &os.PathError{Op: "readdir", Path: gbfs.Clean(name), Err: stdfs.ErrNotExist}
	}
	return []stdfs.DirEntry{
		stdfs.FileInfoToDirEntry(fallbackFixtureInfo{name: "regular.txt", mode: 0o644, size: int64(len("needle\n"))}),
		stdfs.FileInfoToDirEntry(fallbackFixtureInfo{name: "special.pipe", mode: stdfs.ModeNamedPipe}),
		stdfs.FileInfoToDirEntry(fallbackFixtureInfo{name: "special-link", mode: stdfs.ModeSymlink | 0o777, size: int64(len("special.pipe"))}),
	}, nil
}

func (fallbackFixtureFS) Readlink(_ context.Context, name string) (string, error) {
	if gbfs.Clean(name) == "/special-link" {
		return "special.pipe", nil
	}
	return "", &os.PathError{Op: "readlink", Path: gbfs.Clean(name), Err: stdfs.ErrInvalid}
}

func (fallbackFixtureFS) Realpath(_ context.Context, name string) (string, error) {
	return gbfs.Clean(name), nil
}

func (fallbackFixtureFS) Symlink(context.Context, string, string) error { return stdfs.ErrPermission }
func (fallbackFixtureFS) Link(context.Context, string, string) error    { return stdfs.ErrPermission }
func (fallbackFixtureFS) Chown(context.Context, string, uint32, uint32, bool) error {
	return stdfs.ErrPermission
}
func (fallbackFixtureFS) Chmod(context.Context, string, stdfs.FileMode) error {
	return stdfs.ErrPermission
}
func (fallbackFixtureFS) Chtimes(context.Context, string, time.Time, time.Time) error {
	return stdfs.ErrPermission
}
func (fallbackFixtureFS) MkdirAll(context.Context, string, stdfs.FileMode) error {
	return stdfs.ErrPermission
}
func (fallbackFixtureFS) Remove(context.Context, string, bool) error   { return stdfs.ErrPermission }
func (fallbackFixtureFS) Rename(context.Context, string, string) error { return stdfs.ErrPermission }
func (fallbackFixtureFS) Getwd() string                                { return "/" }
func (fallbackFixtureFS) Chdir(string) error                           { return stdfs.ErrPermission }

type fallbackFixtureFile struct {
	*bytes.Reader
	info stdfs.FileInfo
}

func (fallbackFixtureFile) Write([]byte) (int, error) { return 0, stdfs.ErrPermission }
func (fallbackFixtureFile) Close() error              { return nil }
func (f fallbackFixtureFile) Stat() (stdfs.FileInfo, error) {
	return f.info, nil
}

type fallbackFixtureInfo struct {
	name string
	mode stdfs.FileMode
	size int64
}

func (i fallbackFixtureInfo) Name() string         { return i.name }
func (i fallbackFixtureInfo) Size() int64          { return i.size }
func (i fallbackFixtureInfo) Mode() stdfs.FileMode { return i.mode }
func (i fallbackFixtureInfo) ModTime() time.Time   { return time.Unix(0, 0).UTC() }
func (i fallbackFixtureInfo) IsDir() bool          { return i.mode.IsDir() }
func (i fallbackFixtureInfo) Sys() any             { return nil }
