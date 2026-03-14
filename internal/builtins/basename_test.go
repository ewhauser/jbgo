package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestBasenameSupportsSeparateLongSuffixArgument(t *testing.T) {
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

func TestBasenameGNUCompatibilityCases(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename fs fs\nbasename // /\nbasename -z a\nbasename --zero ba a\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "fs\n/\na\x00b\x00"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasenameGNUOperandErrors(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename\nbasename a b c\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	want := "basename: missing operand\nTry 'basename --help' for more information.\nbasename: extra operand 'c'\nTry 'basename --help' for more information.\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBasenameSupportsHelpVersionAndInferredLongOptions(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename -h\necho ---\nbasename -V\necho ---\nbasename --mul /tmp/a /tmp/b\necho ---\nbasename --suf=.txt /tmp/a.txt /tmp/b.txt\necho ---\nbasename --ze -a /tmp/z\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 5 {
		t.Fatalf("Stdout blocks = %q, want 5 blocks", result.Stdout)
	}
	if !strings.Contains(parts[0], "Usage: basename NAME [SUFFIX]") {
		t.Fatalf("help output = %q, want usage", parts[0])
	}
	if !strings.HasPrefix(parts[1], "basename (gbash) ") {
		t.Fatalf("version output = %q, want version prefix", parts[1])
	}
	if got, want := parts[2], "a\nb\n"; got != want {
		t.Fatalf("inferred --multiple output = %q, want %q", got, want)
	}
	if got, want := parts[3], "a\nb\n"; got != want {
		t.Fatalf("inferred --suffix output = %q, want %q", got, want)
	}
	if got, want := parts[4], "z\x00"; got != want {
		t.Fatalf("inferred --zero output = %q, want %q", got, want)
	}
}

func TestBasenamePreservesSimpleFormatAndSpecialPaths(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename a--help --help\nbasename '/.'\nbasename 'hello/.'\nbasename '///'\nbasename ''\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\n.\n.\n/\n\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
