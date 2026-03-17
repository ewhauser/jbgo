package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestCommandAndBuiltinMetaBuiltins(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/bin/hello", []byte("echo hello\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/tmp/bin/hello", 0o755); err != nil {
		t.Fatalf("Chmod(/tmp/bin/hello) error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/bin/nonexec", []byte("echo nope\n"))

	result := mustExecSession(t, session, `
PATH=/tmp/bin
command -v for
echo kw=$?

command -V missing 2>err.txt
echo missing=$?
command -p cat err.txt

builtin ls 2>builtin.err
echo bls=$?
command -p cat builtin.err

builtin --
echo bare=$?

command -p hello
echo phello=$?

PATH=
command -p ls >/dev/null
echo pls=$?

command -v /tmp/bin/nonexec
echo nonexec=$?
`)

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "for\nkw=0\n") {
		t.Fatalf("Stdout = %q, want command -v keyword lookup", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "missing=1\ncommand: missing: not found\n") {
		t.Fatalf("Stdout = %q, want command -V missing diagnostic", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "bls=1\nbuiltin: ls: not a shell builtin\n") {
		t.Fatalf("Stdout = %q, want builtin missing diagnostic", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "bare=0\n") {
		t.Fatalf("Stdout = %q, want builtin -- no-op success", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "phello=127\n") {
		t.Fatalf("Stdout = %q, want command -p to ignore custom PATH entries", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "pls=0\n") {
		t.Fatalf("Stdout = %q, want command -p to find default-path commands", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "nonexec=1\n") {
		t.Fatalf("Stdout = %q, want command -v to ignore non-executable files", result.Stdout)
	}
}

func TestPATHExecutableOverridesRegisteredCommand(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/bin/tr", []byte("echo wrong\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/tmp/bin/tr", 0o755); err != nil {
		t.Fatalf("Chmod(/tmp/bin/tr) error = %v", err)
	}

	result := mustExecSession(t, session, `
PATH=/tmp/bin:$PATH
printf aaa | tr a b
printf aaa | command -p tr a b
`)

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "wrong\nbbb"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
