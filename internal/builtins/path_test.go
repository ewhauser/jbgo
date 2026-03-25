package builtins_test

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
	t.Parallel()
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
	t.Parallel()
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

func TestTouchDateWithoutFileMatchesConformanceOracle(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "touch -d 2024/01/02 > /tmp/out\nprintf 'status=%s\\n' \"$?\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "touch: missing file operand\nTry 'touch --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestTouchValidatesDateBeforeMissingFileOperand(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "touch -d invalid\n")
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stderr, "touch: invalid date format \"invalid\"\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestMkfifoCreatesNamedPipesAndSupportsReadWriteRedirects(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "umask 027\nmkfifo /home/agent/default.pipe\nmkfifo -m 600 /home/agent/explicit.pipe\nexec 8<> /home/agent/default.pipe\necho first >&8\necho second >&8\nread line1 <&8\nread line2 <&8\nexec 8>&-\nprintf 'line1=%s line2=%s\\n' \"$line1\" \"$line2\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "line1=first line2=second\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	defaultInfo, err := session.FileSystem().Lstat(context.Background(), "/home/agent/default.pipe")
	if err != nil {
		t.Fatalf("Lstat(default.pipe) error = %v", err)
	}
	if defaultInfo.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("default.pipe mode = %v, want named pipe", defaultInfo.Mode())
	}
	if got, want := defaultInfo.Mode().Perm(), os.FileMode(0o640); got != want {
		t.Fatalf("default.pipe perm = %o, want %o", got, want)
	}

	explicitInfo, err := session.FileSystem().Lstat(context.Background(), "/home/agent/explicit.pipe")
	if err != nil {
		t.Fatalf("Lstat(explicit.pipe) error = %v", err)
	}
	if explicitInfo.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("explicit.pipe mode = %v, want named pipe", explicitInfo.Mode())
	}
	if got, want := explicitInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("explicit.pipe perm = %o, want %o", got, want)
	}
}

func TestMkfifoReportsExistingPathsAndInvalidModes(t *testing.T) {
	t.Parallel()

	t.Run("existing target", func(t *testing.T) {
		t.Parallel()
		session := newSession(t, &Config{})

		result := mustExecSession(t, session, "touch /home/agent/existing\nmkfifo /home/agent/existing\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "mkfifo: cannot create fifo '/home/agent/existing': File exists") {
			t.Fatalf("Stderr = %q, want existing-path error", result.Stderr)
		}
	})

	t.Run("invalid mode bits", func(t *testing.T) {
		t.Parallel()
		session := newSession(t, &Config{})

		result := mustExecSession(t, session, "mkfifo -m 1777 /home/agent/pipe\n")
		if result.ExitCode == 0 {
			t.Fatalf("ExitCode = 0, want non-zero")
		}
		if !strings.Contains(result.Stderr, "mkfifo: mode must specify only file permission bits") {
			t.Fatalf("Stderr = %q, want mode-bits error", result.Stderr)
		}
	})
}

