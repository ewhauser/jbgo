package builtins_test

import (
	"bytes"
	"context"
	stdfs "io/fs"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestFindSupportsCaseInsensitivePathAndRegexFlagsIsolated(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/dir/README.md", []byte("readme"))
	writeSessionFile(t, session, "/dir/readme.txt", []byte("lower"))
	writeSessionFile(t, session, "/dir/sub/other.txt", []byte("nested"))
	writeSessionFile(t, session, "/Project/SRC/file.ts", []byte("upper"))
	writeSessionFile(t, session, "/Project/src/other.ts", []byte("lower"))

	result := mustExecSession(t, session,
		"find /dir -iname \"readme*\"\n"+
			"find /Project -ipath \"*src*\"\n"+
			"find /dir -regex \".*\\\\.txt\"\n"+
			"find /dir -iregex \".*\\\\.TXT\"\n"+
			"find /Project -path \"*/SRC/*\"\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/dir/README.md\n/dir/readme.txt\n/Project/SRC\n/Project/SRC/file.ts\n/Project/src\n/Project/src/other.ts\n/dir/readme.txt\n/dir/sub/other.txt\n/dir/readme.txt\n/dir/sub/other.txt\n/Project/SRC/file.ts\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsEmptyMTimeAndNewerFlagsIsolated(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/empty/empty.txt", nil)
	writeSessionFile(t, session, "/empty/notempty/file.txt", []byte("content"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/empty/emptydir", 0o755); err != nil {
		t.Fatalf("MkdirAll(emptydir) error = %v", err)
	}

	writeSessionFile(t, session, "/mtime/recent.txt", []byte("recent"))
	writeSessionFile(t, session, "/mtime/old.txt", []byte("old"))

	writeSessionFile(t, session, "/newer/ref.txt", []byte("ref"))
	writeSessionFile(t, session, "/newer/newer.txt", []byte("newer"))
	writeSessionFile(t, session, "/newer/older.txt", []byte("older"))

	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)
	ref := now.Add(-time.Minute)
	older := ref.Add(-time.Minute)
	for _, item := range []struct {
		path string
		when time.Time
	}{
		{"/mtime/recent.txt", now},
		{"/mtime/old.txt", old},
		{"/newer/ref.txt", ref},
		{"/newer/newer.txt", now},
		{"/newer/older.txt", older},
	} {
		if err := session.FileSystem().Chtimes(context.Background(), item.path, item.when, item.when); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", item.path, err)
		}
	}

	result := mustExecSession(t, session,
		"find /empty -empty -type f\n"+
			"find /empty -empty -type d\n"+
			"find /mtime -type f -mtime +7\n"+
			"find /mtime -type f -mtime -7\n"+
			"find /mtime -type f -mtime 0\n"+
			"find /newer -type f -newer /newer/ref.txt\n"+
			"find /newer -type f -newer /newer/missing.txt\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/empty/empty.txt\n/empty/emptydir\n/mtime/old.txt\n/mtime/recent.txt\n/mtime/recent.txt\n/newer/newer.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsSizeFlagsIsolated(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/size/large.txt", bytes.Repeat([]byte("x"), 2048))
	writeSessionFile(t, session, "/size/exact.txt", []byte("12345"))
	writeSessionFile(t, session, "/size/small.txt", []byte("tiny"))

	result := mustExecSession(t, session,
		"find /size -type f -size +1k\n"+
			"find /size -type f -size -100c\n"+
			"find /size -type f -size 5c\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/size/large.txt\n/size/exact.txt\n/size/small.txt\n/size/exact.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsPermMindepthDepthAndPrune(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/perm/exact.sh", []byte("#!/bin/sh\n"))
	writeSessionFile(t, session, "/perm/plain.txt", []byte("plain\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/perm/exact.sh", 0o755); err != nil {
		t.Fatalf("Chmod(exact.sh) error = %v", err)
	}
	if err := session.FileSystem().Chmod(context.Background(), "/perm/plain.txt", 0o644); err != nil {
		t.Fatalf("Chmod(plain.txt) error = %v", err)
	}

	writeSessionFile(t, session, "/tree/root.txt", []byte("root\n"))
	writeSessionFile(t, session, "/tree/sub/child.txt", []byte("child\n"))
	writeSessionFile(t, session, "/prune/keep/seen.txt", []byte("seen\n"))
	writeSessionFile(t, session, "/prune/skip/hidden.txt", []byte("hidden\n"))

	result := mustExecSession(t, session,
		"find /perm -type f -perm 755\n"+
			"echo ---\n"+
			"find /perm -type f -perm -600\n"+
			"echo ---\n"+
			"find /perm -type f -perm /111\n"+
			"echo ---\n"+
			"find /tree -mindepth 1 -maxdepth 1\n"+
			"echo ---\n"+
			"find /tree -depth -maxdepth 1\n"+
			"echo ---\n"+
			"find /prune -path '/prune/skip*' -prune -o -print\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 6 {
		t.Fatalf("Stdout blocks = %q, want 6 blocks", result.Stdout)
	}

	if got, want := strings.TrimSpace(parts[0]), "/perm/exact.sh"; got != want {
		t.Fatalf("perm exact = %q, want %q", got, want)
	}
	if got, want := parts[1], "/perm/exact.sh\n/perm/plain.txt\n"; got != want {
		t.Fatalf("perm all = %q, want %q", got, want)
	}
	if got, want := strings.TrimSpace(parts[2]), "/perm/exact.sh"; got != want {
		t.Fatalf("perm any = %q, want %q", got, want)
	}
	if got, want := parts[3], "/tree/root.txt\n/tree/sub\n"; got != want {
		t.Fatalf("mindepth/maxdepth = %q, want %q", got, want)
	}
	if got, want := parts[4], "/tree/root.txt\n/tree/sub\n/tree\n"; got != want {
		t.Fatalf("depth order = %q, want %q", got, want)
	}
	if got, want := parts[5], "/prune\n/prune/keep\n/prune/keep/seen.txt\n"; got != want {
		t.Fatalf("prune output = %q, want %q", got, want)
	}
}

func TestFindSupportsExecPrint0PrintfAndDelete(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/exec/a.txt", []byte("a\n"))
	writeSessionFile(t, session, "/exec/b.txt", []byte("b\n"))
	writeSessionFile(t, session, "/nul/one", []byte("1"))
	writeSessionFile(t, session, "/nul/two", []byte("2"))
	writeSessionFile(t, session, "/printf/file.txt", []byte("data"))
	if err := session.FileSystem().Chmod(context.Background(), "/printf/file.txt", 0o755); err != nil {
		t.Fatalf("Chmod(printf/file.txt) error = %v", err)
	}
	writeSessionFile(t, session, "/delete/keep.txt", []byte("keep\n"))
	writeSessionFile(t, session, "/delete/remove.tmp", []byte("remove\n"))

	result := mustExecSession(t, session,
		"find /exec -name '*.txt' -exec echo FILE {} \\;\n"+
			"echo ---\n"+
			"find /exec -name '*.txt' -exec echo BATCH {} +\n"+
			"echo ---\n"+
			"find /nul -mindepth 1 -maxdepth 1 -print0 > /tmp/find-zero.out\n"+
			"stat -c '%s' /tmp/find-zero.out\n"+
			"echo ---\n"+
			"find /printf/file.txt -printf '%f|%h|%p|%P|%s|%d|%m|%M\\n'\n"+
			"echo ---\n"+
			"find /delete -name '*.tmp' -delete\n"+
			"find /delete -print\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 5 {
		t.Fatalf("Stdout blocks = %q, want 5 blocks", result.Stdout)
	}

	if got, want := parts[0], "FILE /exec/a.txt\nFILE /exec/b.txt\n"; got != want {
		t.Fatalf("exec each = %q, want %q", got, want)
	}
	if got, want := parts[1], "BATCH /exec/a.txt /exec/b.txt\n"; got != want {
		t.Fatalf("exec batch = %q, want %q", got, want)
	}
	if got, want := strings.TrimSpace(parts[2]), "18"; got != want {
		t.Fatalf("print0 size = %q, want %q", got, want)
	}
	if got, want := parts[3], "file.txt|/printf|/printf/file.txt||4|0|755|-rwxr-xr-x\n"; got != want {
		t.Fatalf("printf output = %q, want %q", got, want)
	}
	if got, want := parts[4], "/delete\n/delete/keep.txt\n"; got != want {
		t.Fatalf("delete output = %q, want %q", got, want)
	}
}

func TestFindFromRootKeepsRelativePathsNormalized(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/pkg/testdata/fuzz/FuzzFoo/corpus1", []byte("data"))

	result := mustExecSession(t, session, "cd /\nfind . -type f -path '*/testdata/fuzz/Fuzz*/*'\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "./pkg/testdata/fuzz/FuzzFoo/corpus1"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindPreservesAbsoluteRootSpellingInOutput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/rooted/file.txt", []byte("data"))

	result := mustExecSession(t, session,
		"find /rooted/ -maxdepth 1 -printf '<%P>|<%p>\\n'\n"+
			"echo ---\n"+
			"find /rooted/../rooted -maxdepth 1\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got, want := parts[0], "<>|</rooted/>\n<file.txt>|</rooted/file.txt>\n"; got != want {
		t.Fatalf("printf output = %q, want %q", got, want)
	}
	if got, want := parts[1], "/rooted/../rooted\n/rooted/../rooted/file.txt\n"; got != want {
		t.Fatalf("find output = %q, want %q", got, want)
	}
}

func TestFindTypeQueriesAvoidDirEntryInfoForKnownEntries(t *testing.T) {
	t.Parallel()

	var tracked *infoCountingFS
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(gbfs.FactoryFunc(func(context.Context) (gbfs.FileSystem, error) {
			tracked = &infoCountingFS{FileSystem: gbfs.NewMemory()}
			return tracked, nil
		}), defaultHomeDir),
	})

	writeSessionFile(t, session, "/bench/a.txt", []byte("a"))
	writeSessionFile(t, session, "/bench/sub/b.txt", []byte("b"))

	tracked.infoCalls.Store(0)
	result := mustExecSession(t, session, "find /bench -type f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/bench/a.txt\n/bench/sub/b.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := tracked.infoCalls.Load(); got != 0 {
		t.Fatalf("DirEntry.Info() calls = %d, want 0", got)
	}
}

