package fs

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestMemoryFSPathIntrospection(t *testing.T) {
	mem := NewMemory()
	writeTestFile(t, mem, "/data/file.txt", "hello\n")

	statInfo, err := mem.Stat(context.Background(), "/data/file.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	lstatInfo, err := mem.Lstat(context.Background(), "/data/file.txt")
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}
	if statInfo.Mode() != lstatInfo.Mode() {
		t.Fatalf("Lstat().Mode() = %v, want %v", lstatInfo.Mode(), statInfo.Mode())
	}

	realpath, err := mem.Realpath(context.Background(), "/data/../data/file.txt")
	if err != nil {
		t.Fatalf("Realpath() error = %v", err)
	}
	if got, want := realpath, "/data/file.txt"; got != want {
		t.Fatalf("Realpath() = %q, want %q", got, want)
	}
}

func TestMemoryFSSymlinkIntrospectionAndTraversal(t *testing.T) {
	mem := NewMemory()
	writeTestFile(t, mem, "/safe/target.txt", "hello\n")
	if err := mem.Symlink(context.Background(), "target.txt", "/safe/link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	lstatInfo, err := mem.Lstat(context.Background(), "/safe/link.txt")
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}
	if lstatInfo.Mode()&stdfs.ModeSymlink == 0 {
		t.Fatalf("Lstat().Mode() = %v, want symlink", lstatInfo.Mode())
	}

	target, err := mem.Readlink(context.Background(), "/safe/link.txt")
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if got, want := target, "target.txt"; got != want {
		t.Fatalf("Readlink() = %q, want %q", got, want)
	}

	realpath, err := mem.Realpath(context.Background(), "/safe/link.txt")
	if err != nil {
		t.Fatalf("Realpath() error = %v", err)
	}
	if got, want := realpath, "/safe/target.txt"; got != want {
		t.Fatalf("Realpath() = %q, want %q", got, want)
	}

	if got, want := readTestFile(t, mem, "/safe/link.txt"), "hello\n"; got != want {
		t.Fatalf("Open(link) = %q, want %q", got, want)
	}
}

func TestMemoryFSSymlinkLoopFails(t *testing.T) {
	mem := NewMemory()
	if err := mem.Symlink(context.Background(), "b", "/a"); err != nil {
		t.Fatalf("Symlink(a) error = %v", err)
	}
	if err := mem.Symlink(context.Background(), "a", "/b"); err != nil {
		t.Fatalf("Symlink(b) error = %v", err)
	}

	_, err := mem.Realpath(context.Background(), "/a")
	if err == nil {
		t.Fatal("Realpath() error = nil, want symlink loop error")
	}
	if !strings.Contains(err.Error(), "too many levels of symbolic links") {
		t.Fatalf("Realpath() error = %v, want symlink loop message", err)
	}
}

func TestMemoryFSReadlinkRejectsNonSymlink(t *testing.T) {
	mem := NewMemory()
	writeTestFile(t, mem, "/data/file.txt", "hello\n")

	_, err := mem.Readlink(context.Background(), "/data/file.txt")
	if err == nil {
		t.Fatal("Readlink() error = nil, want invalid")
	}
	if !errors.Is(err, stdfs.ErrInvalid) {
		t.Fatalf("Readlink() error = %v, want invalid", err)
	}
}

func TestMemoryFSRenameRejectsRoot(t *testing.T) {
	mem := NewMemory()

	err := mem.Rename(context.Background(), "/", "/tmp/root")
	if err == nil {
		t.Fatal("Rename(/) error = nil, want permission")
	}
	if !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("Rename(/) error = %v, want permission", err)
	}

	info, err := mem.Stat(context.Background(), "/")
	if err != nil {
		t.Fatalf("Stat(/) error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat(/).IsDir() = false, want true")
	}
}

func TestOverlayFSReadsFromLowerAndWritesToUpper(t *testing.T) {
	lower := seededMemory(t, map[string]string{
		"/base.txt":       "base\n",
		"/shared/old.txt": "old\n",
	})
	overlay := NewOverlay(lower)

	if got, want := readTestFile(t, overlay, "/base.txt"), "base\n"; got != want {
		t.Fatalf("overlay read = %q, want %q", got, want)
	}

	file, err := overlay.OpenFile(context.Background(), "/base.txt", os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := io.WriteString(file, "upper\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got, want := readTestFile(t, overlay, "/base.txt"), "upper\n"; got != want {
		t.Fatalf("overlay read after write = %q, want %q", got, want)
	}
	if got, want := readTestFile(t, lower, "/base.txt"), "base\n"; got != want {
		t.Fatalf("lower read after overlay write = %q, want %q", got, want)
	}

	writeTestFile(t, overlay, "/shared/new.txt", "new\n")
	if _, err := lower.Stat(context.Background(), "/shared/new.txt"); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("lower Stat(new.txt) error = %v, want not exist", err)
	}
}

