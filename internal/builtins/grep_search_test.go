package builtins_test

import (
	"context"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"sync"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestGrepUsesIndexedPrefilterOnSearchableFS(t *testing.T) {
	fsys, provider := newCountedSearchableFS(t, map[string]string{
		"/workspace/hit.txt":  "needle\n",
		"/workspace/miss.txt": "other\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "grep -r needle /workspace\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/workspace/hit.txt:needle\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if provider.SearchCount() == 0 {
		t.Fatal("SearchCount = 0, want indexed query")
	}
	if got := fsys.OpenCount("/workspace/hit.txt"); got == 0 {
		t.Fatal("OpenCount(hit) = 0, want verification read")
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got != 0 {
		t.Fatalf("OpenCount(miss) = %d, want 0", got)
	}
}

func TestGrepFallsBackWhenProviderIsStale(t *testing.T) {
	fsys := newCountedSearchCapableFS(t, map[string]string{
		"/workspace/miss.txt": "other\n",
	}, grepStaleProvider{})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "grep needle /workspace/miss.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got == 0 {
		t.Fatal("OpenCount(miss) = 0, want fallback read")
	}
}

func TestGrepFallsBackWhenProviderIsUnsupported(t *testing.T) {
	provider := &grepUnsupportedProvider{}
	fsys := newCountedSearchCapableFS(t, map[string]string{
		"/workspace/miss.txt": "other\n",
	}, provider)
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "grep needle /workspace/miss.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if provider.SearchCount() == 0 {
		t.Fatal("SearchCount = 0, want attempted provider search")
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got == 0 {
		t.Fatal("OpenCount(miss) = 0, want fallback read")
	}
}

func TestGrepUsesIndexPerRootOnMountableFS(t *testing.T) {
	indexedFS, indexedProvider := newCountedSearchableFS(t, map[string]string{
		"/hit.txt":  "needle\n",
		"/miss.txt": "other\n",
	})
	staleFS := newCountedSearchCapableFS(t, map[string]string{
		"/hit.txt":  "needle\n",
		"/miss.txt": "other\n",
	}, grepStaleProvider{})

	mountable := gbfs.NewMountable(gbfs.NewMemory())
	if err := mountable.Mount("/indexed", indexedFS); err != nil {
		t.Fatalf("Mount(/indexed) error = %v", err)
	}
	if err := mountable.Mount("/stale", staleFS); err != nil {
		t.Fatalf("Mount(/stale) error = %v", err)
	}

	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(mountable), "/"),
	})

	result := mustExecSession(t, session, "grep -r needle /indexed /stale\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/indexed/hit.txt:needle", "/stale/hit.txt:needle"} {
		if !containsLine(lines(result.Stdout), want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
	if indexedProvider.SearchCount() == 0 {
		t.Fatal("indexed SearchCount = 0, want indexed query")
	}
	if got := indexedFS.OpenCount("/hit.txt"); got == 0 {
		t.Fatal("indexed hit open count = 0, want verification read")
	}
	if got := indexedFS.OpenCount("/miss.txt"); got != 0 {
		t.Fatalf("indexed miss open count = %d, want 0", got)
	}
	if got := staleFS.OpenCount("/miss.txt"); got == 0 {
		t.Fatal("stale miss open count = 0, want fallback read")
	}
}

func TestGrepGuaranteedMissModesUseIndexWithoutReadingFile(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantStdout string
		wantExit   int
	}{
		{
			name:       "default",
			script:     "grep needle /workspace/miss.txt\n",
			wantStdout: "",
			wantExit:   1,
		},
		{
			name:       "count",
			script:     "grep -c needle /workspace/miss.txt\n",
			wantStdout: "0\n",
			wantExit:   1,
		},
		{
			name:       "files-without-match",
			script:     "grep -L needle /workspace/miss.txt\n",
			wantStdout: "/workspace/miss.txt\n",
			wantExit:   0,
		},
		{
			name:       "quiet",
			script:     "grep -q needle /workspace/miss.txt\n",
			wantStdout: "",
			wantExit:   1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys, _ := newCountedSearchableFS(t, map[string]string{
				"/workspace/miss.txt": "other\n",
			})
			session := newSession(t, &Config{
				FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
			})

			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantExit, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantStdout {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantStdout)
			}
			if got := fsys.OpenCount("/workspace/miss.txt"); got != 0 {
				t.Fatalf("OpenCount(miss) = %d, want 0", got)
			}
		})
	}
}

