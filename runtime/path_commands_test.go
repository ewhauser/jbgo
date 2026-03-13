package runtime

import (
	"context"
	"io"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestTouchCreatesFilesAndPreservesExistingContent(t *testing.T) {
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/home/agent/existing.txt", []byte("keep\n"))
	before, err := session.FileSystem().Stat(context.Background(), "/home/agent/existing.txt")
	if err != nil {
		t.Fatalf("Stat(before) error = %v", err)
	}

	result := mustExecSession(t, session, "touch existing.txt new.txt another.txt\ncat existing.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "keep\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, name := range []string{"/home/agent/new.txt", "/home/agent/another.txt"} {
		if _, err := session.FileSystem().Stat(context.Background(), name); err != nil {
			t.Fatalf("Stat(%q) error = %v", name, err)
		}
	}
	after, err := session.FileSystem().Stat(context.Background(), "/home/agent/existing.txt")
	if err != nil {
		t.Fatalf("Stat(after) error = %v", err)
	}
	if !after.ModTime().After(before.ModTime()) && !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("ModTime after touch = %v, want >= %v", after.ModTime(), before.ModTime())
	}
}

func TestTouchSupportsNoCreateAndDateParsing(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "touch -c missing.txt\ntouch -d 2024-01-02 dated.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/missing.txt"); !os.IsNotExist(err) {
		t.Fatalf("Stat(missing.txt) error = %v, want not exist", err)
	}
	info, err := session.FileSystem().Stat(context.Background(), "/home/agent/dated.txt")
	if err != nil {
		t.Fatalf("Stat(dated.txt) error = %v", err)
	}
	if got, want := info.ModTime().UTC().Format("2006-01-02"), "2024-01-02"; got != want {
		t.Fatalf("ModTime = %q, want %q", got, want)
	}
}

