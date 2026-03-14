package runtime

import (
	"context"
	"testing"
)

func TestTRSupportsLongDeleteAndSqueezeFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'aaabbbccc' | tr --squeeze-repeats abc\nprintf 'abc123' | tr --delete '[:alpha:]'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "abc123"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