func TestTruncateCreatesAndResizesFiles(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	t.Run("missing file operand", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestRealpathModesAndFormatting(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	script := strings.Join([]string{
		"echo hi > file",
		"mkdir -p dir1 dir2/bar",
		"ln -s file link-file",
		"ln -s ../dir2/bar dir1/phys",
		"ln -s ../dir2 dir1/log",
		"realpath file",
		"realpath link-file",
		"realpath -z file link-file",
		"realpath -s link-file",
		"realpath --strip link-file",
		"realpath --no-symlinks link-file",
		"realpath -P dir1/phys/..",
		"realpath -L dir1/log/..",
		"",
	}, "\n")

	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "/home/agent/file\n" +
		"/home/agent/file\n" +
		"/home/agent/file\x00/home/agent/file\x00" +
		"/home/agent/link-file\n" +
		"/home/agent/link-file\n" +
		"/home/agent/link-file\n" +
		"/home/agent/dir2\n" +
		"/home/agent/dir1\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRealpathCanonicalizeAndRelativeOptions(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	script := strings.Join([]string{
		"mkdir -p root/sub existing_dir",
		"touch root/file other",
		"realpath existing_dir/missing",
		"realpath -E existing_dir/missing",
		"realpath --canonicalize existing_dir/missing",
		"realpath -e existing_dir",
		"realpath --canonicalize-existing existing_dir",
		"realpath -m missing/path",
		"realpath --canonicalize-missing missing/other",
		"realpath -e -E existing_dir/missing",
		"realpath -sm --relative-base=/home/agent/root --relative-to=/home/agent/root /home/agent/other /home/agent/root",
		"realpath -sm --relative-base=/home/agent/root/sub --relative-to=/home/agent/root /home/agent/root/file",
		"realpath -sm --relative-base=/home/agent/root /home/agent/root/file",
		"realpath -m --relative-to=prefix prefixed/1",
		"",
	}, "\n")

	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := strings.Join([]string{
		"/home/agent/existing_dir/missing",
		"/home/agent/existing_dir/missing",
		"/home/agent/existing_dir/missing",
		"/home/agent/existing_dir",
		"/home/agent/existing_dir",
		"/home/agent/missing/path",
		"/home/agent/missing/other",
		"/home/agent/existing_dir/missing",
		"/home/agent/other",
		".",
		"/home/agent/root/file",
		"file",
		"../prefixed/1",
		"",
	}, "\n")
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRealpathErrorsContinueAndQuiet(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	setup := mustExecSession(t, session, strings.Join([]string{
		"mkdir -p dir1 dir2",
		"touch dir2/bar",
		"ln -s ../dir2/bar dir1/foo1",
		"ln -s /dir2/bar dir1/foo2",
		"ln -s ../dir2/baz dir1/foo3",
		"",
	}, "\n"))
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
	}

	result := mustExecSession(t, session, "realpath dir1/foo1 dir1/foo2 dir1/foo3\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "/home/agent/dir2/bar\n/home/agent/dir2/baz\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "realpath: dir1/foo2: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}

	quiet := mustExecSession(t, session, "realpath -q dir1/foo1 dir1/foo2\n")
	if quiet.ExitCode != 1 {
		t.Fatalf("quiet ExitCode = %d, want 1; stdout=%q stderr=%q", quiet.ExitCode, quiet.Stdout, quiet.Stderr)
	}
	if got, want := quiet.Stdout, "/home/agent/dir2/bar\n"; got != want {
		t.Fatalf("quiet stdout = %q, want %q", got, want)
	}
	if quiet.Stderr != "" {
		t.Fatalf("quiet stderr = %q, want empty", quiet.Stderr)
	}
}

func TestRealpathTrailingSlashAndRelativeDirectoryChecks(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	setup := mustExecSession(t, session, strings.Join([]string{
		"mkdir -p dir dir1",
		"touch file dir1/f",
		"ln -s file link_file",
		"ln -s dir link_dir",
		"ln -s no_dir link_no_dir",
		"",
	}, "\n"))
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
	}

	fileLink := mustExecSession(t, session, "realpath link_file/\n")
	if fileLink.ExitCode != 1 {
		t.Fatalf("fileLink ExitCode = %d, want 1; stdout=%q stderr=%q", fileLink.ExitCode, fileLink.Stdout, fileLink.Stderr)
	}
	if got, want := fileLink.Stderr, "realpath: link_file/: Not a directory\n"; got != want {
		t.Fatalf("fileLink stderr = %q, want %q", got, want)
	}

	dirLink := mustExecSession(t, session, "realpath link_dir/\n")
	if dirLink.ExitCode != 0 {
		t.Fatalf("dirLink ExitCode = %d, want 0; stderr=%q", dirLink.ExitCode, dirLink.Stderr)
	}
	if got, want := dirLink.Stdout, "/home/agent/dir\n"; got != want {
		t.Fatalf("dirLink stdout = %q, want %q", got, want)
	}

	missingLink := mustExecSession(t, session, "realpath link_no_dir/\n")
	if missingLink.ExitCode != 0 {
		t.Fatalf("missingLink ExitCode = %d, want 0; stderr=%q", missingLink.ExitCode, missingLink.Stderr)
	}
	if got, want := missingLink.Stdout, "/home/agent/no_dir\n"; got != want {
		t.Fatalf("missingLink stdout = %q, want %q", got, want)
	}

	missingMode := mustExecSession(t, session, "realpath -m link_file/\n")
	if missingMode.ExitCode != 0 {
		t.Fatalf("missingMode ExitCode = %d, want 0; stderr=%q", missingMode.ExitCode, missingMode.Stderr)
	}
	if got, want := missingMode.Stdout, "/home/agent/file\n"; got != want {
		t.Fatalf("missingMode stdout = %q, want %q", got, want)
	}

	relativeDir := mustExecSession(t, session, "realpath -e --relative-base=. --relative-to=dir1/f .\n")
	if relativeDir.ExitCode != 1 {
		t.Fatalf("relativeDir ExitCode = %d, want 1; stdout=%q stderr=%q", relativeDir.ExitCode, relativeDir.Stdout, relativeDir.Stderr)
	}
	if !strings.Contains(relativeDir.Stderr, "Not a directory") {
		t.Fatalf("relativeDir stderr = %q, want directory diagnostic", relativeDir.Stderr)
	}

	relativeDirOK := mustExecSession(t, session, "realpath -e --relative-base=. --relative-to=dir1 .\n")
	if relativeDirOK.ExitCode != 0 {
		t.Fatalf("relativeDirOK ExitCode = %d, want 0; stderr=%q", relativeDirOK.ExitCode, relativeDirOK.Stderr)
	}
	if got, want := relativeDirOK.Stdout, "..\n"; got != want {
		t.Fatalf("relativeDirOK stdout = %q, want %q", got, want)
	}
}