func TestTruncateCreatesAndResizesFiles(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/target.txt", []byte("1234567890"))

	result := mustExecSession(t, session, "truncate --size ' 4' /home/agent/target.txt\nstat -c '%s' /home/agent/target.txt\ntruncate -s +3 /home/agent/target.txt\nstat -c '%s' /home/agent/target.txt\ntruncate -o -s-1 /home/agent/negative.txt\nstat -c '%s' /home/agent/negative.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "4\n7\n0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	if got := readSessionFile(t, session, "/home/agent/target.txt"); string(got) != "1234\x00\x00\x00" {
		t.Fatalf("target contents = %q, want %q", got, "1234\x00\x00\x00")
	}
	if got := readSessionFile(t, session, "/home/agent/negative.txt"); len(got) != 0 {
		t.Fatalf("negative.txt len = %d, want 0", len(got))
	}
}

func TestTruncateRelativeModes(t *testing.T) {
	session := newSession(t, &Config{})
	for _, name := range []string{"at-most.txt", "at-least.txt", "round-down.txt", "round-up.txt"} {
		writeSessionFile(t, session, "/home/agent/"+name, []byte("1234567890"))
	}

	result := mustExecSession(t, session, "truncate --size '<4' /home/agent/at-most.txt\ntruncate --size '>15' /home/agent/at-least.txt\ntruncate --size '/4' /home/agent/round-down.txt\ntruncate --size '%4' /home/agent/round-up.txt\nstat -c '%s' /home/agent/at-most.txt /home/agent/at-least.txt /home/agent/round-down.txt /home/agent/round-up.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "4\n15\n8\n12\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTruncateReferenceAndNoCreate(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/ref.txt", []byte("1234567890"))

	result := mustExecSession(t, session, "truncate -r /home/agent/ref.txt /home/agent/from-ref.txt\ntruncate -c -r /home/agent/ref.txt /home/agent/no-create.txt\ntruncate -r /home/agent/ref.txt -s +5 /home/agent/from-ref-plus.txt\nstat -c '%s' /home/agent/from-ref.txt /home/agent/from-ref-plus.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "10\n15\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/no-create.txt"); !os.IsNotExist(err) {
		t.Fatalf("Stat(no-create.txt) error = %v, want not exist", err)
	}
}

func TestTruncateReportsErrors(t *testing.T) {
	t.Run("missing file operand", func(t *testing.T) {
		session := newSession(t, &Config{})
		result := mustExecSession(t, session, "truncate -s 1\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "missing file operand") {
			t.Fatalf("Stderr = %q, want missing file operand", result.Stderr)
		}
	})

	t.Run("missing size or reference", func(t *testing.T) {
		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/home/agent/file.txt", []byte("1234"))

		result := mustExecSession(t, session, "truncate /home/agent/file.txt\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "you must specify either '--size' or '--reference'") {
			t.Fatalf("Stderr = %q, want missing size/reference guidance", result.Stderr)
		}
	})

	t.Run("invalid size", func(t *testing.T) {
		session := newSession(t, &Config{})
		result := mustExecSession(t, session, "truncate -s 0B /home/agent/file.txt\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "Invalid number: '0B'") {
			t.Fatalf("Stderr = %q, want invalid number message", result.Stderr)
		}
	})

	t.Run("division by zero", func(t *testing.T) {
		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/home/agent/file.txt", []byte("1234"))

		result := mustExecSession(t, session, "truncate -s /0 /home/agent/file.txt\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "division by zero") {
			t.Fatalf("Stderr = %q, want division by zero", result.Stderr)
		}
	})

	t.Run("missing reference file", func(t *testing.T) {
		session := newSession(t, &Config{})
		result := mustExecSession(t, session, "truncate -r /home/agent/missing.ref /home/agent/file.txt\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "cannot stat '/home/agent/missing.ref': No such file or directory") {
			t.Fatalf("Stderr = %q, want missing reference error", result.Stderr)
		}
	})

	t.Run("missing parent directory", func(t *testing.T) {
		session := newSession(t, &Config{})
		result := mustExecSession(t, session, "truncate -s 0 /home/agent/missing/child.txt\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "cannot open '/home/agent/missing/child.txt' for writing: No such file or directory") {
			t.Fatalf("Stderr = %q, want missing parent error", result.Stderr)
		}
	})
}

func TestTruncateNoCreateIgnoresMissingTargets(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "truncate -c -s 8 /home/agent/missing/child.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("output = stdout %q stderr %q, want empty", result.Stdout, result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/missing/child.txt"); !os.IsNotExist(err) {
		t.Fatalf("Stat(missing child) error = %v, want not exist", err)
	}
}

func TestRmdirRemovesEmptyDirectoriesAndParents(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/a/b/c\nrmdir -p /home/agent/a/b/c\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, name := range []string{"/home/agent/a/b/c", "/home/agent/a/b", "/home/agent/a"} {
		if _, err := session.FileSystem().Stat(context.Background(), name); !os.IsNotExist(err) {
			t.Fatalf("Stat(%q) error = %v, want not exist", name, err)
		}
	}
}

func TestRmdirRejectsNonEmptyDirectories(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir\necho hi > /home/agent/dir/file.txt\nrmdir /home/agent/dir\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Directory not empty") {
		t.Fatalf("Stderr = %q, want non-empty error", result.Stderr)
	}
}

func TestRmdirIgnoresFailOnNonEmptyParents(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p a/b/c a/x\nrmdir -p --ignore-fail-on-non-empty a/b/c\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	for _, name := range []string{"/home/agent/a/x", "/home/agent/a"} {
		if _, err := session.FileSystem().Stat(context.Background(), name); err != nil {
			t.Fatalf("Stat(%q) error = %v, want present", name, err)
		}
	}
	for _, name := range []string{"/home/agent/a/b/c", "/home/agent/a/b"} {
		if _, err := session.FileSystem().Stat(context.Background(), name); !os.IsNotExist(err) {
			t.Fatalf("Stat(%q) error = %v, want not exist", name, err)
		}
	}
}

func TestRmdirReportsSymlinkSlashErrors(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	setup := mustExecSession(t, session, "mkdir dir\nmkdir dir/dir2\necho hi > file\nln -s dir sl\nln -s missing dl\nln -s file fl\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
	}

	fileLink := mustExecSession(t, session, "rmdir fl/\n")
	if fileLink.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", fileLink.ExitCode)
	}
	if got, want := fileLink.Stderr, "rmdir: failed to remove 'fl/': Not a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}

	dirLink := mustExecSession(t, session, "rmdir sl/\n")
	if dirLink.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", dirLink.ExitCode)
	}
	if got, want := dirLink.Stderr, "rmdir: failed to remove 'sl/': Symbolic link not followed\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}

	danglingLink := mustExecSession(t, session, "rmdir dl/\n")
	if danglingLink.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", danglingLink.ExitCode)
	}
	if got, want := danglingLink.Stderr, "rmdir: failed to remove 'dl/': Symbolic link not followed\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestRmdirParentsKeepRelativeSymlinkDiagnostics(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	setup := mustExecSession(t, session, "mkdir -p dir/dir2\nln -s dir sl\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
	}

	result := mustExecSession(t, session, "rmdir -p sl/dir2\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if got, want := result.Stderr, "rmdir: failed to remove 'sl': Not a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/dir/dir2"); !os.IsNotExist(err) {
		t.Fatalf("Stat(dir/dir2) error = %v, want not exist", err)
	}
}

