package runtime

import (
	"context"
	"testing"
)

func TestPasteSupportsLongFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\n' > /tmp/in.txt\n" +
			"paste --serial --delimiters=':' /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one:two\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
