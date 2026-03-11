package runtime

import (
	"context"
	"testing"
)

func TestCutSupportsLongOnlyDelimitedFlagIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'left:right\\nplain\\n' > /tmp/in.txt\ncut --only-delimited -d: -f2 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "right\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