func TestLNCreatesSymlinkAndReadlinkPrintsTarget(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo data > /home/agent/file.txt\nln -s file.txt /home/agent/link.txt\nreadlink /home/agent/link.txt\nreadlink -f /home/agent/link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "file.txt\n/home/agent/file.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestReadlinkCanonicalizeModes(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	script := strings.Join([]string{
		"mkdir dir",
		"mkdir dir/subdir",
		"echo hi > file",
		"ln -s file link-file",
		"ln -s missing link-missing",
		"ln -s dir/subdir link-sub",
		"readlink -e file",
		"readlink -f link-missing/",
		"readlink -m link-file/more",
		"readlink -f link-sub/..",
		"",
	}, "\n")

	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/home/agent/file\n/home/agent/missing\n/home/agent/file/more\n/home/agent/dir\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestReadlinkZeroAndNoNewlineWithMultipleArgs(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "readlink -n -m --zero /1 /1\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/1\x00/1\x00"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestReadlinkPosixAndVerboseErrors(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	quiet := mustExecSession(t, session, "echo hi > file\nreadlink file\n")
	if quiet.ExitCode != 1 {
		t.Fatalf("quiet ExitCode = %d, want 1; stderr=%q", quiet.ExitCode, quiet.Stderr)
	}
	if quiet.Stdout != "" || quiet.Stderr != "" {
		t.Fatalf("quiet output = (%q, %q), want empty output", quiet.Stdout, quiet.Stderr)
	}

	posix := mustExecSession(t, session, "POSIXLY_CORRECT=1 readlink file\n")
	if posix.ExitCode != 1 {
		t.Fatalf("posix ExitCode = %d, want 1; stderr=%q", posix.ExitCode, posix.Stderr)
	}
	if got, want := posix.Stderr, "readlink: file: Invalid argument\n"; got != want {
		t.Fatalf("posix stderr = %q, want %q", got, want)
	}

	loop := mustExecSession(t, session, "ln -s loop loop\nreadlink -ve loop\n")
	if loop.ExitCode != 1 {
		t.Fatalf("loop ExitCode = %d, want 1; stderr=%q", loop.ExitCode, loop.Stderr)
	}
	if !strings.Contains(loop.Stderr, "Too many levels of symbolic links") {
		t.Fatalf("loop stderr = %q, want symlink loop diagnostic", loop.Stderr)
	}
}

func TestMktempCreatesFilesAndDirectories(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p nested\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
	}

	result := mustExecSession(t, session, "mktemp nested/file.XXXX\nmktemp XXX_XXX\nmktemp -d /tmp/dir.XXXX\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %d, want 3; stdout=%q", len(lines), result.Stdout)
	}
	if !strings.HasPrefix(lines[0], "nested/file.") {
		t.Fatalf("first output = %q, want nested/file.*", lines[0])
	}
	if got, want := lines[1][:4], "XXX_"; got != want {
		t.Fatalf("second output prefix = %q, want %q", got, want)
	}
	if !strings.HasPrefix(lines[2], "/tmp/dir.") {
		t.Fatalf("third output = %q, want /tmp/dir.*", lines[2])
	}

	fileInfo, err := session.FileSystem().Stat(context.Background(), mktempRuntimePath(lines[0]))
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", lines[0], err)
	}
	if fileInfo.IsDir() {
		t.Fatalf("Stat(%q) reported directory, want file", lines[0])
	}
	if got, want := fileInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("file perm = %#o, want %#o", got, want)
	}

	dirInfo, err := session.FileSystem().Stat(context.Background(), mktempRuntimePath(lines[2]))
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", lines[2], err)
	}
	if !dirInfo.IsDir() {
		t.Fatalf("Stat(%q) reported non-directory, want directory", lines[2])
	}
	if got, want := dirInfo.Mode().Perm(), os.FileMode(0o700); got != want {
		t.Fatalf("dir perm = %#o, want %#o", got, want)
	}
}