func TestFindMetadataFreePrintfAvoidsDirEntryInfo(t *testing.T) {
	t.Parallel()

	var tracked *infoCountingFS
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(gbfs.FactoryFunc(func(context.Context) (gbfs.FileSystem, error) {
			tracked = &infoCountingFS{FileSystem: gbfs.NewMemory()}
			return tracked, nil
		}), defaultHomeDir),
	})

	writeSessionFile(t, session, "/printf/a.txt", []byte("a"))
	writeSessionFile(t, session, "/printf/sub/b.txt", []byte("b"))

	tracked.infoCalls.Store(0)
	result := mustExecSession(t, session, "find /printf -mindepth 1 -maxdepth 1 -printf '%f|%h|%p|%P|%d\\n'\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a.txt|/printf|/printf/a.txt|a.txt|1\nsub|/printf|/printf/sub|sub|1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := tracked.infoCalls.Load(); got != 0 {
		t.Fatalf("DirEntry.Info() calls = %d, want 0", got)
	}
}

func TestFindSymlinkBehavior(t *testing.T) {
	t.Parallel()

	t.Run("follow mode uses target type and descends", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{
			FileSystem: CustomFileSystem(gbfs.FactoryFunc(func(context.Context) (gbfs.FileSystem, error) {
				return gbfs.NewMemory(), nil
			}), defaultHomeDir),
			Policy: policy.NewStatic(&policy.Config{
				ReadRoots:   []string{"/links", "/usr/bin", "/bin"},
				WriteRoots:  []string{"/links"},
				SymlinkMode: policy.SymlinkFollow,
			}),
		})

		writeSessionFile(t, session, "/links/target.txt", []byte("target"))
		writeSessionFile(t, session, "/links/dir/inner.txt", []byte("inner"))
		if err := session.FileSystem().Symlink(context.Background(), "target.txt", "/links/file-link"); err != nil {
			t.Fatalf("Symlink(file-link) error = %v", err)
		}
		if err := session.FileSystem().Symlink(context.Background(), "dir", "/links/dir-link"); err != nil {
			t.Fatalf("Symlink(dir-link) error = %v", err)
		}

		result := mustExecSession(t, session,
			"find /links/file-link -type f\n"+
				"find /links/dir-link -type d\n"+
				"find /links/dir-link -type f\n",
		)
		if result.ExitCode != 0 {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		if got, want := result.Stdout, "/links/file-link\n/links/dir-link\n/links/dir-link/inner.txt\n"; got != want {
			t.Fatalf("Stdout = %q, want %q", got, want)
		}
	})

	t.Run("default policy still denies symlink traversal", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{
			FileSystem: CustomFileSystem(gbfs.FactoryFunc(func(context.Context) (gbfs.FileSystem, error) {
				return gbfs.NewMemory(), nil
			}), defaultHomeDir),
		})

		writeSessionFile(t, session, "/links/target.txt", []byte("target"))
		if err := session.FileSystem().Symlink(context.Background(), "target.txt", "/links/file-link"); err != nil {
			t.Fatalf("Symlink(file-link) error = %v", err)
		}

		result := mustExecSession(t, session, "find /links/file-link -type f\n")
		if result.ExitCode != 126 {
			t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "symlink traversal denied") {
			t.Fatalf("Stderr = %q, want symlink denial", result.Stderr)
		}
	})

	t.Run("dangling symlink still reports missing root", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{
			FileSystem: CustomFileSystem(gbfs.FactoryFunc(func(context.Context) (gbfs.FileSystem, error) {
				return gbfs.NewMemory(), nil
			}), defaultHomeDir),
			Policy: policy.NewStatic(&policy.Config{
				ReadRoots:   []string{"/links", "/usr/bin", "/bin"},
				WriteRoots:  []string{"/links"},
				SymlinkMode: policy.SymlinkFollow,
			}),
		})

		if err := session.FileSystem().MkdirAll(context.Background(), "/links", 0o755); err != nil {
			t.Fatalf("MkdirAll(/links) error = %v", err)
		}
		if err := session.FileSystem().Symlink(context.Background(), "missing.txt", "/links/dangle"); err != nil {
			t.Fatalf("Symlink(dangle) error = %v", err)
		}

		result := mustExecSession(t, session, "find /links/dangle -type f\n")
		if result.ExitCode != 1 {
			t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
		}
		if got, want := result.Stderr, "find: /links/dangle: No such file or directory\n"; got != want {
			t.Fatalf("Stderr = %q, want %q", got, want)
		}
		if result.Stdout != "" {
			t.Fatalf("Stdout = %q, want empty", result.Stdout)
		}
	})
}

type infoCountingFS struct {
	gbfs.FileSystem
	infoCalls atomic.Int32
}

func (fs *infoCountingFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	entries, err := fs.FileSystem.ReadDir(ctx, name)
	if err != nil {
		return nil, err
	}
	wrapped := make([]stdfs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		wrapped = append(wrapped, infoCountingDirEntry{DirEntry: entry, infoCalls: &fs.infoCalls})
	}
	return wrapped, nil
}

type infoCountingDirEntry struct {
	stdfs.DirEntry
	infoCalls *atomic.Int32
}

func (e infoCountingDirEntry) Info() (stdfs.FileInfo, error) {
	e.infoCalls.Add(1)
	return e.DirEntry.Info()
}
