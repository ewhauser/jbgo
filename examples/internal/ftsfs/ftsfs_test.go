package ftsfs

import (
	"bytes"
	"context"
	"io"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/searchadapter"
)

func TestNewFactoryProvidesIndexedSearch(t *testing.T) {
	t.Parallel()
	factory := NewFactory(gbfs.SeededMemory(gbfs.InitialFiles{
		"/workspace/docs/readme.txt": {Content: []byte("guide needle\n")},
		"/workspace/logs/app.log":    {Content: []byte("log needle\n")},
		"/workspace/notes/todo.txt":  {Content: []byte("nothing here\n")},
	}))

	fsys, err := factory.New(context.Background())
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}
	capable, ok := fsys.(gbfs.SearchCapable)
	if !ok {
		t.Fatalf("filesystem %T does not implement SearchCapable", fsys)
	}
	provider, ok := capable.SearchProviderForPath("/workspace")
	if !ok {
		t.Fatal("SearchProviderForPath(/workspace) = false, want true")
	}
	result, err := provider.Search(context.Background(), &gbfs.SearchQuery{
		Root:        "/workspace",
		Literal:     "needle",
		WantOffsets: true,
	})
	if err != nil {
		t.Fatalf("provider.Search() error = %v", err)
	}
	if len(result.Hits) != 2 {
		t.Fatalf("provider.Search() hit count = %d, want 2", len(result.Hits))
	}

	adapted, err := searchadapter.Search(context.Background(), fsys, &searchadapter.Query{
		Roots:         []string{"/workspace"},
		Literal:       "needle",
		IndexEligible: true,
	}, func(ctx context.Context, hit gbfs.SearchHit) (bool, error) {
		data, err := readFile(ctx, fsys, hit.Path)
		if err != nil {
			return false, err
		}
		return bytes.Contains(data, []byte("needle")), nil
	})
	if err != nil {
		t.Fatalf("searchadapter.Search() error = %v", err)
	}
	if !adapted.UsedIndex {
		t.Fatal("searchadapter.Search() UsedIndex = false, want true")
	}
	if len(adapted.Hits) != 2 {
		t.Fatalf("searchadapter.Search() hit count = %d, want 2", len(adapted.Hits))
	}
}

func readFile(ctx context.Context, fsys gbfs.FileSystem, name string) ([]byte, error) {
	file, err := fsys.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}
