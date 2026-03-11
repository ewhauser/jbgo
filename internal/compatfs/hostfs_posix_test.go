//go:build !windows

package compatfs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestHostFSReadsWritesAndResolvesSymlinks(t *testing.T) {
	t.Chdir(t.TempDir())

	fsys, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	file, err := fsys.OpenFile(context.Background(), "note.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := io.WriteString(file, "hello\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := fsys.MkdirAll(context.Background(), "sub", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.Rename(context.Background(), "note.txt", "sub/note.txt"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if err := fsys.Symlink(context.Background(), "sub/note.txt", "link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	entries, err := fsys.ReadDir(context.Background(), "sub")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "note.txt" {
		t.Fatalf("ReadDir() = %#v, want single note.txt entry", entries)
	}

	target, err := fsys.Readlink(context.Background(), "link.txt")
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if got, want := target, "sub/note.txt"; got != want {
		t.Fatalf("Readlink() = %q, want %q", got, want)
	}

	resolved, err := fsys.Realpath(context.Background(), "link.txt")
	if err != nil {
		t.Fatalf("Realpath() error = %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(tmpDirFromFS(t, fsys))
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	wantResolved := filepath.ToSlash(filepath.Join(canonicalRoot, "sub", "note.txt"))
	if resolved != wantResolved {
		t.Fatalf("Realpath() = %q, want %q", resolved, wantResolved)
	}

	reader, err := fsys.Open(context.Background(), "sub/note.txt")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close(reader) error = %v", err)
	}
	if got, want := string(data), "hello\n"; got != want {
		t.Fatalf("contents = %q, want %q", got, want)
	}
}

func tmpDirFromFS(t *testing.T, fsys *HostFS) string {
	t.Helper()
	return filepath.FromSlash(fsys.Getwd())
}