func TestRealpathRejectsEmptyOperandsAndSupportsVersion(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	emptyOperand := mustExecSession(t, session, "realpath ''\n")
	if emptyOperand.ExitCode != 1 {
		t.Fatalf("emptyOperand ExitCode = %d, want 1; stdout=%q stderr=%q", emptyOperand.ExitCode, emptyOperand.Stdout, emptyOperand.Stderr)
	}
	if got, want := emptyOperand.Stderr, "realpath: invalid operand: empty string\n"; got != want {
		t.Fatalf("emptyOperand stderr = %q, want %q", got, want)
	}

	emptyRelative := mustExecSession(t, session, "realpath --relative-base='' .\n")
	if emptyRelative.ExitCode != 1 {
		t.Fatalf("emptyRelative ExitCode = %d, want 1; stdout=%q stderr=%q", emptyRelative.ExitCode, emptyRelative.Stdout, emptyRelative.Stderr)
	}
	if got, want := emptyRelative.Stderr, "realpath: invalid operand: empty string\n"; got != want {
		t.Fatalf("emptyRelative stderr = %q, want %q", got, want)
	}

	version := mustExecSession(t, session, "realpath -V ignored\n")
	if version.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", version.ExitCode, version.Stderr)
	}
	if got, want := version.Stdout, "realpath (gbash)\n"; got != want {
		t.Fatalf("version stdout = %q, want %q", got, want)
	}
}

func TestMktempCreatesFilesAndDirectories(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "ln -s target1 /home/agent/link\nln -nsf target2 /home/agent/link\nreadlink /home/agent/link\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "target2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLNBackupCreatesSuffixedBackupAndReplacesDestination(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, strings.Join([]string{
		"printf 'fresh\\n' > /tmp/src.txt",
		"printf 'stale\\n' > /tmp/dst.txt",
		"ln --backup=simple --suffix=.bak /tmp/src.txt /tmp/dst.txt",
		"printf 'updated\\n' > /tmp/dst.txt",
		"cat /tmp/src.txt",
		"cat /tmp/dst.txt.bak",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "updated\nstale\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLNBackupReportsSameFileBeforeReplacingDestination(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'payload\\n' > /tmp/file.txt\nln --backup /tmp/file.txt /tmp/file.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "ln: '/tmp/file.txt' and '/tmp/file.txt' are the same file\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/file.txt")), "payload\n"; got != want {
		t.Fatalf("file contents = %q, want %q", got, want)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/tmp/file.txt~"); err == nil {
		t.Fatalf("Stat(backup) succeeded, want no backup created")
	}
}

func TestLNNoTargetDirectoryRejectsTrailingSlashDestination(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'payload\\n' > /tmp/src.txt\nln -T /tmp/src.txt /tmp/no-such-file/\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/tmp/no-such-file"); err == nil {
		t.Fatalf("Stat(cleaned destination) succeeded, want no file created")
	}
}

func TestLNNoTargetDirectoryExistingDirectorySlashReportsFileExists(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'payload\\n' > /tmp/src.txt\nmkdir /tmp/existing-dir\nln -T /tmp/src.txt /tmp/existing-dir/\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "ln: failed to create link '/tmp/existing-dir/': File exists\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestLNNoDereferenceTreatsTrailingSlashSymlinkDestAsDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result := mustExecSession(t, session, strings.Join([]string{
		"printf 'payload\\n' > /tmp/src.txt",
		"mkdir /tmp/real-dir",
		"ln -s real-dir /tmp/link-dir",
		"ln -ns /tmp/src.txt /tmp/link-dir/",
		"readlink /tmp/real-dir/src.txt",
		"readlink /tmp/link-dir",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/src.txt\nreal-dir\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLNSymbolicForceRejectsIdenticalRegularFileSourceAndDestination(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'payload\\n' > /tmp/file.txt\nln -sf /tmp/file.txt /tmp/file.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "ln: '/tmp/file.txt' and '/tmp/file.txt' are the same file\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/file.txt")), "payload\n"; got != want {
		t.Fatalf("file contents = %q, want %q", got, want)
	}
}

