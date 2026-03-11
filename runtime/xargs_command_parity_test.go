package runtime

import (
	"context"
	"testing"
)

func TestXArgsSupportsLongFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\0b\\0' | xargs --null --verbose --max-args 1 echo\n" +
			"printf '' | xargs --no-run-if-empty echo skip\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "'echo' 'a'\n'echo' 'b'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}