func TestMktempSupportsDryRunSuffixAndQuiet(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mktemp -u --suffix=.txt dry.XXXX\nmktemp --suffix=.log keep.XXXX\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %d, want 2; stdout=%q", len(lines), result.Stdout)
	}
	if !strings.HasSuffix(lines[0], ".txt") {
		t.Fatalf("dry-run output = %q, want .txt suffix", lines[0])
	}
	if !strings.HasSuffix(lines[1], ".log") {
		t.Fatalf("created output = %q, want .log suffix", lines[1])
	}
	if _, err := session.FileSystem().Stat(context.Background(), mktempRuntimePath(lines[0])); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) error = %v, want not exist for dry run", lines[0], err)
	}
	if _, err := session.FileSystem().Stat(context.Background(), mktempRuntimePath(lines[1])); err != nil {
		t.Fatalf("Stat(%q) error = %v, want created file", lines[1], err)
	}

	quiet := mustExecSession(t, newSession(t, &Config{}), "mktemp -q -p /definitely/not/exist/I/promise\n")
	if quiet.ExitCode != 1 {
		t.Fatalf("quiet ExitCode = %d, want 1", quiet.ExitCode)
	}
	if quiet.Stdout != "" || quiet.Stderr != "" {
		t.Fatalf("quiet output = (%q, %q), want empty output", quiet.Stdout, quiet.Stderr)
	}
}

func TestMktempTmpdirAndDeprecatedTHandling(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p a\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
	}

	script := strings.Join([]string{
		"TMPDIR=. mktemp",
		"TMPDIR=. mktemp XXX",
		"TMPDIR=. mktemp -t foo.XXXX",
		"TMPDIR=. mktemp -t -p should_not_exist foo.XXXX",
		"mktemp --tmpdir foo.XXXX",
		"mktemp --directory --tmpdir apt-key-gpghome.XXXX",
		"mktemp --tmpdir=. a/bXXXX",
		"",
	}, "\n")
	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 7 {
		t.Fatalf("Stdout lines = %d, want 7; stdout=%q", len(lines), result.Stdout)
	}
	if !strings.HasPrefix(lines[0], "./tmp.") {
		t.Fatalf("mktemp output = %q, want ./tmp.*", lines[0])
	}
	if strings.HasPrefix(lines[1], "./") || strings.Contains(lines[1], "/") {
		t.Fatalf("mktemp XXX output = %q, want plain relative name", lines[1])
	}
	if !strings.HasPrefix(lines[2], "./foo.") {
		t.Fatalf("mktemp -t output = %q, want TMPDIR-based relative path", lines[2])
	}
	if !strings.HasPrefix(lines[3], "./foo.") {
		t.Fatalf("mktemp -t -p output = %q, want TMPDIR to override -p", lines[3])
	}
	if !strings.HasPrefix(lines[4], "/tmp/foo.") {
		t.Fatalf("mktemp --tmpdir output = %q, want /tmp/foo.*", lines[4])
	}
	if !strings.HasPrefix(lines[5], "/tmp/apt-key-gpghome.") {
		t.Fatalf("mktemp --directory --tmpdir output = %q, want /tmp/apt-key-gpghome.*", lines[5])
	}
	if !strings.HasPrefix(lines[6], "./a/b") {
		t.Fatalf("mktemp --tmpdir=. a/bXXXX output = %q, want ./a/b*", lines[6])
	}

	for i, name := range lines {
		info, err := session.FileSystem().Stat(context.Background(), mktempRuntimePath(name))
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", name, err)
		}
		if i == 5 {
			if !info.IsDir() {
				t.Fatalf("Stat(%q) reported non-directory, want directory", name)
			}
			continue
		}
		if info.IsDir() {
			t.Fatalf("Stat(%q) reported directory, want file", name)
		}
	}
}