func TestLNSymbolicForceAllowsSelfRelinkOnExistingSymlink(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "ln -s target /tmp/link\nln -sf /tmp/link /tmp/link\nreadlink /tmp/link\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/link\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLNSymbolicBackupAllowsSelfRelinkOnRegularFile(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, strings.Join([]string{
		"printf 'payload\\n' > /tmp/file.txt",
		"ln -s --backup /tmp/file.txt /tmp/file.txt",
		"readlink /tmp/file.txt",
		"cat /tmp/file.txt~",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/file.txt\npayload\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLNNoDereferenceReplacesSymlinkToDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, strings.Join([]string{
		"printf 'payload\\n' > /tmp/target.txt",
		"mkdir /tmp/real-dir",
		"ln -s real-dir /tmp/link-dir",
		"ln --no-dereference -fs /tmp/target.txt /tmp/link-dir",
		"readlink /tmp/link-dir",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/target.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	info, err := session.FileSystem().Lstat(context.Background(), "/tmp/link-dir")
	if err != nil {
		t.Fatalf("Lstat(link-dir) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Lstat(link-dir).Mode() = %v, want symlink", info.Mode())
	}
}

func TestLinkHardLinkSharesContent(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "link /home/agent/missing.txt /home/agent/dst.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "cannot create link") || !strings.Contains(result.Stderr, "No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-source error", result.Stderr)
	}
}

func TestUnlinkRemovesFile(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/home/agent/file.txt", []byte("data\n"))

	result := mustExecSession(t, session, "unlink /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("output = stdout %q stderr %q, want empty", result.Stdout, result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/file.txt"); !os.IsNotExist(err) {
		t.Fatalf("Stat(file.txt) error = %v, want not exist", err)
	}
}

func TestUnlinkRemovesSymlinkOnly(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo target > /home/agent/target.txt\nln -s /home/agent/target.txt /home/agent/link.txt\nunlink /home/agent/link.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/target.txt"); err != nil {
		t.Fatalf("Stat(target.txt) error = %v, want present", err)
	}
	if _, err := session.FileSystem().Lstat(context.Background(), "/home/agent/link.txt"); !os.IsNotExist(err) {
		t.Fatalf("Lstat(link.txt) error = %v, want not exist", err)
	}
}

func TestUnlinkSupportsDashedPaths(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo data > /home/agent/--dash\nunlink -- --dash\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/--dash"); !os.IsNotExist(err) {
		t.Fatalf("Stat(--dash) error = %v, want not exist", err)
	}
}

func TestUnlinkHelpAndVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		script     string
		wantStdout string
	}{
		{
			name:       "short help",
			script:     "unlink -h\n",
			wantStdout: "Unlink the file at FILE.\n\nUsage: unlink FILE\n       unlink OPTION\n\nOptions:\n  -h, --help     Print help\n  -V, --version  Print version\n",
		},
		{
			name:       "inferred long help",
			script:     "unlink --he\n",
			wantStdout: "Unlink the file at FILE.\n\nUsage: unlink FILE\n       unlink OPTION\n\nOptions:\n  -h, --help     Print help\n  -V, --version  Print version\n",
		},
		{
			name:       "short version",
			script:     "unlink -V\n",
			wantStdout: "unlink (gbash) dev\n",
		},
		{
			name:       "inferred long version",
			script:     "unlink --ver\n",
			wantStdout: "unlink (gbash) dev\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})

			result := mustExecSession(t, session, tt.script)
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tt.wantStdout {
				t.Fatalf("Stdout = %q, want %q", got, tt.wantStdout)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestUnlinkUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		script     string
		wantStderr string
	}{
		{
			name:       "missing operand",
			script:     "unlink\n",
			wantStderr: "error: the following required arguments were not provided:\n  <FILE>\n\nUsage: unlink FILE\n       unlink OPTION\n\nFor more information, try '--help'.\n",
		},
		{
			name:       "extra operand",
			script:     "unlink first second\n",
			wantStderr: "error: unexpected argument 'second' found\n\nUsage: unlink FILE\n       unlink OPTION\n\nFor more information, try '--help'.\n",
		},
		{
			name:       "invalid option",
			script:     "unlink --definitely-invalid\n",
			wantStderr: "error: unexpected argument '--definitely-invalid' found\n\n  tip: to pass '--definitely-invalid' as a value, use '-- --definitely-invalid'\n\nUsage: unlink FILE\n       unlink OPTION\n\nFor more information, try '--help'.\n",
		},
		{
			name:       "unexpected option value",
			script:     "unlink --version=1\n",
			wantStderr: "error: unexpected value '1' for '--version' found; no more were expected\n\nUsage: unlink FILE\n       unlink OPTION\n\nFor more information, try '--help'.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})

			result := mustExecSession(t, session, tt.script)
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
			}
			if result.Stdout != "" {
				t.Fatalf("Stdout = %q, want empty", result.Stdout)
			}
			if got := result.Stderr; got != tt.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}

func TestUnlinkReportsPathErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		setup      string
		script     string
		wantStderr string
	}{
		{
			name:       "directory",
			setup:      "mkdir /home/agent/dir\n",
			script:     "unlink /home/agent/dir\n",
			wantStderr: "unlink: cannot unlink '/home/agent/dir': Is a directory\n",
		},
		{
			name:       "missing path",
			script:     "unlink /home/agent/missing.txt\n",
			wantStderr: "unlink: cannot unlink '/home/agent/missing.txt': No such file or directory\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})

			if tt.setup != "" {
				setup := mustExecSession(t, session, tt.setup)
				if setup.ExitCode != 0 {
					t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
				}
			}

			result := mustExecSession(t, session, tt.script)
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
			}
			if result.Stdout != "" {
				t.Fatalf("Stdout = %q, want empty", result.Stdout)
			}
			if got := result.Stderr; got != tt.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}

func TestChmodSupportsOctalAndSymbolicModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nchmod 755 /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\nchmod g-w,u+x /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %v, want 2 lines", lines)
	}
	if got, want := lines[0], "755 -rwxr-xr-x"; got != want {
		t.Fatalf("First stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "755 -rwxr-xr-x"; got != want {
		t.Fatalf("Second stat = %q, want %q", got, want)
	}
}

func TestChmodSymbolicModeUsesSandboxUmask(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nchmod 444 /home/agent/file.txt\nchmod +w /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "644 -rw-r--r--"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChmodSymbolicEqualsHonorsShellUmask(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "umask 005\necho hi > /home/agent/file.txt\nchmod 644 /home/agent/file.txt\nchmod a=r,=x /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "110 ---x--x---"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChmodSupportsNegativeModeOperands(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nchmod 644 /home/agent/file.txt\nchmod -w /home/agent/file.txt\nstat -c '%a' /home/agent/file.txt\nchmod -w -w /home/agent/file.txt\nstat -c '%a' /home/agent/file.txt\necho hi > /home/agent/later.txt\nchmod 644 /home/agent/later.txt\nchmod /home/agent/later.txt -w\nstat -c '%a' /home/agent/later.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %v, want 3", lines)
	}
	if got, want := lines[0], "444"; got != want {
		t.Fatalf("first stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "444"; got != want {
		t.Fatalf("second stat = %q, want %q", got, want)
	}
	if got, want := lines[2], "444"; got != want {
		t.Fatalf("third stat = %q, want %q", got, want)
	}
}

