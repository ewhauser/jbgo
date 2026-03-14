package builtins_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestEchoSupportsGNUEscapeDecoding(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo -n -e '\\x1b\\n\\e\\n\\33\\n\\033\\n\\0033\\n'\n" +
			"echo -n -e '\\x\\n'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "\x1b\n\x1b\n\x1b\n\x1b\n\x1b\n\\x\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEchoTreatsDoubleHyphenAsLiteralAndHonorsBackslashC(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo -- 'foo'\n" +
			"echo -n -e -- 'foo\\n'\n" +
			"echo -e 'foo\\n\\cbar'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "-- foo\n-- foo\nfoo\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEchoSupportsPOSIXLYCorrectMode(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "POSIXLY_CORRECT=1 echo -n -E 'foo\\n'\n" +
			"POSIXLY_CORRECT=1 echo -nE 'foo'\n" +
			"POSIXLY_CORRECT=1 echo -E -n 'foo'\n" +
			"POSIXLY_CORRECT=1 echo --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "foo\n-nE foo\n-E -n foo\n--version\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEchoRecognizesExactHelpVersionAndOptionPrecedence(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo --version\n" +
			"echo --ver\n" +
			"echo -e -E '\\na'\n" +
			"echo -E -e '\\na'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if !strings.HasPrefix(result.Stdout, "echo (gbash) dev\n--ver\n\\na\n") {
		t.Fatalf("Stdout = %q, want version banner, literal partial long option, and disabled escapes output", result.Stdout)
	}
	if !strings.HasSuffix(result.Stdout, "\na\n") {
		t.Fatalf("Stdout = %q, want final escape-enabled newline chunk", result.Stdout)
	}
}

func TestEchoSupportsGNUOctalWrapping(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo -ne '\\0501\\777\\08'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := []byte{'A', 0xff, 0x00, '8'}
	if got := []byte(result.Stdout); !bytes.Equal(got, want) {
		t.Fatalf("Stdout bytes = %v, want %v", got, want)
	}
}

func TestEchoHelpIsAvailableAsSoleLongOption(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo --help\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Usage: echo") {
		t.Fatalf("Stdout = %q, want help text", result.Stdout)
	}
}
