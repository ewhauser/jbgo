package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestMalformedRedirectionDoesNotPanic(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: ">&000000000000000000\n",
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
}

func TestCommandPathBelowFileDoesNotEscapeAsInternalError(t *testing.T) {
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
