package agentfsfs

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gbruntime "github.com/ewhauser/gbash"
	gbfs "github.com/ewhauser/gbash/fs"
)

func TestAgentFSFileLifecycle(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	writeFSFile(t, fsys, "/data/file.txt", "alpha\n")

	file, err := fsys.OpenFile(context.Background(), "/data/file.txt", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("OpenFile(append) error = %v", err)
	}
	if _, err := io.WriteString(file, "beta\n"); err != nil {
		t.Fatalf("WriteString(append) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(append) error = %v", err)
	}

	if got, want := readFSFile(t, fsys, "/data/file.txt"), "alpha\nbeta\n"; got != want {
		t.Fatalf("read after append = %q, want %q", got, want)
	}

	file, err = fsys.OpenFile(context.Background(), "/data/file.txt", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile(trunc) error = %v", err)
	}
	if _, err := io.WriteString(file, "reset\n"); err != nil {
		t.Fatalf("WriteString(trunc) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(trunc) error = %v", err)
	}

	if got, want := readFSFile(t, fsys, "/data/file.txt"), "reset\n"; got != want {
		t.Fatalf("read after trunc = %q, want %q", got, want)
	}

	info, err := fsys.Stat(context.Background(), "/data/file.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got, want := info.Size(), int64(len("reset\n")); got != want {
		t.Fatalf("Stat().Size() = %d, want %d", got, want)
	}

	if _, err := fsys.OpenFile(context.Background(), "/data/file.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); !errors.Is(err, stdfs.ErrExist) {
		t.Fatalf("OpenFile(create excl) error = %v, want exist", err)
	}
}

func TestAgentFSReadDirAndRename(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	writeFSFile(t, fsys, "/dir/b.txt", "b\n")
	writeFSFile(t, fsys, "/dir/a.txt", "a\n")

	entries, err := fsys.ReadDir(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if got, want := names, []string{"a.txt", "b.txt"}; !slices.Equal(got, want) {
		t.Fatalf("ReadDir() names = %v, want %v", got, want)
	}

	if err := fsys.Rename(context.Background(), "/dir/a.txt", "/dir/c.txt"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if got, want := readFSFile(t, fsys, "/dir/c.txt"), "a\n"; got != want {
		t.Fatalf("read moved file = %q, want %q", got, want)
	}
	if _, err := fsys.Stat(context.Background(), "/dir/a.txt"); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("Stat(old path) error = %v, want not exist", err)
	}
}

func TestAgentFSRemoveRecursiveKeepsExternalHardLink(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	writeFSFile(t, fsys, "/tree/file.txt", "shared\n")
	if err := fsys.Link(context.Background(), "/tree/file.txt", "/keep.txt"); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	if err := fsys.Remove(context.Background(), "/tree", false); !errors.Is(err, stdfs.ErrInvalid) {
		t.Fatalf("Remove(non-recursive) error = %v, want invalid", err)
	}
	if err := fsys.Remove(context.Background(), "/tree", true); err != nil {
		t.Fatalf("Remove(recursive) error = %v", err)
	}

	if got, want := readFSFile(t, fsys, "/keep.txt"), "shared\n"; got != want {
		t.Fatalf("read surviving hardlink = %q, want %q", got, want)
	}
	if _, err := fsys.Stat(context.Background(), "/tree"); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("Stat(tree) error = %v, want not exist", err)
	}
}

func TestAgentFSSymlinkIntrospectionAndTraversal(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	writeFSFile(t, fsys, "/safe/target.txt", "hello\n")
	if err := fsys.Symlink(context.Background(), "target.txt", "/safe/link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	info, err := fsys.Lstat(context.Background(), "/safe/link.txt")
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}
	if info.Mode()&stdfs.ModeSymlink == 0 {
		t.Fatalf("Lstat().Mode() = %v, want symlink", info.Mode())
	}

	target, err := fsys.Readlink(context.Background(), "/safe/link.txt")
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if got, want := target, "target.txt"; got != want {
		t.Fatalf("Readlink() = %q, want %q", got, want)
	}

	realpath, err := fsys.Realpath(context.Background(), "/safe/link.txt")
	if err != nil {
		t.Fatalf("Realpath() error = %v", err)
	}
	if got, want := realpath, "/safe/target.txt"; got != want {
		t.Fatalf("Realpath() = %q, want %q", got, want)
	}

	if got, want := readFSFile(t, fsys, "/safe/link.txt"), "hello\n"; got != want {
		t.Fatalf("Open(link) = %q, want %q", got, want)
	}
}

func TestAgentFSSymlinkLoopFails(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	if err := fsys.Symlink(context.Background(), "b", "/a"); err != nil {
		t.Fatalf("Symlink(a) error = %v", err)
	}
	if err := fsys.Symlink(context.Background(), "a", "/b"); err != nil {
		t.Fatalf("Symlink(b) error = %v", err)
	}

	_, err := fsys.Realpath(context.Background(), "/a")
	if err == nil {
		t.Fatal("Realpath() error = nil, want symlink loop")
	}
	if !strings.Contains(err.Error(), "too many levels of symbolic links") {
		t.Fatalf("Realpath() error = %v, want symlink loop message", err)
	}
}

