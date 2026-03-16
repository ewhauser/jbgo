package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/examples/internal/blevefs"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/searchadapter"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdout io.Writer) error {
	rt, err := gbash.New(gbash.WithFileSystem(gbash.CustomFileSystem(
		blevefs.NewFactory(gbfs.Memory()),
		"/workspace",
	)))
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	fsys := session.FileSystem()
	if err := writeFile(ctx, fsys, "/workspace/docs/readme.txt", "guide needle\n"); err != nil {
		return err
	}
	if err := writeFile(ctx, fsys, "/workspace/logs/app.log", "log needle\n"); err != nil {
		return err
	}
	if err := writeFile(ctx, fsys, "/workspace/notes/todo.txt", "nothing here\n"); err != nil {
		return err
	}

	capable, ok := fsys.(gbfs.SearchCapable)
	if !ok {
		return fmt.Errorf("filesystem %T does not implement SearchCapable", fsys)
	}
	provider, ok := capable.SearchProviderForPath("/workspace")
	if !ok {
		return fmt.Errorf("search provider unavailable for /workspace")
	}

	direct, err := provider.Search(ctx, &gbfs.SearchQuery{
		Root:        "/workspace",
		Literal:     "needle",
		WantOffsets: true,
	})
	if err != nil {
		return fmt.Errorf("direct search: %w", err)
	}

	if _, err := fmt.Fprintf(stdout, "direct search (backend=%s):\n", direct.Status.Backend); err != nil {
		return err
	}
	for _, hit := range direct.Hits {
		if _, err := fmt.Fprintf(stdout, "  %s offsets=%v\n", hit.Path, hit.Offsets); err != nil {
			return err
		}
	}

	adapted, err := searchadapter.Search(ctx, fsys, &searchadapter.Query{
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
		return fmt.Errorf("adapter search: %w", err)
	}

	if _, err := fmt.Fprintf(stdout, "adapter used index: %v\n", adapted.UsedIndex); err != nil {
		return err
	}
	for _, hit := range adapted.Hits {
		if _, err := fmt.Fprintf(stdout, "  %s\n", hit.Path); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(ctx context.Context, fsys gbfs.FileSystem, name, contents string) error {
	dir := path.Dir(name)
	if err := fsys.MkdirAll(ctx, dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	file, err := fsys.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	if _, err := io.WriteString(file, contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	return nil
}

func readFile(ctx context.Context, fsys gbfs.FileSystem, name string) ([]byte, error) {
	file, err := fsys.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}
