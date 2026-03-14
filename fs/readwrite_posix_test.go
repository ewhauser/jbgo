//go:build !windows

package fs

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadWriteFSReadsWritesAndResolvesSymlinks(t *testing.T) {
	root := t.TempDir()

	fsys, err := NewReadWrite(ReadWriteOptions{Root: root})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}

	file, err := fsys.OpenFile(context.Background(), "/note.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := io.WriteString(file, "hello\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := fsys.MkdirAll(context.Background(), "/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.Rename(context.Background(), "/note.txt", "/sub/note.txt"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if err := fsys.Symlink(context.Background(), "/sub/note.txt", "/link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	entries, err := fsys.ReadDir(context.Background(), "/sub")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "note.txt" {
		t.Fatalf("ReadDir() = %#v, want single note.txt entry", entries)
	}

	target, err := fsys.Readlink(context.Background(), "/link.txt")
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if got, want := target, "/sub/note.txt"; got != want {
		t.Fatalf("Readlink() = %q, want %q", got, want)
	}

	resolved, err := fsys.Realpath(context.Background(), "/link.txt")
	if err != nil {
		t.Fatalf("Realpath() error = %v", err)
	}
	if got, want := resolved, "/sub/note.txt"; got != want {
		t.Fatalf("Realpath() = %q, want %q", got, want)
	}

	reader, err := fsys.Open(context.Background(), "/sub/note.txt")
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

	hostData, err := os.ReadFile(filepath.Join(root, "sub", "note.txt"))
	if err != nil {
		t.Fatalf("ReadFile(host) error = %v", err)
	}
	if got, want := string(hostData), "hello\n"; got != want {
		t.Fatalf("host contents = %q, want %q", got, want)
	}
}

func TestReadWriteFSStatPreservesRawSysStat(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fsys, err := NewReadWrite(ReadWriteOptions{Root: root})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}

	info, err := fsys.Stat(context.Background(), "/note.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if _, ok := info.Sys().(*syscall.Stat_t); !ok {
		t.Fatalf("Stat().Sys() = %T, want *syscall.Stat_t", info.Sys())
	}
}

func TestReadWriteFSReadCapRejectsLargeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fsys, err := NewReadWrite(ReadWriteOptions{
		Root:             root,
		MaxFileReadBytes: 4,
	})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}

	_, err = fsys.Open(context.Background(), "/big.txt")
	if err == nil {
		t.Fatal("Open(big.txt) error = nil, want file too large")
	}
	if !errors.Is(err, syscall.EFBIG) {
		t.Fatalf("Open(big.txt) error = %v, want EFBIG", err)
	}
	if !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("Open(big.txt) error = %v, want file too large message", err)
	}
}

func TestReadWriteFSDeniesEscapeViaSymlink(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()

	if err := os.WriteFile(filepath.Join(outsideRoot, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	if err := os.Symlink(filepath.Join(outsideRoot, "secret.txt"), filepath.Join(root, "abs-out-link.txt")); err != nil {
		t.Fatalf("Symlink(abs-out) error = %v", err)
	}

	fsys, err := NewReadWrite(ReadWriteOptions{Root: root})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}

	_, err = fsys.Open(context.Background(), "/abs-out-link.txt")
	if err == nil {
		t.Fatal("Open(abs-out-link) error = nil, want permission")
	}
	if !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("Open(abs-out-link) error = %v, want permission", err)
	}

	_, err = fsys.Realpath(context.Background(), "/abs-out-link.txt")
	if err == nil {
		t.Fatal("Realpath(abs-out-link) error = nil, want permission")
	}
	if !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("Realpath(abs-out-link) error = %v, want permission", err)
	}

	_, err = fsys.Readlink(context.Background(), "/abs-out-link.txt")
	if err == nil {
		t.Fatal("Readlink(abs-out-link) error = nil, want permission")
	}
	if !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("Readlink(abs-out-link) error = %v, want permission", err)
	}
}

func TestReadWriteFSChdirPreservesLogicalCWD(t *testing.T) {
	root := t.TempDir()
	physicalDir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(physicalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	logicalDir := filepath.Join(root, "c")
	if err := os.Symlink(physicalDir, logicalDir); err != nil {
		t.Skipf("Symlink() error = %v", err)
	}

	fsys, err := NewReadWrite(ReadWriteOptions{Root: root})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}

	if err := fsys.Chdir("/c"); err != nil {
		t.Fatalf("Chdir(/c) error = %v", err)
	}
	if got, want := fsys.Getwd(), "/c"; got != want {
		t.Fatalf("Getwd() = %q, want %q", got, want)
	}

	realpath, err := fsys.Realpath(context.Background(), ".")
	if err != nil {
		t.Fatalf("Realpath(.) error = %v", err)
	}
	if got, want := realpath, "/a/b"; got != want {
		t.Fatalf("Realpath(.) = %q, want %q", got, want)
	}
}

func TestReadWriteFSChdirAllowsCurrentLongPathAtHostRoot(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previous)
	})

	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir(root) error = %v", err)
	}

	segment := strings.Repeat("z", 31)
	for depth := range 256 {
		if err := os.Mkdir(segment, 0o755); err != nil {
			t.Fatalf("Mkdir(depth=%d) error = %v", depth, err)
		}
		if err := os.Chdir(segment); err != nil {
			t.Fatalf("Chdir(depth=%d) error = %v", depth, err)
		}
	}

	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(current) error = %v", err)
	}

	fsys, err := NewReadWrite(ReadWriteOptions{Root: "/"})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}
	if err := fsys.Chdir(filepath.ToSlash(current)); err != nil {
		t.Fatalf("Chdir(current long path) error = %v", err)
	}
	if got, want := fsys.Getwd(), filepath.ToSlash(current); got != want {
		t.Fatalf("Getwd() = %q, want %q", got, want)
	}

	realpath, err := fsys.Realpath(context.Background(), ".")
	if err != nil {
		t.Fatalf("Realpath(.) error = %v", err)
	}
	if got, want := realpath, filepath.ToSlash(current); got != want {
		t.Fatalf("Realpath(.) = %q, want %q", got, want)
	}
}