func TestAgentFSHardLinksShareContentAndRejectDirectories(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	writeFSFile(t, fsys, "/docs/original.txt", "draft\n")
	if err := fsys.Link(context.Background(), "/docs/original.txt", "/docs/copy.txt"); err != nil {
		t.Fatalf("Link(file) error = %v", err)
	}

	file, err := fsys.OpenFile(context.Background(), "/docs/copy.txt", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile(copy) error = %v", err)
	}
	if _, err := io.WriteString(file, "published\n"); err != nil {
		t.Fatalf("WriteString(copy) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(copy) error = %v", err)
	}

	if got, want := readFSFile(t, fsys, "/docs/original.txt"), "published\n"; got != want {
		t.Fatalf("read original = %q, want %q", got, want)
	}

	if err := fsys.MkdirAll(context.Background(), "/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.Link(context.Background(), "/dir", "/dir-link"); !errors.Is(err, stdfs.ErrInvalid) {
		t.Fatalf("Link(directory) error = %v, want invalid", err)
	}
}

func TestAgentFSMetadataAndCwd(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	if err := fsys.MkdirAll(context.Background(), "/workspace", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.Chdir("/workspace"); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	if got, want := fsys.Getwd(), "/workspace"; got != want {
		t.Fatalf("Getwd() = %q, want %q", got, want)
	}

	writeFSFile(t, fsys, "note.txt", "hello\n")
	if got, want := readFSFile(t, fsys, "/workspace/note.txt"), "hello\n"; got != want {
		t.Fatalf("relative write = %q, want %q", got, want)
	}

	if err := fsys.Chmod(context.Background(), "note.txt", 0o600); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	when := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := fsys.Chtimes(context.Background(), "note.txt", when, when); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	info, err := fsys.Stat(context.Background(), "note.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got, want := info.Mode().Perm(), stdfs.FileMode(0o600); got != want {
		t.Fatalf("Mode().Perm() = %v, want %v", got, want)
	}
	if got, want := info.ModTime().UnixNano(), when.UnixNano(); got != want {
		t.Fatalf("ModTime() = %d, want %d", got, want)
	}
}

func TestAgentFSFIFOWriterWaitsForReader(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	if err := fsys.MkdirAll(context.Background(), "/tmp", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.Mkfifo(context.Background(), "/tmp/p", 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}

	writer, err := fsys.OpenFile(context.Background(), "/tmp/p", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(writer) error = %v", err)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, "hello")
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		t.Fatalf("writer finished before reader attached: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	reader, err := fsys.OpenFile(context.Background(), "/tmp/p", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(reader) error = %v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("reader.Close() error = %v", err)
		}
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("writer error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not unblock after reader attached")
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(data), "hello"; got != want {
		t.Fatalf("ReadAll() = %q, want %q", got, want)
	}
}

func TestAgentFSFIFOWriteFailsAfterReaderDisconnect(t *testing.T) {
	t.Parallel()
	fsys := newTestAgentFS(t)

	if err := fsys.MkdirAll(context.Background(), "/tmp", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := fsys.Mkfifo(context.Background(), "/tmp/p", 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}

	reader, err := fsys.OpenFile(context.Background(), "/tmp/p", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(reader) error = %v", err)
	}
	writer, err := fsys.OpenFile(context.Background(), "/tmp/p", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(writer) error = %v", err)
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("reader.Close() error = %v", err)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			t.Errorf("writer.Close() error = %v", err)
		}
	}()

	if _, err := io.WriteString(writer, "hello"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("WriteString() error = %v, want ErrClosedPipe", err)
	}
}

func TestAgentFSBackedRuntimePersistsAcrossRuns(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "sandbox.db")

	first := newAgentFSRuntime(t, dbPath)
	result, err := first.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf 'persisted\\n' > /tmp/persist.txt\n",
	})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("first ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	second := newAgentFSRuntime(t, dbPath)
	result, err = second.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "cat /tmp/persist.txt\n",
	})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("second ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "persisted\n"; got != want {
		t.Fatalf("second Stdout = %q, want %q", got, want)
	}
}

func newTestAgentFS(t *testing.T) *FileSystem {
	t.Helper()
	return newTestAgentFSAt(t, filepath.Join(t.TempDir(), "sandbox.db"))
}

func newTestAgentFSAt(t *testing.T, dbPath string) *FileSystem {
	t.Helper()

	fsys, err := newAgentFS(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("newAgentFS(%q) error = %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := fsys.close(); err != nil {
			t.Errorf("close() error = %v", err)
		}
	})
	return fsys
}

func newAgentFSRuntime(t *testing.T, dbPath string) *gbruntime.Runtime {
	t.Helper()

	rt, err := gbruntime.New(gbruntime.WithFileSystem(
		gbruntime.CustomFileSystem(
			Factory{DBPath: dbPath},
			"/home/agent",
		),
	))
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	return rt
}

func writeFSFile(t *testing.T, fsys gbfs.FileSystem, name, contents string) {
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

func readFSFile(t *testing.T, fsys gbfs.FileSystem, name string) string {
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
