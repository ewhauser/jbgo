package runtime

import (
	"context"
	"testing"
)

func TestExprSupportsArithmeticAndRegexCapture(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "expr 12 \\* 4\nexpr 7 + 5\nexpr './tests/init.sh' : '.*/\\(.*\\)$'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "48\n12\ninit.sh\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExprSupportsComparisonsAndFalseyExitCode(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "expr 1 '<' 2\nexpr 0\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
