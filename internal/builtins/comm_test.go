package builtins_test

import (
	"context"
	"testing"
)

func TestCommSupportsSeparatedColumnFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'apple\\nbanana\\n' > /tmp/left.txt\nprintf 'banana\\ncarrot\\n' > /tmp/right.txt\ncomm -1 /tmp/left.txt /tmp/right.txt\ncomm -2 /tmp/left.txt /tmp/right.txt\ncomm -3 /tmp/left.txt /tmp/right.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\tbanana\ncarrot\napple\n\tbanana\napple\n\tcarrot\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
