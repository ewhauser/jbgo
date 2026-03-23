package builtins_test

import (
	"context"
	stdfs "io/fs"
	"strings"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestInstallDirectoryModeOnlyAppliesToTarget(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "install -d -m 0700 /tmp/install/a/b\nstat -c '%a' /tmp/install /tmp/install/a /tmp/install/a/b\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "755\n755\n700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestInstallRejectsDirectoryWithTargetDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "install -d -t /tmp/root foo\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "options --directory and --target-directory are mutually exclusive") {
		t.Fatalf("Stderr = %q, want mutually exclusive error", result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/foo"); err == nil {
		t.Fatalf("Stat(foo) succeeded, want no file created")
	}
}

func TestInstallSupportsCreateLeadingAndTargetDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, strings.Join([]string{
		"printf 'one\\n' > /tmp/src1",
		"printf 'two\\n' > /tmp/src2",
		"install -D /tmp/src1 /tmp/lead/dir/out",
		"install -D -t /tmp/target/dir /tmp/src1 /tmp/src2",
		"cat /tmp/lead/dir/out",
		"cat /tmp/target/dir/src1",
		"cat /tmp/target/dir/src2",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one\none\ntwo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestInstallCreateLeadingFollowsSymlinkedDirectories(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("one\n"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/real", 0o755); err != nil {
		t.Fatalf("MkdirAll(real) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "real", "/tmp/link"); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}

	result := mustExecSession(t, session, "install -D /tmp/src.txt /tmp/link/sub/out.txt\ncat /tmp/real/sub/out.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	info, err := session.FileSystem().Lstat(context.Background(), "/tmp/link")
	if err != nil {
		t.Fatalf("Lstat(link) error = %v", err)
	}
	if info.Mode()&stdfs.ModeSymlink == 0 {
		t.Fatalf("Lstat(link).Mode() = %v, want symlink", info.Mode())
	}
}

func TestInstallCompareSkipsUnchangedCopiesAndWarnsOnSpecialBits(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("payload\n"))

	first := mustExecSession(t, session, "install -Cv -m 0644 /tmp/src.txt /tmp/dst.txt\n")
	if first.ExitCode != 0 {
		t.Fatalf("first install ExitCode = %d, want 0; stderr=%q", first.ExitCode, first.Stderr)
	}
	if got := strings.TrimSpace(first.Stdout); got != "'/tmp/src.txt' -> '/tmp/dst.txt'" {
		t.Fatalf("first install Stdout = %q, want copy message", first.Stdout)
	}

	old := time.Unix(1_700_000_000, 0).UTC()
	if err := session.FileSystem().Chtimes(context.Background(), "/tmp/dst.txt", old, old); err != nil {
		t.Fatalf("Chtimes(dst) error = %v", err)
	}
	before, err := session.FileSystem().Stat(context.Background(), "/tmp/dst.txt")
	if err != nil {
		t.Fatalf("Stat(dst before compare) error = %v", err)
	}

	second := mustExecSession(t, session, "install -Cv -m 0644 /tmp/src.txt /tmp/dst.txt\n")
	if second.ExitCode != 0 {
		t.Fatalf("second install ExitCode = %d, want 0; stderr=%q", second.ExitCode, second.Stderr)
	}
	if second.Stdout != "" {
		t.Fatalf("second install Stdout = %q, want empty compare no-op", second.Stdout)
	}
	afterSecond, err := session.FileSystem().Stat(context.Background(), "/tmp/dst.txt")
	if err != nil {
		t.Fatalf("Stat(dst after compare) error = %v", err)
	}
	if got, want := afterSecond.ModTime(), before.ModTime(); !got.Equal(want) {
		t.Fatalf("ModTime after no-op compare = %v, want %v", got, want)
	}

	third := mustExecSession(t, session, "install -Cv -m 1644 /tmp/src.txt /tmp/dst.txt\n")
	if third.ExitCode != 0 {
		t.Fatalf("third install ExitCode = %d, want 0; stderr=%q", third.ExitCode, third.Stderr)
	}
	if !strings.Contains(third.Stdout, "removed '/tmp/dst.txt'") || !strings.Contains(third.Stdout, "'/tmp/src.txt' -> '/tmp/dst.txt'") {
		t.Fatalf("third install Stdout = %q, want overwrite messages", third.Stdout)
	}
	if !strings.Contains(third.Stderr, "the --compare (-C) option is ignored") {
		t.Fatalf("third install Stderr = %q, want compare warning", third.Stderr)
	}
	afterThird, err := session.FileSystem().Stat(context.Background(), "/tmp/dst.txt")
	if err != nil {
		t.Fatalf("Stat(dst after special-bit compare) error = %v", err)
	}
	if got, want := afterThird.ModTime(), before.ModTime(); got.Equal(want) {
		t.Fatalf("ModTime after forced compare copy = %v, want change from %v", got, want)
	}
}

func TestInstallSupportsBackupOwnershipAndPreservedTimestamps(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("fresh\n"))
	writeSessionFile(t, session, "/tmp/dst.txt", []byte("stale\n"))

	old := time.Unix(1_650_000_000, 0).UTC()
	if err := session.FileSystem().Chtimes(context.Background(), "/tmp/src.txt", old, old); err != nil {
		t.Fatalf("Chtimes(src) error = %v", err)
	}

	result := mustExecSession(t, session, "install -S .bak -o 123 -g 456 -p /tmp/src.txt /tmp/dst.txt\nstat -c '%X:%Y' /tmp/src.txt /tmp/dst.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "1650000000:1650000000\n1650000000:1650000000"; got != want {
		t.Fatalf("Stdout = %q, want preserved timestamp output %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/dst.txt")), "fresh\n"; got != want {
		t.Fatalf("dst contents = %q, want %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/dst.txt.bak")), "stale\n"; got != want {
		t.Fatalf("backup contents = %q, want %q", got, want)
	}

	info, err := session.FileSystem().Stat(context.Background(), "/tmp/dst.txt")
	if err != nil {
		t.Fatalf("Stat(dst) error = %v", err)
	}
	ownership, ok := gbfs.OwnershipFromFileInfo(info)
	if !ok {
		t.Fatalf("OwnershipFromFileInfo(dst) = not found")
	}
	if got, want := ownership, (gbfs.FileOwnership{UID: 123, GID: 456}); got != want {
		t.Fatalf("dst ownership = %#v, want %#v", got, want)
	}
}

func TestInstallStripProgramAndUnprivilegedOwnership(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("payload\n"))
	writeSessionFile(t, session, "/tmp/fake-strip", []byte("#!/bin/sh\nprintf 'stripped\\n' > \"$1\"\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/tmp/fake-strip", 0o755); err != nil {
		t.Fatalf("Chmod(fake-strip) error = %v", err)
	}

	result := mustExecSession(t, session, strings.Join([]string{
		"install -s --strip-program=/tmp/fake-strip /tmp/src.txt /tmp/stripped.txt",
		"install -o 321 -g 654 /tmp/src.txt /tmp/owned.txt",
		"install -U -o 111 -g 222 /tmp/src.txt /tmp/unpriv.txt",
		"cat /tmp/stripped.txt",
		"stat -c '%u:%g' /tmp/owned.txt /tmp/unpriv.txt",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "stripped\n321:654\n1000:1000"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestInstallReplacesDestinationSymlinkInsteadOfFollowingIt(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("new\n"))
	writeSessionFile(t, session, "/tmp/sensitive.txt", []byte("keep\n"))
	if err := session.FileSystem().Symlink(context.Background(), "sensitive.txt", "/tmp/dst.txt"); err != nil {
		t.Fatalf("Symlink(dst) error = %v", err)
	}

	result := mustExecSession(t, session, "install /tmp/src.txt /tmp/dst.txt\ncat /tmp/dst.txt\ncat /tmp/sensitive.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "new\nkeep\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	info, err := session.FileSystem().Lstat(context.Background(), "/tmp/dst.txt")
	if err != nil {
		t.Fatalf("Lstat(dst) error = %v", err)
	}
	if info.Mode()&stdfs.ModeSymlink != 0 {
		t.Fatalf("Lstat(dst).Mode() = %v, want regular file", info.Mode())
	}
}

func TestInstallReplacesSymlinkToSourceWithoutSameFileError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("new\n"))
	if err := session.FileSystem().Symlink(context.Background(), "src.txt", "/tmp/dst.txt"); err != nil {
		t.Fatalf("Symlink(dst) error = %v", err)
	}

	result := mustExecSession(t, session, "install /tmp/src.txt /tmp/dst.txt\ncat /tmp/dst.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "new\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	info, err := session.FileSystem().Lstat(context.Background(), "/tmp/dst.txt")
	if err != nil {
		t.Fatalf("Lstat(dst) error = %v", err)
	}
	if info.Mode()&stdfs.ModeSymlink != 0 {
		t.Fatalf("Lstat(dst).Mode() = %v, want regular file", info.Mode())
	}
}

func TestInstallNumberedBackupsAdvanceFromHighestExistingIndex(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("fresh\n"))
	writeSessionFile(t, session, "/tmp/dst.txt", []byte("stale\n"))
	writeSessionFile(t, session, "/tmp/dst.txt.~2~", []byte("older\n"))

	result := mustExecSession(t, session, "install --backup=numbered /tmp/src.txt /tmp/dst.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/dst.txt.~3~")), "stale\n"; got != want {
		t.Fatalf("numbered backup contents = %q, want %q", got, want)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/tmp/dst.txt.~1~"); err == nil {
		t.Fatalf("Stat(.~1~) succeeded, want sparse history to stay sparse")
	}
}

func TestInstallExistingBackupsDetectHigherNumberedSeries(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("fresh\n"))
	writeSessionFile(t, session, "/tmp/dst.txt", []byte("stale\n"))
	writeSessionFile(t, session, "/tmp/dst.txt.~2~", []byte("older\n"))

	result := mustExecSession(t, session, "install -b /tmp/src.txt /tmp/dst.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/dst.txt.~3~")), "stale\n"; got != want {
		t.Fatalf("existing backup contents = %q, want %q", got, want)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/tmp/dst.txt~"); err == nil {
		t.Fatalf("Stat(simple backup) succeeded, want numbered backup")
	}
}

func TestInstallRejectsOutOfRangeOctalMode(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/src.txt", []byte("payload\n"))

	result := mustExecSession(t, session, "install -m 10000 /tmp/src.txt /tmp/dst.txt\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "install: invalid mode '10000'") {
		t.Fatalf("Stderr = %q, want invalid mode error", result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/tmp/dst.txt"); err == nil {
		t.Fatalf("Stat(dst) succeeded, want no file created")
	}
}
