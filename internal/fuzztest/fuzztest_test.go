package fuzztest

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/ewhauser/gbash"
)

func TestAssertSecureFuzzOutcomeRejectsRawDeadlineExceeded(t *testing.T) {
	t.Parallel()
	err := runFatalHelper(t, "raw-deadline")
	if err == nil {
		t.Fatalf("AssertSecureFuzzOutcome() unexpectedly accepted raw context deadline exceeded")
	}
}

func TestAssertExecutionTimeoutFuzzOutcomeAcceptsNormalizedTimeout(t *testing.T) {
	t.Parallel()
	AssertExecutionTimeoutFuzzOutcome(t, []byte("sleep 1\n"), &gbash.ExecutionResult{
		ExitCode:      124,
		Stderr:        "execution timed out after 500ms\n",
		ControlStderr: "execution timed out after 500ms",
		Duration:      300 * time.Millisecond,
	}, nil)
}

func TestAssertSecureFuzzOutcomeRejectsNormalizedTimeoutByDefault(t *testing.T) {
	t.Parallel()
	err := runFatalHelper(t, "normalized-timeout")
	if err == nil {
		t.Fatalf("AssertSecureFuzzOutcome() unexpectedly accepted normalized timeout output")
	}
}

func TestAssertHelperProcess(t *testing.T) {
	t.Parallel()
	mode := os.Getenv("GBASH_FUZZTEST_HELPER")
	if mode == "" {
		t.Skip("helper process only")
	}

	switch mode {
	case "raw-deadline":
		AssertSecureFuzzOutcome(t, []byte("sleep 1\n"), nil, context.DeadlineExceeded)
	case "normalized-timeout":
		AssertSecureFuzzOutcome(t, []byte("sleep 1\n"), &gbash.ExecutionResult{
			ExitCode:      124,
			Stderr:        "execution timed out after 500ms\n",
			ControlStderr: "execution timed out after 500ms",
			Duration:      300 * time.Millisecond,
		}, nil)
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func runFatalHelper(t *testing.T, mode string) error {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestAssertHelperProcess$") //nolint:noctx // test helper
	cmd.Env = append(os.Environ(), "GBASH_FUZZTEST_HELPER="+mode)
	return cmd.Run()
}
