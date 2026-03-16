package blevefs

import (
	"context"
	"io"
	"os"
	"path"
	"slices"
	"sort"
	"testing"

	"github.com/blevesearch/bleve/v2"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/searchadapter"
)

func TestBleveProviderSearchQueries(t *testing.T) {
	fsys := newBleveSearchableFS(t, gbfs.InitialFiles{
		"/workspace/docs/readme.txt": {Content: []byte("alpha needle omega\n")},
		"/workspace/src/main.go":     {Content: []byte("package main\nconst greeting = \"Hello\"\n")},
		"/workspace/src/util.go":     {Content: []byte("package main\nconst secondary = \"hello\"\n")},
		"/workspace/tmp/ignore.txt":  {Content: []byte("needle hidden\n")},
		"/workspace/logs/app.log":    {Content: []byte("abc needle def needle\n")},
	})
	provider := mustBleveProvider(t, fsys)

	caps := provider.SearchCapabilities()
	if !caps.LiteralSearch || !caps.IgnoreCaseLiteralSearch || !caps.RootRestriction || !caps.IncludeGlobs || !caps.ExcludeGlobs || !caps.Offsets || !caps.VerifiedResults {
		t.Fatalf("SearchCapabilities() = %+v, want literal/ignore-case/root/globs/offsets/verified support", caps)
	}
	if caps.ApproximateResults {
		t.Fatalf("SearchCapabilities().ApproximateResults = true, want false")
	}

	tests := []struct {
		name          string
		query         gbfs.SearchQuery
		wantPaths     []string
		wantOffsets   map[string][]int64
		wantTruncated bool
	}{
		{
			name: "basic-literal",
			query: gbfs.SearchQuery{
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
			query: gbfs.SearchQuery{
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
			query: gbfs.SearchQuery{
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
			name: "offsets",
			query: gbfs.SearchQuery{
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
			query: gbfs.SearchQuery{
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
			if got, want := result.Status.Backend, "bleve"; got != want {
				t.Fatalf("Status.Backend = %q, want %q", got, want)
			}

			gotPaths := make([]string, 0, len(result.Hits))
			for _, hit := range result.Hits {
				gotPaths = append(gotPaths, hit.Path)
				if !hit.Verified {
					t.Fatalf("Hit(%s).Verified = false, want true", hit.Path)
				}
				if hit.Approximate {
					t.Fatalf("Hit(%s).Approximate = true, want false", hit.Path)
				}
				if want := tc.wantOffsets[hit.Path]; tc.wantOffsets != nil && !slices.Equal(hit.Offsets, want) {
					t.Fatalf("Offsets(%s) = %v, want %v", hit.Path, hit.Offsets, want)
				}
			}
			if !slices.Equal(gotPaths, tc.wantPaths) {
				t.Fatalf("Paths = %v, want %v", gotPaths, tc.wantPaths)
			}
			if result.Truncated != tc.wantTruncated {
				t.Fatalf("Truncated = %v, want %v", result.Truncated, tc.wantTruncated)
			}
		})
	}
}

func TestBleveProviderMutationsStayInSync(t *testing.T) {
	fsys := newBleveSearchableFS(t, nil)
	provider := mustBleveProvider(t, fsys)

	writeFileForTest(t, fsys, "/docs/file.txt", "hello needle\n")
	assertSearchPaths(t, provider, &gbfs.SearchQuery{Root: "/docs", Literal: "needle"}, []string{"/docs/file.txt"})

	if err := fsys.Rename(context.Background(), "/docs/file.txt", "/docs/renamed.txt"); err != nil {
		t.Fatalf("Rename(file) error = %v", err)
	}
	assertSearchPaths(t, provider, &gbfs.SearchQuery{Root: "/docs", Literal: "needle"}, []string{"/docs/renamed.txt"})

	writeFileForTest(t, fsys, "/tree/sub/child.txt", "needle child\n")
	if err := fsys.Rename(context.Background(), "/tree", "/moved"); err != nil {
		t.Fatalf("Rename(dir) error = %v", err)
	}
	assertSearchPaths(t, provider, &gbfs.SearchQuery{Root: "/moved", Literal: "needle"}, []string{"/moved/sub/child.txt"})

	if err := fsys.Remove(context.Background(), "/docs/renamed.txt", false); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	assertSearchPaths(t, provider, &gbfs.SearchQuery{Root: "/docs", Literal: "needle"}, nil)

	status := provider.IndexStatus()
	if status.CurrentGeneration == 0 || status.CurrentGeneration != status.IndexedGeneration {
		t.Fatalf("IndexStatus() = %+v, want synchronized non-zero generations", status)
	}
}

func TestBleveProviderSearchAdapterUsesIndex(t *testing.T) {
	fsys := newBleveSearchableFS(t, gbfs.InitialFiles{
		"/workspace/docs/readme.txt": {Content: []byte("guide needle\n")},
		"/workspace/logs/app.log":    {Content: []byte("log needle\n")},
		"/workspace/notes/todo.txt":  {Content: []byte("nothing here\n")},
	})

	result, err := searchadapter.Search(context.Background(), fsys, &searchadapter.Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("searchadapter.Search() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("searchadapter.Search() UsedIndex = false, want true")
	}
	assertHitPaths(t, result.Hits, []string{
		"/workspace/docs/readme.txt",
		"/workspace/logs/app.log",
	})
}

func TestPreparedBleveArtifactFactoryUsesIndex(t *testing.T) {
	files := gbfs.InitialFiles{
		"/workspace/docs/readme.txt": {Content: []byte("guide needle\n")},
		"/workspace/logs/app.log":    {Content: []byte("log needle needle\n")},
		"/workspace/notes/todo.txt":  {Content: []byte("nothing here\n")},
	}

	artifactDir := path.Join(t.TempDir(), "bleve-index")
	if err := buildTestBleveArtifact(context.Background(), artifactDir, files); err != nil {
		t.Fatalf("buildTestBleveArtifact() error = %v", err)
	}

	factory := NewPreparedFactory(gbfs.SeededMemory(files), artifactDir, len(files))
	fsys, err := factory.New(context.Background())
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}
	defer func() {
		type closer interface{ Close() error }
		if c, ok := fsys.(closer); ok {
			if err := c.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		}
	}()

	result, err := searchadapter.Search(context.Background(), fsys, &searchadapter.Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("searchadapter.Search() error = %v", err)
	}
	if !result.UsedIndex {
		t.Fatal("searchadapter.Search() UsedIndex = false, want true")
	}
	assertHitPaths(t, result.Hits, []string{
		"/workspace/docs/readme.txt",
		"/workspace/logs/app.log",
	})

	provider := mustBleveProvider(t, fsys)
	searchResult, err := provider.Search(context.Background(), &gbfs.SearchQuery{
		Root:        "/workspace/logs",
		Literal:     "needle",
		WantOffsets: true,
	})
	if err != nil {
		t.Fatalf("provider.Search() error = %v", err)
	}
	if got, want := searchResult.Status.Backend, "bleve"; got != want {
		t.Fatalf("Status.Backend = %q, want %q", got, want)
	}
	if len(searchResult.Hits) != 1 {
		t.Fatalf("hit count = %d, want 1", len(searchResult.Hits))
	}
	if !slices.Equal(searchResult.Hits[0].Offsets, []int64{4, 11}) {
		t.Fatalf("Offsets = %v, want [4 11]", searchResult.Hits[0].Offsets)
	}
}

func buildTestBleveArtifact(ctx context.Context, artifactDir string, files gbfs.InitialFiles) error {
	index, err := bleve.New(artifactDir, NewIndexMapping())
	if err != nil {
		return err
	}
	defer func() { _ = index.Close() }()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, gbfs.Clean(name))
	}
	sort.Strings(names)

	for _, name := range names {
		initial := files[name]
		data := initial.Content
		if initial.Lazy != nil {
			data, err = initial.Lazy(ctx)
			if err != nil {
				return err
			}
		}
		if err := index.Index(name, NewDocument(name, data)); err != nil {
			return err
		}
	}
	return nil
}

func newBleveSearchableFS(t *testing.T, files gbfs.InitialFiles) gbfs.FileSystem {
	t.Helper()

	factory := NewFactory(gbfs.SeededMemory(files))
	fsys, err := factory.New(context.Background())
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}
	return fsys
}

func mustBleveProvider(t *testing.T, fsys gbfs.FileSystem) gbfs.SearchProvider {
	t.Helper()

	capable, ok := fsys.(gbfs.SearchCapable)
	if !ok {
		t.Fatalf("filesystem %T does not implement SearchCapable", fsys)
	}
	provider, ok := capable.SearchProviderForPath("/")
	if !ok {
		t.Fatal("SearchProviderForPath(/) = false, want true")
	}
	return provider
}

func writeFileForTest(t *testing.T, fsys gbfs.FileSystem, name, contents string) {
	t.Helper()
	dir := path.Dir(name)
	if err := fsys.MkdirAll(context.Background(), dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	file, err := fsys.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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

func assertSearchPaths(t *testing.T, provider gbfs.SearchProvider, query *gbfs.SearchQuery, want []string) {
	t.Helper()
	result, err := provider.Search(context.Background(), query)
	if err != nil {
		t.Fatalf("Search(%+v) error = %v", query, err)
	}
	assertHitPaths(t, result.Hits, want)
}

func assertHitPaths(t *testing.T, hits []gbfs.SearchHit, want []string) {
	t.Helper()
	got := make([]string, 0, len(hits))
	for _, hit := range hits {
		got = append(got, hit.Path)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
}