func TestGrepInvertMatchSkipsIndexedPrefilter(t *testing.T) {
	fsys, _ := newCountedSearchableFS(t, map[string]string{
		"/workspace/miss.txt": "other\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "grep -v needle /workspace/miss.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "other\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got == 0 {
		t.Fatal("OpenCount(miss) = 0, want direct read")
	}
}

func TestGrepRecursiveSymlinkDirectoryFallsBackToDirectReads(t *testing.T) {
	fsys, _ := newCountedSearchableFS(t, map[string]string{
		"/workspace/real/miss.txt": "other\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
		Policy: policy.NewStatic(&policy.Config{
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	if err := session.FileSystem().Symlink(context.Background(), "real", "/workspace/linkdir"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	result := mustExecSession(t, session, "grep -r needle /workspace/linkdir\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got := fsys.OpenCount("/workspace/linkdir/miss.txt"); got == 0 {
		t.Fatal("OpenCount(linkdir/miss.txt) = 0, want fallback read")
	}
}

func TestGrepQuietStopsBeforeLaterOperands(t *testing.T) {
	fsys, _ := newCountedSearchableFS(t, map[string]string{
		"/workspace/hit.txt": "needle\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "grep -q needle /workspace/hit.txt /workspace/missing.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func factoryForFS(fsys gbfs.FileSystem) gbfs.Factory {
	return gbfs.FactoryFunc(func(context.Context) (gbfs.FileSystem, error) {
		return fsys, nil
	})
}

func newCountedSearchableFS(t *testing.T, files map[string]string) (*countingOpenFS, *countingSearchIndexer) {
	t.Helper()

	base := seededMemoryFS(t, files)
	provider := newCountingSearchIndexer()
	searchable, err := gbfs.NewSearchableFileSystem(context.Background(), base, provider)
	if err != nil {
		t.Fatalf("NewSearchableFileSystem() error = %v", err)
	}
	return newCountingOpenFS(searchable), provider
}

func newCountedSearchCapableFS(t *testing.T, files map[string]string, provider gbfs.SearchProvider) *countingOpenFS {
	t.Helper()
	return newCountingOpenFS(grepSearchCapableFS{
		FileSystem: seededMemoryFS(t, files),
		provider:   provider,
	})
}

func seededMemoryFS(t *testing.T, files map[string]string) gbfs.FileSystem {
	t.Helper()

	mem := gbfs.NewMemory()
	for name, contents := range files {
		if err := mem.MkdirAll(context.Background(), path.Dir(name), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path.Dir(name), err)
		}
		file, err := mem.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			t.Fatalf("OpenFile(%q) error = %v", name, err)
		}
		if _, err := io.WriteString(file, contents); err != nil {
			_ = file.Close()
			t.Fatalf("WriteString(%q) error = %v", name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close(%q) error = %v", name, err)
		}
	}
	return mem
}

func lines(stdout string) []string {
	if stdout == "" {
		return nil
	}
	return splitLines(stdout)
}

func splitLines(stdout string) []string {
	out := make([]string, 0)
	start := 0
	for i := 0; i < len(stdout); i++ {
		if stdout[i] != '\n' {
			continue
		}
		out = append(out, stdout[start:i])
		start = i + 1
	}
	if start < len(stdout) {
		out = append(out, stdout[start:])
	}
	return out
}

type countingOpenFS struct {
	gbfs.FileSystem

	mu        sync.Mutex
	openCount map[string]int
}

func newCountingOpenFS(fsys gbfs.FileSystem) *countingOpenFS {
	return &countingOpenFS{
		FileSystem: fsys,
		openCount:  make(map[string]int),
	}
}

func (f *countingOpenFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	f.recordOpen(name)
	return f.FileSystem.Open(ctx, name)
}

func (f *countingOpenFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY {
		f.recordOpen(name)
	}
	return f.FileSystem.OpenFile(ctx, name, flag, perm)
}

func (f *countingOpenFS) recordOpen(name string) {
	f.mu.Lock()
	f.openCount[gbfs.Clean(name)]++
	f.mu.Unlock()
}

func (f *countingOpenFS) SearchProviderForPath(name string) (gbfs.SearchProvider, bool) {
	capable, ok := f.FileSystem.(gbfs.SearchCapable)
	if !ok {
		return nil, false
	}
	return capable.SearchProviderForPath(name)
}

func (f *countingOpenFS) OpenCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.openCount[gbfs.Clean(name)]
}

type countingSearchIndexer struct {
	inner gbfs.SearchIndexer

	mu      sync.Mutex
	queries []gbfs.SearchQuery
}

func newCountingSearchIndexer() *countingSearchIndexer {
	return &countingSearchIndexer{
		inner: gbfs.NewInMemorySearchProvider(),
	}
}

func (p *countingSearchIndexer) Search(ctx context.Context, query *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	p.mu.Lock()
	if query != nil {
		copied := *query
		copied.IncludeGlobs = append([]string(nil), query.IncludeGlobs...)
		copied.ExcludeGlobs = append([]string(nil), query.ExcludeGlobs...)
		p.queries = append(p.queries, copied)
	}
	p.mu.Unlock()
	return p.inner.Search(ctx, query)
}

func (p *countingSearchIndexer) SearchCapabilities() gbfs.SearchCapabilities {
	return p.inner.SearchCapabilities()
}

func (p *countingSearchIndexer) IndexStatus() gbfs.IndexStatus {
	return p.inner.IndexStatus()
}

func (p *countingSearchIndexer) ApplySearchMutation(ctx context.Context, mutation *gbfs.SearchMutation) error {
	return p.inner.ApplySearchMutation(ctx, mutation)
}

func (p *countingSearchIndexer) SearchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queries)
}

type grepSearchCapableFS struct {
	gbfs.FileSystem
	provider gbfs.SearchProvider
}

func (f grepSearchCapableFS) SearchProviderForPath(string) (gbfs.SearchProvider, bool) {
	return f.provider, true
}

type grepStaleProvider struct{}

func (grepStaleProvider) Search(context.Context, *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	return gbfs.SearchResult{
		Status: gbfs.IndexStatus{
			CurrentGeneration: 2,
			IndexedGeneration: 1,
			Backend:           "grep-stale",
		},
	}, nil
}

func (grepStaleProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (grepStaleProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 2,
		IndexedGeneration: 1,
		Backend:           "grep-stale",
	}
}

type grepUnsupportedProvider struct {
	mu          sync.Mutex
	searchCount int
}

func (p *grepUnsupportedProvider) Search(context.Context, *gbfs.SearchQuery) (gbfs.SearchResult, error) {
	p.mu.Lock()
	p.searchCount++
	p.mu.Unlock()
	return gbfs.SearchResult{}, gbfs.ErrSearchUnsupported
}

func (*grepUnsupportedProvider) SearchCapabilities() gbfs.SearchCapabilities {
	return gbfs.SearchCapabilities{
		LiteralSearch:   true,
		RootRestriction: true,
	}
}

func (*grepUnsupportedProvider) IndexStatus() gbfs.IndexStatus {
	return gbfs.IndexStatus{
		CurrentGeneration: 1,
		IndexedGeneration: 1,
		Backend:           "grep-unsupported",
	}
}

func (p *grepUnsupportedProvider) SearchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.searchCount
}
