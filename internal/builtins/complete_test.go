package builtins_test

import (
	"context"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestCompletePrintsAndStoresSpecsWithinOneExecution(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "complete\n" +
			"complete -W 'foo bar' mycommand\n" +
			"complete -p\n" +
			"complete -F myfunc other\n" +
			"complete\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stdout, "complete -W 'foo bar' mycommand\ncomplete -W 'foo bar' mycommand\ncomplete -F myfunc other\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestCompleteSupportsOptionsCommandAndNoActionSpecs(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "complete -o default -o nospace -F foo git\n" +
			"complete -C generator cmd\n" +
			"complete empty\n" +
			"complete -p git\n" +
			"complete -p cmd\n" +
			"complete -p empty\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stdout, "complete -o default -o nospace -F foo git\ncomplete -C generator cmd\ncomplete empty\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCompleteSupportsBuiltinAndCommandWrappers(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "builtin complete -W 'foo bar' builtin_cmd\n" +
			"command complete -F wrapped cmd_wrapper\n" +
			"complete\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	for _, want := range []string{
		"complete -W 'foo bar' builtin_cmd\n",
		"complete -F wrapped cmd_wrapper\n",
	} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want substring %q", result.Stdout, want)
		}
	}
}

func TestCompleteReportsUsageAndMissingSpecErrors(t *testing.T) {
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name   string
		script string
		code   int
		stderr string
	}{
		{
			name:   "missing function command name",
			script: "complete -F f\n",
			code:   2,
			stderr: "complete: -F: option requires a command name\n",
		},
		{
			name:   "invalid option name",
			script: "complete -o invalid cmd\n",
			code:   2,
			stderr: "complete: invalid: invalid option name\n",
		},
		{
			name:   "missing completion specification",
			script: "complete -p missing\n",
			code:   1,
			stderr: "complete: missing: no completion specification\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := result.Stdout; got != "" {
				t.Fatalf("Stdout = %q, want empty", got)
			}
			if got, want := result.ExitCode, tc.code; got != want {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
			}
			if got, want := result.Stderr, tc.stderr; got != want {
				t.Fatalf("Stderr = %q, want %q", got, want)
			}
		})
	}
}

func TestCompletePersistsAcrossInteractiveEntries(t *testing.T) {
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder

	result, err := session.Interact(context.Background(), &gbruntime.InteractiveRequest{
		Stdin:  strings.NewReader("complete -W 'foo bar' mycommand\ncomplete -p\nexit\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Interact() error = %v", err)
	}
	if result == nil {
		t.Fatalf("Interact() result = nil")
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
	if !strings.Contains(stdout.String(), "complete -W 'foo bar' mycommand\n") {
		t.Fatalf("Stdout = %q, want completion spec output", stdout.String())
	}
}
