package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestMalformedRedirectionDoesNotPanic(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	cases := []string{
		">&000000000000000000\n",
		">&0\n",
		">&0&0000000000000000\n",
		"0|0|>|0|0\n",
		"0|0|5>5|0\n",
	}
	for _, script := range cases {
		t.Run(strings.TrimSpace(script), func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
			}
			if !strings.Contains(result.Stderr, "invalid redirection") {
				t.Fatalf("Stderr = %q, want invalid-redirection message", result.Stderr)
			}
			if strings.Contains(result.Stderr, "unhandled >& arg") {
				t.Fatalf("Stderr = %q, want sanitized panic output", result.Stderr)
			}
		})
	}
}

func TestMalformedFunctionDeclarationDoesNotPanic(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "()8&3\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "syntax error near unexpected token") {
		t.Fatalf("Stderr = %q, want parse-error message", result.Stderr)
	}
	if strings.Contains(result.Stderr, "panic:") {
		t.Fatalf("Stderr = %q, want sanitized output", result.Stderr)
	}
}

func TestCommandPathBelowFileDoesNotEscapeAsInternalError(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "0/0>0\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}
