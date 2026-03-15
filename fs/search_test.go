package fs

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"testing"
	"time"
)

func TestSearchProviderQueries(t *testing.T) {
	base := seededMemory(t, map[string]string{
		"/workspace/docs/readme.txt": "alpha needle omega\n",
		"/workspace/src/main.go":     "package main\nconst greeting = \"Hello\"\n",
		"/workspace/src/util.go":     "package main\nconst secondary = \"hello\"\n",
		"/workspace/tmp/ignore.txt":  "needle hidden\n",
		"/workspace/logs/app.log":    "abc needle def needle\n",
	})

	fsys, err := NewSearchableFileSystem(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	provider := mustSearchProvider(t, fsys, "/workspace")

	tests := []struct {
		name          string
		query         SearchQuery
		wantPaths     []string
		wantOffsets   map[string][]int64
		wantTruncated bool
	}{
		{
			name: "basic-literal",
			query: SearchQuery{
				Root:    "/workspace",
				Literal: "needle",
			},
			wantPaths: []string{
				"/workspace/docs/readme.txt",
				"/workspace/logs/app.log",
				"/workspace/tmp/ignore.txt",
			},
		},
		{
			name: "case-insensitive",
			query: SearchQuery{
				Root:       "/workspace/src",
				Literal:    "HELLO",
				IgnoreCase: true,
			},
			wantPaths: []string{
				"/workspace/src/main.go",
				"/workspace/src/util.go",
			},
		},
		{
			name: "include-exclude-globs",
			query: SearchQuery{
				Root:         "/workspace",
				Literal:      "needle",
				IncludeGlobs: []string{"**/*.txt"},
				ExcludeGlobs: []string{"tmp/**"},
			},
			wantPaths: []string{
				"/workspace/docs/readme.txt",
			},
		},
		{
			name: "path-prefix",
			query: SearchQuery{
				Root:    "/workspace/docs",
				Literal: "needle",
			},
			wantPaths: []string{
				"/workspace/docs/readme.txt",
			},
		},
		{
			name: "offsets",
			query: SearchQuery{
				Root:        "/workspace/logs",
				Literal:     "needle",
				WantOffsets: true,
			},
			wantPaths: []string{
				"/workspace/logs/app.log",
			},
			wantOffsets: map[string][]int64{
				"/workspace/logs/app.log": {4, 15},
			},
		},
		{
			name: "limit-truncates",
			query: SearchQuery{
				Root:    "/workspace",
				Literal: "needle",
				Limit:   1,
			},
			wantPaths: []string{
				"/workspace/docs/readme.txt",
			},
			wantTruncated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := provider.Search(context.Background(), &tc.query)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}

			gotPaths := make([]string, 0, len(result.Hits))
			for _, hit := range result.Hits {
				gotPaths = append(gotPaths, hit.Path)
				if want := tc.wantOffsets[hit.Path]; tc.wantOffsets != nil && !slices.Equal(hit.Offsets, want) {
					t.Fatalf("Offsets(%s) = %v, want %v", hit.Path, hit.Offsets, want)
				}
				if hit.Approximate {
					t.Fatalf("Hit(%s).Approximate = true, want false", hit.Path)
				}
				if !hit.Verified {
					t.Fatalf("Hit(%s).Verified = false, want true", hit.Path)
				}
				if hit.Stale {
					t.Fatalf("Hit(%s).Stale = true, want false", hit.Path)
				}
			}

			if !slices.Equal(gotPaths, tc.wantPaths) {
				t.Fatalf("Paths = %v, want %v", gotPaths, tc.wantPaths)
			}
			if result.Truncated != tc.wantTruncated {
				t.Fatalf("Truncated = %v, want %v", result.Truncated, tc.wantTruncated)
			}
			if got, want := result.Status.CurrentGeneration, uint64(0); got != want {
				t.Fatalf("CurrentGeneration = %d, want %d", got, want)
			}
			if got, want := result.Status.IndexedGeneration, uint64(0); got != want {
				t.Fatalf("IndexedGeneration = %d, want %d", got, want)
			}
		})
	}
}