func TestMktempTreatsEmptyTmpdirValuesAsFallback(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "TMPDIR=. mktemp -p ''\nTMPDIR=. mktemp --tmpdir=\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %d, want 2; stdout=%q", len(lines), result.Stdout)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "./tmp.") {
			t.Fatalf("output = %q, want TMPDIR-based ./tmp.* path", line)
		}
		if _, err := session.FileSystem().Stat(context.Background(), mktempRuntimePath(line)); err != nil {
			t.Fatalf("Stat(%q) error = %v, want created file", line, err)
		}
	}
}

func TestMktempRejectsInvalidTemplates(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantStderr string
	}{
		{
			name:       "too few Xs",
			script:     "mktemp aXX\n",
			wantStderr: "mktemp: too few X's in template 'aXX'\n",
		},
		{
			name:       "suffix requires trailing X",
			script:     "mktemp --suffix=.txt aXXXb\n",
			wantStderr: "mktemp: with --suffix, template 'aXXXb' must end in X\n",
		},
		{
			name:       "deprecated template separator",
			script:     "mktemp -t a/bXXX\n",
			wantStderr: "mktemp: invalid template, 'a/bXXX', contains directory separator\n",
		},
		{
			name:       "tmpdir forbids absolute template",
			script:     "mktemp --tmpdir=. /XXX\n",
			wantStderr: "mktemp: invalid template, '/XXX'; with --tmpdir, it may not be absolute\n",
		},
		{
			name:       "too many templates",
			script:     "mktemp a b\n",
			wantStderr: "mktemp: too many templates\nTry 'mktemp --help' for more information.\n",
		},
		{
			name:       "posixly correct requires template last",
			script:     "POSIXLY_CORRECT=1 mktemp aXXXX --suffix=b\n",
			wantStderr: "mktemp: too many templates\nTry 'mktemp --help' for more information.\n",
		},
		{
			name:       "missing short tmpdir argument",
			script:     "mktemp -p\n",
			wantStderr: "mktemp: option requires an argument -- 'p'\nTry 'mktemp --help' for more information.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := newSession(t, &Config{})
			result := mustExecSession(t, session, tt.script)
			if result.ExitCode == 0 {
				t.Fatalf("ExitCode = 0, want non-zero; stdout=%q", result.Stdout)
			}
			if got := result.Stderr; got != tt.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}

func mktempRuntimePath(name string) string {
	if strings.HasPrefix(name, "/") {
		return path.Clean(name)
	}
	return path.Clean(path.Join(defaultHomeDir, name))
}

func TestReadlinkExistingFailsForRemovedWorkingDirectory(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result := mustExecSession(t, session, "mkdir removed\ncd removed\nrmdir ../removed\nreadlink -e .\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("output = (%q, %q), want empty output", result.Stdout, result.Stderr)
	}
}

func TestLNHardLinkSharesContent(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo original > /home/agent/src.txt\nln /home/agent/src.txt /home/agent/dst.txt\necho updated > /home/agent/dst.txt\ncat /home/agent/src.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "updated\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLNAcceptsGroupedShortSymlinkFlags(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "ln -s target1 /home/agent/link\nln -nsf target2 /home/agent/link\nreadlink /home/agent/link\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "target2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLinkHardLinkSharesContent(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo original > /home/agent/src.txt\nlink /home/agent/src.txt /home/agent/dst.txt\necho updated > /home/agent/dst.txt\ncat /home/agent/src.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "updated\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLinkReportsMissingSource(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "link /home/agent/missing.txt /home/agent/dst.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "cannot create link") || !strings.Contains(result.Stderr, "No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-source error", result.Stderr)
	}
}

func TestChmodSupportsOctalAndSymbolicModes(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nchmod 755 /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\nchmod g-w,u+x /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %v, want 2 lines", lines)
	}
	if got, want := lines[0], "0755 -rwxr-xr-x"; got != want {
		t.Fatalf("First stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "0755 -rwxr-xr-x"; got != want {
		t.Fatalf("Second stat = %q, want %q", got, want)
	}
}

func TestChmodSupportsRecursiveMode(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho hi > /home/agent/dir/sub/file.txt\nchmod -R 700 /home/agent/dir\nstat -c '%a' /home/agent/dir/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "0700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChownSupportsNamedAndNumericOwners(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nstat -c '%u:%g:%U:%G' /home/agent/file.txt\nchown 123:456 /home/agent/file.txt\nstat -c '%u:%g:%U:%G' /home/agent/file.txt\nchown agent:agent /home/agent/file.txt\nstat -c '%u:%g:%U:%G' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %v, want 3", lines)
	}
	if got, want := lines[0], "1000:1000:agent:agent"; got != want {
		t.Fatalf("initial stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "123:456:123:456"; got != want {
		t.Fatalf("numeric chown stat = %q, want %q", got, want)
	}
	if got, want := lines[2], "1000:1000:agent:agent"; got != want {
		t.Fatalf("named chown stat = %q, want %q", got, want)
	}
}

