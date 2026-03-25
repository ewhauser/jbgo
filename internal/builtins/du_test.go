package builtins_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestDUVisiblePathAndSymlinkModes(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	mustExecSession(t, session, "mkdir -p /tmp/dir/1/2\n")
	if err := session.FileSystem().Symlink(context.Background(), "dir", "/tmp/slink"); err != nil {
		t.Fatalf("Symlink(slink) error = %v", err)
	}

	result := mustExecSession(t, session, strings.Join([]string{
		"cd /tmp",
		"du slink | cut -f2-",
		"printf '%s\\n' ---",
		"du -D slink | cut -f2-",
		"printf '%s\\n' ---",
		"du slink/ | cut -f2-",
		"printf '%s\\n' ---",
		"du -L slink | cut -f2-",
	}, "\n"))
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}

	const want = "slink\n---\nslink/1/2\nslink/1\nslink\n---\nslink/1/2\nslink/1\nslink/\n---\nslink/1/2\nslink/1\nslink\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDUHardLinksDedupAndCountLinks(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/dir/f1", []byte("payload\n"))
	mustExecSession(t, session, "mkdir -p /tmp/dir/sub\n")
	if err := session.FileSystem().Link(context.Background(), "/tmp/dir/f1", "/tmp/dir/f2"); err != nil {
		t.Fatalf("Link(f2) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "f1", "/tmp/dir/f3"); err != nil {
		t.Fatalf("Symlink(f3) error = %v", err)
	}

	result := mustExecSession(t, session, strings.Join([]string{
		"cd /tmp",
		"du -a -L dir | cut -f2- | sed 's/f[123]/f_/' | sort",
		"printf '%s\\n' ---",
		"du -a -l -L dir | cut -f2- | sort",
		"printf '%s\\n' ---",
		"du -a -L dir dir | cut -f2- | sed 's/f[123]/f_/' | sort",
	}, "\n"))
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}

	const want = "dir\ndir/f_\ndir/sub\n---\ndir\ndir/f1\ndir/f2\ndir/f3\ndir/sub\n---\ndir\ndir/f_\ndir/sub\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDUFiles0FromDeduplicatesAndReportsZeroLengthEntries(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/a", []byte("a"))
	writeSessionFile(t, session, "/tmp/b", []byte("bb"))
	writeSessionFile(t, session, "/tmp/list0", []byte("a\x00\x00b\x00a"))

	result := mustExecSession(t, session, strings.Join([]string{
		"cd /tmp",
		"du --apparent-size --block-size=1 --files0-from=list0 > /tmp/out",
		"status=$?",
		"cut -f2- /tmp/out",
		"printf 'status=%s\\n' \"$status\"",
	}, "\n"))
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\nstatus=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "du: list0:2: invalid zero-length file name\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestDUExcludeAndExcludeFrom(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	mustExecSession(t, session, "mkdir -p /tmp/a/b/c /tmp/a/x/y /tmp/a/u/v\n")
	writeSessionFile(t, session, "/tmp/excl", []byte("b\n"))

	result := mustExecSession(t, session, strings.Join([]string{
		"cd /tmp",
		"du --exclude=x a | cut -f2- | sort",
		"printf '%s\\n' ---",
		"du --exclude-from=excl a | cut -f2- | sort",
		"printf '%s\\n' ---",
		"du --exclude=a a",
		"printf '%s\\n' ---",
		"du --exclude=a/u --exclude=a/b a | cut -f2- | sort",
	}, "\n"))
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}

	const want = "a\na/b\na/b/c\na/u\na/u/v\n---\na\na/u\na/u/v\na/x\na/x/y\n---\n---\na\na/x\na/x/y\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDUInodesThresholdAndWarnings(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/d/f", []byte("x"))
	mustExecSession(t, session, "mkdir -p /tmp/d/sub\n")
	if err := session.FileSystem().Link(context.Background(), "/tmp/d/f", "/tmp/d/h"); err != nil {
		t.Fatalf("Link(h) error = %v", err)
	}

	result := mustExecSession(t, session, strings.Join([]string{
		"cd /tmp",
		"du --inodes d",
		"printf '%s\\n' ---",
		"du --inodes -l d",
		"printf '%s\\n' ---",
		"du --inodes --threshold=3 d",
		"printf '%s\\n' ---",
		"du --inodes --threshold=-2 d",
		"printf '%s\\n' ---",
		"du --inodes -b d",
	}, "\n"))
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}

	const want = "1\td/sub\n3\td\n---\n1\td/sub\n4\td\n---\n3\td\n---\n1\td/sub\n---\n1\td/sub\n3\td\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "ineffective with --inodes") {
		t.Fatalf("Stderr = %q, want ineffective warning", result.Stderr)
	}
}