func TestChmodReportsPartialImplicitWhoApplication(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nchmod 755 /home/agent/file.txt\numask 077\nchmod -x /home/agent/file.txt\nprintf 'status=%s\\n' \"$?\"\nstat -c '%a %A' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %v, want 2", lines)
	}
	if got, want := lines[0], "status=1"; got != want {
		t.Fatalf("status line = %q, want %q", got, want)
	}
	if got, want := lines[1], "655 -rw-r-xr-x"; got != want {
		t.Fatalf("stat line = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "new permissions are rw-r-xr-x, not rw-r--r--") {
		t.Fatalf("Stderr = %q, want partial-application diagnostic", result.Stderr)
	}
}

func TestChmodQuietSuppressesMissingFileDiagnostics(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "chmod -f 0 /home/agent/missing\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestChmodDanglingSymlinkMatchesGNUDiagnostic(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "ln -s missing /home/agent/dangle\nchmod 644 /home/agent/dangle\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := strings.TrimSpace(result.Stderr), "chmod: cannot operate on dangling symlink '/home/agent/dangle'"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestChmodRecursiveDefaultsToTraverseFirstOnCLIArgs(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /home/agent/a\nln -s missing /home/agent/a/dangle\nchmod 755 -R /home/agent/a/dangle\nprintf 'status=%s\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "status=1"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "cannot operate on dangling symlink '/home/agent/a/dangle'") {
		t.Fatalf("Stderr = %q, want dangling-symlink diagnostic", result.Stderr)
	}
}

