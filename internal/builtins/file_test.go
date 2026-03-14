package builtins_test

import (
	"context"
	"testing"
)

func TestFileSupportsLongBriefAndMimeFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hello\\n' > /tmp/note.txt\nfile --brief /tmp/note.txt\nfile --mime /tmp/note.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "ASCII text\n/tmp/note.txt: text/plain\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