func TestDURejectsOverflowingSizeArguments(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})

	blockSize := mustExecSession(t, session, "du --block-size=9223372036854775807K .\n")
	if got, want := blockSize.ExitCode, 1; got != want {
		t.Fatalf("blockSize.ExitCode = %d, want %d; stderr=%q", got, want, blockSize.Stderr)
	}
	if got, want := blockSize.Stdout, ""; got != want {
		t.Fatalf("blockSize.Stdout = %q, want %q", got, want)
	}
	if got, want := blockSize.Stderr, "du: invalid --block-size argument '9223372036854775807K'\n"; got != want {
		t.Fatalf("blockSize.Stderr = %q, want %q", got, want)
	}

	threshold := mustExecSession(t, session, "du --threshold=9223372036854775807K .\n")
	if got, want := threshold.ExitCode, 1; got != want {
		t.Fatalf("threshold.ExitCode = %d, want %d; stderr=%q", got, want, threshold.Stderr)
	}
	if got, want := threshold.Stdout, ""; got != want {
		t.Fatalf("threshold.Stdout = %q, want %q", got, want)
	}
	if got, want := threshold.Stderr, "du: invalid --threshold=9223372036854775807K argument '9223372036854775807K'\n"; got != want {
		t.Fatalf("threshold.Stderr = %q, want %q", got, want)
	}
}

func TestDUMaxDepthAndUnreadableDirectoryContinuation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "tmp", "a", "b", "c", "d"),
		filepath.Join(root, "tmp", "f", "a"),
		filepath.Join(root, "tmp", "f", "b"),
		filepath.Join(root, "tmp", "f", "c"),
		filepath.Join(root, "tmp", "f", "d"),
		filepath.Join(root, "tmp", "f", "e"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tmp", "f", "c", "j"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(c/j) error = %v", err)
	}
	unreadableDir := filepath.Join(root, "tmp", "f", "c")
	if err := os.Chmod(unreadableDir, 0o000); err != nil {
		t.Fatalf("Chmod(%q) error = %v", unreadableDir, err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(unreadableDir, 0o755)
	})

	session := newSession(t, &Config{
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
	})

	maxDepth := mustExecSession(t, session, "cd /tmp\ndu -d 1 a | cut -f2-\n")
	if got, want := maxDepth.ExitCode, 0; got != want {
		t.Fatalf("maxDepth.ExitCode = %d, want %d; stderr=%q", got, want, maxDepth.Stderr)
	}
	if got, want := maxDepth.Stdout, "a/b\na\n"; got != want {
		t.Fatalf("maxDepth.Stdout = %q, want %q", got, want)
	}

	result := mustExecSession(t, session, strings.Join([]string{
		"cd /tmp/f",
		"du > /tmp/out 2> /tmp/err",
		"status=$?",
		"cut -f2- /tmp/out | sort",
		"printf '%s\\n' ---",
		"cat /tmp/err",
		"printf 'status=%s\\n' \"$status\"",
	}, "\n"))
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}

	const want = ".\n./a\n./b\n./c\n./d\n./e\n---\ndu: cannot read directory './c': Permission denied\nstatus=1\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