func TestSearchableFSMutations(t *testing.T) {
	fsys, err := NewSearchableFileSystem(context.Background(), NewMemory(), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	provider := mustSearchProvider(t, fsys, "/")

	if got, want := provider.IndexStatus().CurrentGeneration, uint64(0); got != want {
		t.Fatalf("initial generation = %d, want %d", got, want)
	}

	writeSearchFile(t, fsys, "/docs/file.txt", "hello needle\n")
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, []string{"/docs/file.txt"})
	assertIndexGeneration(t, provider, 2)

	if err := fsys.Rename(context.Background(), "/docs/file.txt", "/docs/renamed.txt"); err != nil {
		t.Fatalf("Rename(file) error = %v", err)
	}
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, []string{"/docs/renamed.txt"})
	assertIndexGeneration(t, provider, 3)

	writeSearchFile(t, fsys, "/tree/sub/child.txt", "needle child\n")
	if err := fsys.Rename(context.Background(), "/tree", "/moved"); err != nil {
		t.Fatalf("Rename(dir) error = %v", err)
	}
	assertSearchPaths(t, provider, &SearchQuery{Root: "/moved", Literal: "needle"}, []string{"/moved/sub/child.txt"})
	assertIndexGeneration(t, provider, 6)

	if err := fsys.Remove(context.Background(), "/docs/renamed.txt", false); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, nil)
	assertIndexGeneration(t, provider, 7)

	if err := fsys.MkdirAll(context.Background(), "/meta", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.Chtimes(context.Background(), "/moved/sub/child.txt", time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	assertIndexGeneration(t, provider, 9)
}

func TestSearchableFSIndexesNewLinks(t *testing.T) {
	fsys, err := NewSearchableFileSystem(context.Background(), NewMemory(), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	provider := mustSearchProvider(t, fsys, "/")

	writeSearchFile(t, fsys, "/docs/file.txt", "hello needle\n")
	if err := fsys.Symlink(context.Background(), "file.txt", "/docs/link-sym.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := fsys.Link(context.Background(), "/docs/file.txt", "/docs/link-hard.txt"); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, []string{
		"/docs/file.txt",
		"/docs/link-hard.txt",
		"/docs/link-sym.txt",
	})

	writeSearchFile(t, fsys, "/docs/file.txt", "fresh token\n")
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "fresh"}, []string{
		"/docs/file.txt",
		"/docs/link-hard.txt",
		"/docs/link-sym.txt",
	})
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, nil)

	status := provider.IndexStatus()
	if status.CurrentGeneration != status.IndexedGeneration {
		t.Fatalf("index status = %+v, want current generation indexed", status)
	}
}

func TestSearchableFSTracksDanglingSymlinkUntilTargetExists(t *testing.T) {
	fsys, err := NewSearchableFileSystem(context.Background(), NewMemory(), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	provider := mustSearchProvider(t, fsys, "/")

	if err := fsys.MkdirAll(context.Background(), "/docs", 0o755); err != nil {
		t.Fatalf("MkdirAll(/docs) error = %v", err)
	}
	if err := fsys.Symlink(context.Background(), "future.txt", "/docs/link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, nil)

	writeSearchFile(t, fsys, "/docs/future.txt", "needle later\n")
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, []string{
		"/docs/future.txt",
		"/docs/link.txt",
	})
}

func TestSearchableFSBootstrapsExistingSymlinkTracking(t *testing.T) {
	base := NewMemory()
	writeSearchFile(t, base, "/docs/target.txt", "needle before\n")
	if err := base.Symlink(context.Background(), "target.txt", "/docs/link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	fsys, err := NewSearchableFileSystem(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	provider := mustSearchProvider(t, fsys, "/")

	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, []string{
		"/docs/link.txt",
		"/docs/target.txt",
	})

	writeSearchFile(t, fsys, "/docs/target.txt", "fresh after wrap\n")
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "fresh"}, []string{
		"/docs/link.txt",
		"/docs/target.txt",
	})
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, nil)
}

func TestSearchableFSBootstrapsExistingHardLinkTracking(t *testing.T) {
	base := NewMemory()
	writeSearchFile(t, base, "/docs/target.txt", "needle before\n")
	if err := base.Link(context.Background(), "/docs/target.txt", "/docs/link.txt"); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	fsys, err := NewSearchableFileSystem(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	provider := mustSearchProvider(t, fsys, "/")

	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, []string{
		"/docs/link.txt",
		"/docs/target.txt",
	})

	writeSearchFile(t, fsys, "/docs/target.txt", "fresh after wrap\n")
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "fresh"}, []string{
		"/docs/link.txt",
		"/docs/target.txt",
	})
	assertSearchPaths(t, provider, &SearchQuery{Root: "/docs", Literal: "needle"}, nil)
}

