package commands

import (
	"context"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestCommandFSSearchProviderForPathScopesResults(t *testing.T) {
	fsys := commandSearchCapableFS{
		FileSystem: gbfs.NewMemory(),
		provider: commandFixedHitsProvider{
			hits: []gbfs.SearchHit{
				{Path: "/workspace/a.txt", Verified: true},
				{Path: "/other/b.txt", Verified: true},
			},
		},
	}
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/",
		FileSystem: fsys,
	})

	provider, abs, ok, err := inv.FS.SearchProviderForPath(context.Background(), "/workspace")
	if err != nil {
		t.Fatalf("SearchProviderForPath() error = %v", err)
	}
	if !ok {
		t.Fatal("SearchProviderForPath() = false, want true")
	}
	if got, want := abs, "/workspace"; got != want {
		t.Fatalf("abs = %q, want %q", got, want)
	}

	caps := provider.SearchCapabilities()
	if !caps.RootRestriction {
		t.Fatal("RootRestriction = false, want true")
	}

	result, err := provider.Search(context.Background(), &gbfs.SearchQuery{
		Root:    "/workspace",
		Literal: "needle",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got, want := hitPaths(result.Hits), []string{"/workspace/a.txt"}; !equalStrings(got, want) {
		t.Fatalf("hits = %v, want %v", got, want)
	}
}

func TestCommandFSSearchProviderForPathRejectsOutOfScopeQuery(t *testing.T) {
	fsys := commandSearchCapableFS{
		FileSystem: gbfs.NewMemory(),
		provider:   commandFixedHitsProvider{},
	}
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/",
		FileSystem: fsys,
	})

	provider, _, ok, err := inv.FS.SearchProviderForPath(context.Background(), "/workspace")
	if err != nil {
		t.Fatalf("SearchProviderForPath() error = %v", err)
	}
	if !ok {
		t.Fatal("SearchProviderForPath() = false, want true")
	}

	_, err = provider.Search(context.Background(), &gbfs.SearchQuery{
		Root:    "/other",
		Literal: "needle",
	})
	if err == nil {
		t.Fatal("Search() error = nil, want out-of-scope error")
	}
}

func TestCommandFSSearchProviderForPathHonorsPolicy(t *testing.T) {
	fsys := commandSearchCapableFS{
		FileSystem: gbfs.NewMemory(),
		provider:   commandFixedHitsProvider{},
	}
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/",
		FileSystem: fsys,
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots: []string{"/workspace"},
		}),
	})

	_, _, _, err := inv.FS.SearchProviderForPath(context.Background(), "/other")
	if !policy.IsDenied(err) {
		t.Fatalf("SearchProviderForPath() error = %v, want denied error", err)
	}
}

type commandSearchCapableFS struct {
	gbfs.FileSystem
	provider gbfs.SearchProvider
}

func (f commandSearchCapableFS) SearchProviderForPath(string) (gbfs.SearchProvider, bool) {
	return f.provider, true
}

type commandFixedHitsProvider struct {
	hits []gbfs.SearchHit
}

func (p commandFixedHitsProvider) Search(context.Context, *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	return gbfs.SearchResult{
		Hits: p.hits,
		Status: gbfs.IndexStatus{
			CurrentGeneration: 1,
			IndexedGeneration: 1,
			Backend:           "command-fixed-hits",
		},
	}, nil
}

func (commandFixedHitsProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: false,
	}
}

func (commandFixedHitsProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "command-fixed-hits",
	}
}

func hitPaths(hits []gbfs.SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Path)
	}
	return out
}
