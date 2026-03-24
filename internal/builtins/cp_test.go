package builtins_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/policy"
)

func TestCPSupportsParityFlagsIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"echo old > /tmp/dst.txt\n" +
			"cp --no-clobber --preserve --verbose /tmp/src.txt /tmp/dst.txt\n" +
			"cat /tmp/dst.txt\n" +
			"cp -p -v /tmp/src.txt /tmp/fresh.txt\n" +
			"cat /tmp/fresh.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "old\n'/tmp/src.txt' -> '/tmp/fresh.txt'\nnew\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPAcceptsForceFlagForOverwrite(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"echo old > /tmp/dst.txt\n" +
			"cp -f /tmp/src.txt /tmp/dst.txt\n" +
			"cat /tmp/dst.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "new\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPNoDereferencePreservesSourceSymlink(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo payload > /tmp/target.txt\n" +
			"cd /tmp\n" +
			"ln -s target.txt src-link\n" +
			"cp -d /tmp/src-link /tmp/dst-link\n" +
			"readlink /tmp/dst-link\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "target.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPDereferenceCommandLineAppliesToAllSources(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/target1.txt\n" +
			"echo two > /tmp/target2.txt\n" +
			"cd /tmp\n" +
			"ln -s target1.txt link1\n" +
			"ln -s target2.txt link2\n" +
			"mkdir out\n" +
			"cp -H /tmp/link1 /tmp/link2 /tmp/out\n" +
			"test -L /tmp/out/link1 && echo link1-symlink || echo link1-regular\n" +
			"cat /tmp/out/link1\n" +
			"test -L /tmp/out/link2 && echo link2-symlink || echo link2-regular\n" +
			"cat /tmp/out/link2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "link1-regular\none\nlink2-regular\ntwo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPSymlinkCopyOverwritesExistingDestinationByDefault(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo payload > /tmp/target.txt\n" +
			"cd /tmp\n" +
			"ln -s target.txt src-link\n" +
			"echo old > dst-link\n" +
			"cp -P /tmp/src-link /tmp/dst-link\n" +
			"test -L /tmp/dst-link && echo symlink || echo regular\n" +
			"readlink /tmp/dst-link\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "symlink\ntarget.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPSupportsHardLinkModeAndSameFileNoop(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"cp -l /tmp/src.txt /tmp/hard.txt\n" +
			"cp -l /tmp/src.txt /tmp/src.txt\n" +
			"printf 'same=%s\\n' \"$?\"\n" +
			"stat -c '%d:%i' /tmp/src.txt /tmp/hard.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %q, want 3 lines", result.Stdout)
	}
	if got, want := lines[0], "same=0"; got != want {
		t.Fatalf("same-file status = %q, want %q", got, want)
	}
	if lines[1] != lines[2] {
		t.Fatalf("hard-link inode lines = %q and %q, want equal", lines[1], lines[2])
	}
}

func TestCPSupportsSymbolicLinkMode(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"cp -s /tmp/src.txt /tmp/dst.txt\n" +
			"test -L /tmp/dst.txt && echo symlink || echo regular\n" +
			"readlink /tmp/dst.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "symlink\n/tmp/src.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPSymbolicLinkModeNormalizesRelativeTargetsAcrossDirectories(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cd /tmp\n" +
			"echo new > src.txt\n" +
			"mkdir dir\n" +
			"cp -s src.txt dir/dst.txt\n" +
			"readlink /tmp/dir/dst.txt\n" +
			"cat /tmp/dir/dst.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "../src.txt\nnew\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPLinkModesStillUseDirectoryCopySemantics(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/src/sub\n" +
			"echo payload > /tmp/src/sub/file.txt\n" +
			"cp -l /tmp/src /tmp/hard-no-r\n" +
			"printf 'hard_no_r=%s\\n' \"$?\"\n" +
			"cp -l -R /tmp/src /tmp/hard-r\n" +
			"cat /tmp/hard-r/sub/file.txt\n" +
			"cp -s -R /tmp/src /tmp/sym-r\n" +
			"test -d /tmp/sym-r && echo sym-r-dir || echo sym-r-not-dir\n" +
			"cat /tmp/sym-r/sub/file.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hard_no_r=1\npayload\nsym-r-dir\npayload\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "cp: omitting directory \"/tmp/src\"") {
		t.Fatalf("Stderr = %q, want omitting-directory error", result.Stderr)
	}
}

func TestCPSupportsUpdateModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	type fileSpec struct {
		path    string
		content string
		mtime   time.Time
	}

	base := time.Unix(1_700_000_000, 0).UTC()
	files := []fileSpec{
		{path: "/tmp/src-none.txt", content: "fresh-none\n", mtime: base.Add(4 * time.Hour)},
		{path: "/tmp/dst-none.txt", content: "keep-none\n", mtime: base},
		{path: "/tmp/src-fail.txt", content: "fresh-fail\n", mtime: base.Add(4 * time.Hour)},
		{path: "/tmp/dst-fail.txt", content: "keep-fail\n", mtime: base},
		{path: "/tmp/src-all.txt", content: "fresh-all\n", mtime: base.Add(4 * time.Hour)},
		{path: "/tmp/dst-all.txt", content: "stale-all\n", mtime: base},
		{path: "/tmp/src-older.txt", content: "fresh-older\n", mtime: base.Add(4 * time.Hour)},
		{path: "/tmp/dst-older.txt", content: "stale-older\n", mtime: base},
		{path: "/tmp/src-short.txt", content: "old-short\n", mtime: base},
		{path: "/tmp/dst-short.txt", content: "keep-short\n", mtime: base.Add(4 * time.Hour)},
	}
	for _, file := range files {
		writeSessionFile(t, session, file.path, []byte(file.content))
		if err := session.FileSystem().Chtimes(context.Background(), file.path, file.mtime, file.mtime); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", file.path, err)
		}
	}

	result := mustExecSession(t, session, strings.Join([]string{
		"cp --update=none /tmp/src-none.txt /tmp/dst-none.txt",
		"cat /tmp/dst-none.txt",
		"cp --update=none-fail /tmp/src-fail.txt /tmp/dst-fail.txt",
		"printf 'none_fail=%s\\n' \"$?\"",
		"cat /tmp/dst-fail.txt",
		"cp --update=all /tmp/src-all.txt /tmp/dst-all.txt",
		"cat /tmp/dst-all.txt",
		"cp --update=older /tmp/src-older.txt /tmp/dst-older.txt",
		"cat /tmp/dst-older.txt",
		"cp -u /tmp/src-short.txt /tmp/dst-short.txt",
		"cat /tmp/dst-short.txt",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if got, want := result.Stdout, strings.Join([]string{
		"keep-none",
		"none_fail=1",
		"keep-fail",
		"fresh-all",
		"fresh-older",
		"keep-short",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "cp: not replacing '/tmp/dst-fail.txt'") {
		t.Fatalf("Stderr = %q, want none-fail message", result.Stderr)
	}
}

func TestCPUpdateModeStillRejectsSameFile(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo payload > /tmp/file.txt\n" +
			"cp -u /tmp/file.txt /tmp/file.txt\n" +
			"printf 'status=%s\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "cp: '/tmp/file.txt' and 'file.txt' are the same file") {
		t.Fatalf("Stderr = %q, want same-file error", result.Stderr)
	}
}