func TestChownSupportsZeroOwnerIDs(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/root.txt\nchown 0:0 /home/agent/root.txt\nstat -c '%u:%g:%U:%G' /home/agent/root.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "0:0:0:0"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChownSupportsReferenceFromAndRecursiveFlags(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/tree/sub\necho ref > /home/agent/ref.txt\necho one > /home/agent/tree/file.txt\necho two > /home/agent/tree/sub/file.txt\nchown 41:42 /home/agent/ref.txt\nchown --from=1000:1000 --reference=/home/agent/ref.txt /home/agent/tree/file.txt\nchown --from=7:8 99:100 /home/agent/tree/file.txt\nchown -R 51:52 /home/agent/tree\nstat -c '%u:%g' /home/agent/tree/file.txt\nstat -c '%u:%g' /home/agent/tree/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %v, want 2", lines)
	}
	for idx, line := range lines {
		if got, want := line, "51:52"; got != want {
			t.Fatalf("line %d = %q, want %q", idx, got, want)
		}
	}
}

func TestChownNoDereferenceTargetsTheSymlink(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo data > /home/agent/target.txt\nln -s target.txt /home/agent/link.txt\nchown 61:62 /home/agent/link.txt\nstat -c '%u:%g' /home/agent/target.txt\nstat -c '%u:%g %F' /home/agent/link.txt\nchown -h 71:72 /home/agent/link.txt\nstat -c '%u:%g' /home/agent/target.txt\nstat -c '%u:%g %F' /home/agent/link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("Stdout lines = %v, want 4", lines)
	}
	if got, want := lines[0], "61:62"; got != want {
		t.Fatalf("target after dereference = %q, want %q", got, want)
	}
	if got, want := lines[1], "1000:1000 symbolic link"; got != want {
		t.Fatalf("link before -h = %q, want %q", got, want)
	}
	if got, want := lines[2], "61:62"; got != want {
		t.Fatalf("target after -h = %q, want %q", got, want)
	}
	if got, want := lines[3], "71:72 symbolic link"; got != want {
		t.Fatalf("link after -h = %q, want %q", got, want)
	}
}

