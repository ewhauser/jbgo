package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestTailPidRequiresProcessCapability(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hello\\n' >/tmp/in.txt\n" +
			"tail -n 0 -f --pid=123 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty output", got)
	}
	if !strings.Contains(result.Stderr, "tail: --pid is unsupported in this sandbox") {
		t.Fatalf("Stderr = %q, want unsupported --pid error", result.Stderr)
	}
}