func TestOverlayFSReadDirMergesAndHidesDeletedEntries(t *testing.T) {
	lower := seededMemory(t, map[string]string{
		"/dir/lower.txt": "lower\n",
		"/dir/keep.txt":  "keep\n",
	})
	overlay := NewOverlay(lower)
	writeTestFile(t, overlay, "/dir/upper.txt", "upper\n")

	if err := overlay.Remove(context.Background(), "/dir/lower.txt", false); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	entries, err := overlay.ReadDir(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if got, want := names, []string{"keep.txt", "upper.txt"}; !slices.Equal(got, want) {
		t.Fatalf("ReadDir() names = %v, want %v", got, want)
	}

	if got, want := readTestFile(t, lower, "/dir/lower.txt"), "lower\n"; got != want {
		t.Fatalf("lower file content = %q, want %q", got, want)
	}
}

func TestOverlayFSRenameCopiesUpAndTombstonesSource(t *testing.T) {
	lower := seededMemory(t, map[string]string{
		"/dir/file.txt": "move-me\n",
	})
	overlay := NewOverlay(lower)

	if err := overlay.Rename(context.Background(), "/dir/file.txt", "/dir/moved.txt"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	if got, want := readTestFile(t, overlay, "/dir/moved.txt"), "move-me\n"; got != want {
		t.Fatalf("overlay moved content = %q, want %q", got, want)
	}
	if _, err := overlay.Stat(context.Background(), "/dir/file.txt"); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("overlay Stat(old) error = %v, want not exist", err)
	}
	if got, want := readTestFile(t, lower, "/dir/file.txt"), "move-me\n"; got != want {
		t.Fatalf("lower original content = %q, want %q", got, want)
	}
}

func TestOverlayFSRealpathResolvesLowerSymlinks(t *testing.T) {
	lower := seededMemory(t, map[string]string{
		"/safe/target.txt": "hello\n",
	})
	if err := lower.Symlink(context.Background(), "target.txt", "/safe/link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	overlay := NewOverlay(lower)
	realpath, err := overlay.Realpath(context.Background(), "/safe/link.txt")
	if err != nil {
		t.Fatalf("Realpath() error = %v", err)
	}
	if got, want := realpath, "/safe/target.txt"; got != want {
		t.Fatalf("Realpath() = %q, want %q", got, want)
	}
}

func TestSnapshotFSPreservesSourceViewAndRejectsWrites(t *testing.T) {
	source := seededMemory(t, map[string]string{
		"/data.txt": "before\n",
	})
	snapshot, err := NewSnapshot(context.Background(), source)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	writeTestFile(t, source, "/data.txt", "after\n")
	if got, want := readTestFile(t, snapshot, "/data.txt"), "before\n"; got != want {
		t.Fatalf("snapshot content = %q, want %q", got, want)
	}

	if err := snapshot.MkdirAll(context.Background(), "/newdir", 0o755); !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("MkdirAll() error = %v, want permission", err)
	}
	if _, err := snapshot.OpenFile(context.Background(), "/data.txt", os.O_WRONLY|os.O_TRUNC, 0o644); !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("OpenFile(write) error = %v, want permission", err)
	}
	if realpath, err := snapshot.Realpath(context.Background(), "/data.txt"); err != nil || realpath != "/data.txt" {
		t.Fatalf("Realpath() = %q, %v; want /data.txt, nil", realpath, err)
	}
}

func writeTestFile(t *testing.T, fsys FileSystem, name, contents string) {
	t.Helper()

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

func readTestFile(t *testing.T, fsys FileSystem, name string) string {
	t.Helper()

	file, err := fsys.Open(context.Background(), name)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", name, err)
	}
	return string(data)
}

func seededMemory(t *testing.T, files map[string]string) *MemoryFS {
	t.Helper()

	mem := NewMemory()
	for name, contents := range files {
		writeTestFile(t, mem, name, contents)
	}
	return mem
}
