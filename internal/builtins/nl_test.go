package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestNLSupportsNumberFormatsAndBlankLines(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\n' | nl -ba -n rz -w 3\n" +
			"printf 'one\\n' | nl -ba -n ln -w 3 -s ':'\n" +
			"printf 'a\\n\\n' | nl\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "001\tone\n002\ttwo\n1  :one\n     1\ta\n       \n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestNLSupportsSectionsAndRenumbering(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/sections.txt", []byte("\\:\\:\\:\na\n\\:\\:\nb\n\\:\nc\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "nl -ha -fa /tmp/sections.txt\n" +
			"nl -p -ha -fa /tmp/sections.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "\n     1\ta\n\n     1\tb\n\n     1\tc\n\n     1\ta\n\n     2\tb\n\n     3\tc\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestNLSupportsDelimiterExtensions(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/disabled.txt", []byte("a\n\\:\\:\nc\n"))
	writeSessionFile(t, session, "/tmp/x.txt", []byte("a\nx:x:\nc\n"))
	writeSessionFile(t, session, "/tmp/foo.txt", []byte("a\nfoofoo\nc\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "nl -d '' /tmp/disabled.txt\n" +
			"nl -d x /tmp/x.txt\n" +
			"nl -d foo /tmp/foo.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "     1\ta\n     2\t\\:\\:\n     3\tc\n     1\ta\n\n     1\tc\n     1\ta\n\n     1\tc\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestNLHandlesNegativeIncrementAndOverflow(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\nb\\n' | nl -v 9223372036854775807 -i -9223372036854775808\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "9223372036854775807\ta\n    -1\tb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/overflow.txt", []byte("a\n\\:\\:\nb\n"))

	overflow, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "nl -p -v 9223372036854775807 /tmp/overflow.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() overflow error = %v", err)
	}
	if overflow.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", overflow.ExitCode, overflow.Stderr)
	}
	if got, want := overflow.Stdout, "9223372036854775807\ta\n\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(overflow.Stderr, "nl: line number overflow") {
		t.Fatalf("Stderr = %q, want overflow message", overflow.Stderr)
	}
}

func TestNLContinuesPastDirectoryOperands(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/file.txt", []byte("aaa"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "nl /tmp/dir /tmp/file.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "     1\taaa\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "nl: /tmp/dir: Is a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestNLRejectsInvalidStylesAndWidth(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '' | nl -binvalid\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "invalid numbering style") {
		t.Fatalf("Stderr = %q, want invalid numbering style", result.Stderr)
	}

	result, err = rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '' | nl -w0\n",
	})
	if err != nil {
		t.Fatalf("Run() width error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "line number field width") {
		t.Fatalf("Stderr = %q, want width error", result.Stderr)
	}
}
