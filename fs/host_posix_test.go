//go:build !windows

package fs

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestHostFSMountsHostTreeAtVirtualRootAndSynthesizesAncestors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fsys, err := NewHost(HostOptions{Root: root})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	if got, want := fsys.Getwd(), defaultHostVirtualRoot; got != want {
		t.Fatalf("Getwd() = %q, want %q", got, want)
	}

	assertDirEntries(t, fsys, "/", "home")
	assertDirEntries(t, fsys, "/home", "agent")
	assertDirEntries(t, fsys, "/home/agent", "project")
	assertDirEntries(t, fsys, defaultHostVirtualRoot, "docs")

	if err := fsys.Chdir("/home/agent"); err != nil {
		t.Fatalf("Chdir(/home/agent) error = %v", err)
	}
	if err := fsys.Chdir("project"); err != nil {
		t.Fatalf("Chdir(project) error = %v", err)
	}

	file, err := fsys.Open(context.Background(), "docs/guide.txt")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(data), "hello\n"; got != want {
		t.Fatalf("contents = %q, want %q", got, want)
	}
}

func TestHostFSReadOnlyAndSanitizesErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fsys, err := NewHost(HostOptions{Root: root})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	_, err = fsys.Open(context.Background(), defaultHostVirtualRoot+"/missing.txt")
	if err == nil {
		t.Fatal("Open(missing) error = nil, want not exist")
	}
	if !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("Open(missing) error = %v, want not exist", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("Open(missing) error leaked host root: %v", err)
	}

	_, err = fsys.OpenFile(context.Background(), defaultHostVirtualRoot+"/note.txt", os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		t.Fatal("OpenFile(write) error = nil, want permission")
	}
	if !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("OpenFile(write) error = %v, want permission", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("OpenFile(write) error leaked host root: %v", err)
	}
}

func TestHostFSDotDotPathsCannotEscapeMountedRoot(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(project) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}

	fsys, err := NewHost(HostOptions{Root: root})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	_, err = fsys.Open(context.Background(), defaultHostVirtualRoot+"/../secret.txt")
	if err == nil {
		t.Fatal("Open(../secret.txt) error = nil, want not exist")
	}
	if !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("Open(../secret.txt) error = %v, want not exist", err)
	}
	if strings.Contains(err.Error(), parent) {
		t.Fatalf("Open(../secret.txt) error leaked host parent: %v", err)
	}
}

func TestHostFSStatPreservesRawSysStat(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fsys, err := NewHost(HostOptions{Root: root})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	info, err := fsys.Stat(context.Background(), defaultHostVirtualRoot+"/note.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if _, ok := info.Sys().(*syscall.Stat_t); !ok {
		t.Fatalf("Stat().Sys() = %T, want *syscall.Stat_t", info.Sys())
	}
}

func TestHostFSReadCapRejectsLargeFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fsys, err := NewHost(HostOptions{
		Root:             root,
		MaxFileReadBytes: 4,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	_, err = fsys.Open(context.Background(), defaultHostVirtualRoot+"/big.txt")
	if err == nil {
		t.Fatal("Open(big.txt) error = nil, want file too large")
	}
	if !errors.Is(err, syscall.EFBIG) {
		t.Fatalf("Open(big.txt) error = %v, want EFBIG", err)
	}
	if !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("Open(big.txt) error = %v, want file too large message", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("Open(big.txt) error leaked host root: %v", err)
	}
}

func TestHostFSSymlinkResolutionAndReadlinkSanitization(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outsideRoot := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideRoot, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "rel-link.txt")); err != nil {
		t.Fatalf("Symlink(rel) error = %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "abs-in-link.txt")); err != nil {
		t.Fatalf("Symlink(abs-in) error = %v", err)
	}
	if err := os.Symlink(filepath.Join(outsideRoot, "secret.txt"), filepath.Join(root, "abs-out-link.txt")); err != nil {
		t.Fatalf("Symlink(abs-out) error = %v", err)
	}

	fsys, err := NewHost(HostOptions{Root: root})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	realpath, err := fsys.Realpath(context.Background(), defaultHostVirtualRoot+"/rel-link.txt")
	if err != nil {
		t.Fatalf("Realpath(rel-link) error = %v", err)
	}
	if got, want := realpath, defaultHostVirtualRoot+"/target.txt"; got != want {
		t.Fatalf("Realpath(rel-link) = %q, want %q", got, want)
	}

	target, err := fsys.Readlink(context.Background(), defaultHostVirtualRoot+"/rel-link.txt")
	if err != nil {
		t.Fatalf("Readlink(rel-link) error = %v", err)
	}
	if got, want := target, "target.txt"; got != want {
		t.Fatalf("Readlink(rel-link) = %q, want %q", got, want)
	}

	target, err = fsys.Readlink(context.Background(), defaultHostVirtualRoot+"/abs-in-link.txt")
	if err != nil {
		t.Fatalf("Readlink(abs-in-link) error = %v", err)
	}
	if got, want := target, defaultHostVirtualRoot+"/target.txt"; got != want {
		t.Fatalf("Readlink(abs-in-link) = %q, want %q", got, want)
	}

	info, err := fsys.Lstat(context.Background(), defaultHostVirtualRoot+"/abs-out-link.txt")
	if err != nil {
		t.Fatalf("Lstat(abs-out-link) error = %v", err)
	}
	if info.Mode()&stdfs.ModeSymlink == 0 {
		t.Fatalf("Lstat(abs-out-link).Mode() = %v, want symlink", info.Mode())
	}

	_, err = fsys.Readlink(context.Background(), defaultHostVirtualRoot+"/abs-out-link.txt")
	if err == nil {
		t.Fatal("Readlink(abs-out-link) error = nil, want permission")
	}
	if !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("Readlink(abs-out-link) error = %v, want permission", err)
	}
	if strings.Contains(err.Error(), outsideRoot) {
		t.Fatalf("Readlink(abs-out-link) error leaked outside root: %v", err)
	}

	_, err = fsys.Realpath(context.Background(), defaultHostVirtualRoot+"/abs-out-link.txt")
	if err == nil {
		t.Fatal("Realpath(abs-out-link) error = nil, want permission")
	}
	if !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("Realpath(abs-out-link) error = %v, want permission", err)
	}
}

func assertDirEntries(t *testing.T, fsys *HostFS, dir string, want ...string) {
	t.Helper()

	entries, err := fsys.ReadDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", dir, err)
	}

	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ReadDir(%q) = %v, want %v", dir, got, want)
	}
}
