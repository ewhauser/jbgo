package runtime

import (
	"context"
	"testing"
)

func TestCPSupportsParityFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"echo old > /tmp/dst.txt\n" +
			"cp --no-clobber --preserve --verbose /tmp/src.txt /tmp/dst.txt\n" +
			"cat /tmp/dst.txt\n" +
			"cp -p -v /tmp/src.txt /tmp/fresh.txt\n" +
			"cat /tmp/fresh.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "old\n'/tmp/src.txt' -> '/tmp/fresh.txt'\nnew\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