func TestSearchableFSOpenFileTracksCreateWithoutWrite(t *testing.T) {
	fsys, err := NewSearchableFileSystem(context.Background(), NewMemory(), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	provider := mustSearchProvider(t, fsys, "/")

	file, err := fsys.OpenFile(context.Background(), "/empty.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(create) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(create) error = %v", err)
	}
	if got, want := provider.IndexStatus().CurrentGeneration, uint64(1); got != want {
		t.Fatalf("CurrentGeneration = %d, want %d", got, want)
	}

	file, err = fsys.OpenFile(context.Background(), "/empty.txt", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile(trunc) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(trunc) error = %v", err)
	}
	if got, want := provider.IndexStatus().CurrentGeneration, uint64(2); got != want {
		t.Fatalf("CurrentGeneration = %d, want %d", got, want)
	}
}

func TestMountableFSSearchProviderForPath(t *testing.T) {
	base, err := NewSearchableFileSystem(context.Background(), seededMemory(t, map[string]string{
		"/base.txt": "base needle\n",
	}), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem(base) error = %v", err)
	}
	child, err := NewSearchableFileSystem(context.Background(), seededMemory(t, map[string]string{
		"/child.txt": "mount needle\n",
	}), nil)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem(child) error = %v", err)
	}

	mountable := NewMountable(base)
	if err := mountable.Mount("/workspace", child); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	rootProvider, ok := mountable.SearchProviderForPath("/")
	if ok || rootProvider != nil {
		t.Fatalf("SearchProviderForPath(/) = %v, %v, want nil,false", rootProvider, ok)
	}

	baseProvider, ok := mountable.SearchProviderForPath("/base.txt")
	if !ok {
		t.Fatal("SearchProviderForPath(/base.txt) = false, want true")
	}
	baseResult, err := baseProvider.Search(context.Background(), &SearchQuery{
		Root:    "/",
		Literal: "needle",
	})
	if err != nil {
		t.Fatalf("Search(base) error = %v", err)
	}
	if got, want := searchHitPaths(baseResult.Hits), []string{"/base.txt"}; !slices.Equal(got, want) {
		t.Fatalf("base paths = %v, want %v", got, want)
	}

	mountProvider, ok := mountable.SearchProviderForPath("/workspace")
	if !ok {
		t.Fatal("SearchProviderForPath(/workspace) = false, want true")
	}
	mountResult, err := mountProvider.Search(context.Background(), &SearchQuery{
		Root:    "/workspace",
		Literal: "needle",
	})
	if err != nil {
		t.Fatalf("Search(mount) error = %v", err)
	}
	if got, want := searchHitPaths(mountResult.Hits), []string{"/workspace/child.txt"}; !slices.Equal(got, want) {
		t.Fatalf("mount paths = %v, want %v", got, want)
	}
}

func TestUnsupportedSearchProvider(t *testing.T) {
	provider := NewUnsupportedSearchProvider()
	_, err := provider.Search(context.Background(), &SearchQuery{Literal: "needle"})
	if !errors.Is(err, ErrSearchUnsupported) {
		t.Fatalf("Search() error = %v, want ErrSearchUnsupported", err)
	}
}

func mustSearchProvider(t *testing.T, fsys FileSystem, root string) SearchProvider {
	t.Helper()
	capable, ok := fsys.(SearchCapable)
	if !ok {
		t.Fatalf("filesystem %T does not implement SearchCapable", fsys)
	}
	provider, ok := capable.SearchProviderForPath(root)
	if !ok {
		t.Fatalf("SearchProviderForPath(%q) = false", root)
	}
	return provider
}

func writeSearchFile(t *testing.T, fsys FileSystem, name, contents string) {
	t.Helper()
	dir := parentDir(name)
	if err := fsys.MkdirAll(context.Background(), dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
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

func assertSearchPaths(t *testing.T, provider SearchProvider, query *SearchQuery, want []string) {
	t.Helper()
	result, err := provider.Search(context.Background(), query)
	if err != nil {
		t.Fatalf("Search(%+v) error = %v", query, err)
	}
	if got := searchHitPaths(result.Hits); !slices.Equal(got, want) {
		t.Fatalf("Search(%+v) paths = %v, want %v", query, got, want)
	}
}

func searchHitPaths(hits []SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Path)
	}
	return out
}

func assertIndexGeneration(t *testing.T, provider SearchProvider, want uint64) {
	t.Helper()
	status := provider.IndexStatus()
	if status.CurrentGeneration != want {
		t.Fatalf("CurrentGeneration = %d, want %d", status.CurrentGeneration, want)
	}
	if status.IndexedGeneration != want {
		t.Fatalf("IndexedGeneration = %d, want %d", status.IndexedGeneration, want)
	}
}
