package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestPathBasedCommandResolution(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/bin/echo hi\n/usr/bin/pwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "hi\n/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBareCommandResolutionRespectsPATH(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=/bin\nls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	for _, entry := range []string{"bin", "dev", "home", "tmp", "usr"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), entry) {
			t.Fatalf("Stdout missing root entry %q: %q", entry, result.Stdout)
		}
	}
}

func TestBareCommandResolutionFailsWhenPATHHasNoCommandDirs(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=/tmp\nls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "ls: command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}

func TestEmptyPATHDisablesBareCommandResolution(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=\nls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "ls: command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}

func TestExplicitPathResolutionBypassesPATH(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=\n/bin/ls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	for _, entry := range []string{"bin", "dev", "home", "tmp", "usr"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), entry) {
			t.Fatalf("Stdout missing root entry %q: %q", entry, result.Stdout)
		}
	}
}

func TestUnknownCommandPathReturnsCommandNotFound(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/bin/missing-command\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "No such file or directory") {
		t.Fatalf("Stderr = %q, want No such file or directory message", result.Stderr)
	}
}

func TestExplicitPathNonExecutableFileReturns126(t *testing.T) {
	t.Parallel()
	session, err := newRuntime(t, &Config{}).NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	// writeSessionFile creates the file with mode 0o644 (not executable).
	writeSessionFile(t, session, "/tmp/text-file", []byte("not a script\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "/tmp/text-file\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Permission denied") {
		t.Fatalf("Stderr = %q, want Permission denied message", result.Stderr)
	}
}

func TestExplicitPathMissingDirectoryReturns127(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/tmp/not-a-dir/text-file\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "No such file or directory") {
		t.Fatalf("Stderr = %q, want No such file or directory message", result.Stderr)
	}
}

func TestExplicitPathTooLongReturns126(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	longName := strings.Repeat("0123456789", 52) // 520 chars, exceeds NAME_MAX (255)
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script:  "./" + longName + "\n",
		WorkDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "File name too long") {
		t.Fatalf("Stderr = %q, want File name too long message", result.Stderr)
	}
}

func TestEnableBuiltinDisablesBuiltinResolution(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"foo=old\n" +
			"enable -n printf\n" +
			"printf -v foo %s hi\n" +
			"printf '\\nvalue=<%s>\\n' \"$foo\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "-v\nvalue=<old>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "printf: warning: ignoring excess arguments, starting with 'foo'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnableBuiltinStatePropagatesToSubshells(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"enable -n printf\n" +
			"(foo=old; printf -v foo %s hi; printf \"\\\\nchild=<%s>\\\\n\" \"$foo\")\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "-v\nchild=<old>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "printf: warning: ignoring excess arguments, starting with 'foo'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestDisabledBuiltinsEnvAffectsDirectShellFileExec(t *testing.T) {
	t.Parallel()
	session, err := newRuntime(t, &Config{}).NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/child.sh", []byte(
		"foo=old\n"+
			"printf -v foo %s hi\n"+
			"printf '\\nchild-file=<%s>\\n' \"$foo\"\n",
	))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Env: map[string]string{
			"GBASH_DISABLED_BUILTINS": "printf",
		},
		Command: []string{"/bin/sh", "/tmp/child.sh"},
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "-v\nchild-file=<old>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "sh: printf: warning: ignoring excess arguments, starting with 'foo'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}
