package runtime

import (
	"context"
	"testing"
)

func TestNLSupportsNumberFormatFlagIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\n' | nl -ba -n rz -w 3\n" +
			"printf 'one\\n' | nl -ba -n ln -w 3 -s ':'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "001\tone\n002\ttwo\n1  :one\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
