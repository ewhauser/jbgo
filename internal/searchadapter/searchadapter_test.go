package searchadapter

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"testing"

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
	return gbfs.SearchCapabilities{LiteralSearch: true}
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
	return gbfs.SearchCapabilities{LiteralSearch: true}
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
	return gbfs.SearchCapabilities{LiteralSearch: true}
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
	return gbfs.SearchCapabilities{LiteralSearch: true}
}

func (fixedHitsProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "fixed-hits",
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