func TestChmodSupportsRecursiveMode(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho hi > /home/agent/dir/sub/file.txt\nchmod -R 700 /home/agent/dir\nstat -c '%a' /home/agent/dir/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChmodAcceptsRecursiveOptionAfterMode(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho hi > /home/agent/dir/sub/file.txt\nchmod 444 /home/agent/dir/sub/file.txt\nchmod u+w -R /home/agent/dir\nstat -c '%a' /home/agent/dir/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "644"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChmodSupportsDoubleDashModeAndFiles(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo one > /home/agent/--\necho two > /home/agent/file.txt\nchmod -- -- /home/agent/file.txt\nstat -c '%a' /home/agent/--\nstat -c '%a' /home/agent/file.txt\nchmod -w -- /home/agent/-- /home/agent/file.txt\nstat -c '%a' /home/agent/--\nstat -c '%a' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("Stdout lines = %v, want 4", lines)
	}
	if got, want := lines[0], "644"; got != want {
		t.Fatalf("first stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "644"; got != want {
		t.Fatalf("second stat = %q, want %q", got, want)
	}
	if got, want := lines[2], "444"; got != want {
		t.Fatalf("third stat = %q, want %q", got, want)
	}
	if got, want := lines[3], "444"; got != want {
		t.Fatalf("fourth stat = %q, want %q", got, want)
	}
}

func TestChmodRejectsMixedCopyAndLiteralPermissions(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nchmod u+gr /home/agent/file.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "chmod: invalid mode: /home/agent/file.txt") {
		t.Fatalf("Stderr = %q, want invalid-mode diagnostic", result.Stderr)
	}
}

func TestChownSupportsNamedAndNumericOwners(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestChgrpSupportsNamedNumericAndColonGroups(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nstat -c '%g:%G' /home/agent/file.txt\nchgrp 456 /home/agent/file.txt\nstat -c '%g:%G' /home/agent/file.txt\nchgrp agent /home/agent/file.txt\nstat -c '%g:%G' /home/agent/file.txt\nchgrp :789 /home/agent/file.txt\nstat -c '%g:%G' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("Stdout lines = %v, want 4", lines)
	}
	if got, want := lines[0], "1000:agent"; got != want {
		t.Fatalf("initial stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "456:456"; got != want {
		t.Fatalf("numeric chgrp stat = %q, want %q", got, want)
	}
	if got, want := lines[2], "1000:agent"; got != want {
		t.Fatalf("named chgrp stat = %q, want %q", got, want)
	}
	if got, want := lines[3], "789:789"; got != want {
		t.Fatalf("colon chgrp stat = %q, want %q", got, want)
	}
}

func TestChgrpAndChownQuietSuppressMissingFileDiagnostics(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "chgrp -f 0 /home/agent/missing\nchown -f 0:0 /home/agent/missing\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestChownDereferenceRejectsDanglingSymlink(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "cd /home/agent\nln -s missing dangle\nchown --dereference 123 dangle\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := strings.TrimSpace(result.Stderr), "chown: cannot dereference 'dangle': No such file or directory"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestChownPreserveRootSkipsUntraversedChildSymlinkAndFormatsRootMessage(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result := mustExecSession(t, session, "mkdir /home/agent/d\nln -s / /home/agent/d/slink-to-root\ncd /home/agent\nchown -RHh --preserve-root 1000 d\nprintf 'first=%s\\n' \"$?\"\nchown -RLh --preserve-root 1000 d\nprintf 'second=%s\\n' \"$?\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "first=0\nsecond=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	wantErr := "chown: it is dangerous to operate recursively on 'd/slink-to-root' (same as '/')\nchown: use --no-preserve-root to override this failsafe\n"
	if got := result.Stderr; got != wantErr {
		t.Fatalf("Stderr = %q, want %q", got, wantErr)
	}
}
func TestChgrpSupportsReferenceFromRecursiveAndTrailingReference(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/tree/sub\necho ref > /home/agent/ref.txt\necho one > /home/agent/tree/file1.txt\necho two > /home/agent/tree/file2.txt\necho three > /home/agent/tree/sub/file3.txt\nchgrp 41 /home/agent/ref.txt\nchgrp /home/agent/tree/file1.txt /home/agent/tree/file2.txt --reference /home/agent/ref.txt\nchgrp --from=41 52 /home/agent/tree/file1.txt\nchgrp --from=:41 :53 /home/agent/tree/file2.txt\nchgrp -R 61 /home/agent/tree/sub\nstat -c '%g' /home/agent/tree/file1.txt\nstat -c '%g' /home/agent/tree/file2.txt\nstat -c '%g' /home/agent/tree/sub/file3.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %v, want 3", lines)
	}
	if got, want := lines[0], "52"; got != want {
		t.Fatalf("file1 gid = %q, want %q", got, want)
	}
	if got, want := lines[1], "53"; got != want {
		t.Fatalf("file2 gid = %q, want %q", got, want)
	}
	if got, want := lines[2], "61"; got != want {
		t.Fatalf("file3 gid = %q, want %q", got, want)
	}
}

func TestChgrpNoDereferenceTargetsTheSymlink(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo data > /home/agent/target.txt\nln -s target.txt /home/agent/link.txt\nchgrp 61 /home/agent/link.txt\nstat -c '%g' /home/agent/target.txt\nstat -c '%g %F' /home/agent/link.txt\nchgrp -h 71 /home/agent/link.txt\nstat -c '%g' /home/agent/target.txt\nstat -c '%g %F' /home/agent/link.txt\n",
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
	if got, want := lines[0], "61"; got != want {
		t.Fatalf("target after dereference = %q, want %q", got, want)
	}
	if got, want := lines[1], "1000 symbolic link"; got != want {
		t.Fatalf("link before -h = %q, want %q", got, want)
	}
	if got, want := lines[2], "61"; got != want {
		t.Fatalf("target after -h = %q, want %q", got, want)
	}
	if got, want := lines[3], "71 symbolic link"; got != want {
		t.Fatalf("link after -h = %q, want %q", got, want)
	}
}

func TestChgrpInfersLongOptionsAfterPositionals(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo ref > /home/agent/ref.txt\necho target > /home/agent/target.txt\nchgrp 41 /home/agent/ref.txt\nchgrp --verb /home/agent/target.txt --ref=/home/agent/ref.txt\nstat -c '%g' /home/agent/target.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "41"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "changed group of") {
		t.Fatalf("Stderr = %q, want verbose group-change message", result.Stderr)
	}
}

func TestChgrpPreserveRootUsesCommandName(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "chgrp --preserve-root -R 123 /\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "chgrp: it is dangerous to operate recursively on") {
		t.Fatalf("Stderr = %q, want chgrp preserve-root diagnostic", result.Stderr)
	}
	if strings.Contains(result.Stderr, "chown:") {
		t.Fatalf("Stderr = %q, want no chown-prefixed diagnostic", result.Stderr)
	}
}

func TestStatFormatsMultipleFilesAndContinuesOnError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestStatSupportsPrintfFormat(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/target.txt", []byte("hello"))

	result := mustExecSession(t, session, "stat --printf='[%n]\\n' /home/agent/target.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "[/home/agent/target.txt]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasenameAndDirnameHandleSuffixesAndMultipleOperands(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	if got, want := strings.TrimSpace(result.Stdout), "700"; got != want {
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
	t.Parallel()
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