func TestChownDoesNotChangeModTime(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/mtime.txt", []byte("hello\n"))

	before, err := session.FileSystem().Stat(context.Background(), "/home/agent/mtime.txt")
	if err != nil {
		t.Fatalf("Stat(before) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	result := mustExecSession(t, session, "chown 123:456 /home/agent/mtime.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	after, err := session.FileSystem().Stat(context.Background(), "/home/agent/mtime.txt")
	if err != nil {
		t.Fatalf("Stat(after) error = %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("ModTime after chown = %v, want unchanged from %v", after.ModTime(), before.ModTime())
	}
}

func TestStatFormatsMultipleFilesAndContinuesOnError(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/one.txt", []byte("hello"))

	result := mustExecSession(t, session, "stat -c '%n %s %F' /home/agent/one.txt /home/agent/missing\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := strings.TrimSpace(result.Stdout), "/home/agent/one.txt 5 regular file"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "missing") {
		t.Fatalf("Stderr = %q, want missing-file message", result.Stderr)
	}
}

func TestStatFormatsDeviceAndInodePlaceholders(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/one.txt", []byte("hello"))

	result := mustExecSession(t, session, "stat -c '%d:%i' /home/agent/one.txt /\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0:0\n0:0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasenameAndDirnameHandleSuffixesAndMultipleOperands(t *testing.T) {
	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename -a -s .txt /tmp/a.txt /tmp/b.txt\ndirname /tmp/a.txt plain /root/\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n/tmp\n.\n/\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDirnameMatchesGNUStringSemanticsAndZeroTerminator(t *testing.T) {
	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "dirname '///a///b' '///a//b/' 'foo/.'\ndirname -z '///a///b' ''\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "///a\n///a\nfoo\n///a\x00.\x00"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDirnameMissingOperandIncludesHelpHint(t *testing.T) {
	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "dirname\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stderr, "dirname: missing operand\nTry 'dirname --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBasenameSupportsLongSuffixFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename --suffix .log /tmp/build.log\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "build\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTreeShowsHiddenFilesAndDepthLimits(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho hi > /home/agent/dir/file.txt\necho hidden > /home/agent/dir/.secret\necho nested > /home/agent/dir/sub/nested.txt\ntree -L 1 /home/agent/dir\necho ---\ntree -a -L 1 /home/agent/dir\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("tree output blocks = %q, want 2 parts", result.Stdout)
	}
	if strings.Contains(parts[0], ".secret") || strings.Contains(parts[0], "nested.txt") {
		t.Fatalf("first tree block = %q, want hidden and depth-limited entries omitted", parts[0])
	}
	if !strings.Contains(parts[1], ".secret") {
		t.Fatalf("second tree block = %q, want hidden file", parts[1])
	}
}

func TestDUReportsSummaryAllEntriesAndTotals(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho -n hello > /home/agent/dir/a.txt\necho -n world!! > /home/agent/dir/sub/b.txt\ndu -a /home/agent/dir\ndu -s -c /home/agent/dir /home/agent/dir/sub\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "/home/agent/dir/a.txt") || !strings.Contains(result.Stdout, "/home/agent/dir/sub/b.txt") {
		t.Fatalf("Stdout = %q, want file entries", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "total") {
		t.Fatalf("Stdout = %q, want grand total", result.Stdout)
	}
}

func TestFileDetectsMagicTextAndDirectories(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/image.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00})
	writeSessionFile(t, session, "/home/agent/script.sh", []byte("#!/bin/sh\necho hi\n"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/home/agent/docs", 0o755); err != nil {
		t.Fatalf("MkdirAll(docs) error = %v", err)
	}

	result := mustExecSession(t, session, "file /home/agent/image.png\nfile -i /home/agent/script.sh\nfile -b /home/agent/docs\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %v, want 3 lines", lines)
	}
	if !strings.Contains(lines[0], "PNG image data") {
		t.Fatalf("line 1 = %q, want PNG detection", lines[0])
	}
	if got, want := lines[1], "/home/agent/script.sh: text/x-shellscript"; got != want {
		t.Fatalf("line 2 = %q, want %q", got, want)
	}
	if got, want := lines[2], "directory"; got != want {
		t.Fatalf("line 3 = %q, want %q", got, want)
	}
}

func TestFileMissingPathReportsErrorOnStdout(t *testing.T) {
	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "file /missing\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "cannot open") {
		t.Fatalf("Stdout = %q, want missing-path message", result.Stdout)
	}
}

func TestFileSupportsLongBriefAndMimeFlags(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/note.txt", []byte("hello\n"))

	result := mustExecSession(t, session, "file --brief /home/agent/note.txt\nfile --mime /home/agent/note.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "ASCII text\n/home/agent/note.txt: text/plain\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestOverlayFactorySupportsHardLinksAndMetadataCopyUp(t *testing.T) {
	rt := newRuntime(t, &Config{
		FileSystem: CustomFileSystem(
			gbfs.Overlay(seededFSFactory{files: map[string]string{
				"/seed.txt": "seed\n",
			}}),
			defaultHomeDir,
		),
	})

	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	result := mustExecSession(t, session, "ln /seed.txt /copy.txt\nchmod 700 /seed.txt\nstat -c '%a' /seed.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "0700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := readTestFSFile(t, session.FileSystem(), "/copy.txt"); got != "seed\n" {
		t.Fatalf("copy.txt content = %q, want seed", got)
	}
}

func readTestFSFile(t *testing.T, fsys gbfs.FileSystem, name string) string {
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

func TestTouchDateParsingUsesStableTimestamp(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "touch --date=2024/03/04 /home/agent/date.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	info, err := session.FileSystem().Stat(context.Background(), "/home/agent/date.txt")
	if err != nil {
		t.Fatalf("Stat(date.txt) error = %v", err)
	}
	if got, want := info.ModTime().UTC(), time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("ModTime = %v, want %v", got, want)
	}
}
