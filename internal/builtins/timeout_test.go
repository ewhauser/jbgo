package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestTimeoutSupportsLongKillAfterAndSignalFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout --signal TERM --kill-after 0.01 0.02 sleep 1 || echo timed\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "timed\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout message", result.Stderr)
	}
}

func TestTimeoutSupportsForegroundPreserveStatusAndVerboseFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout -fv -s 0 -k 0.01 0.02 sleep 1 || echo timed\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "timed\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout message", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "timeout: sending signal 0 to command \"sleep\"") {
		t.Fatalf("Stderr = %q, want verbose timeout signal message", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "timeout: sending signal KILL to command \"sleep\"") {
		t.Fatalf("Stderr = %q, want verbose KILL message", result.Stderr)
	}
}

func TestTimeoutPreserveStatusReturnsSignalExitCodeOnTimeout(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout --preserve-status 0.02 sleep 1\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 143 {
		t.Fatalf("ExitCode = %d, want 143; stderr=%q", result.ExitCode, result.Stderr)
	}
}

func TestTimeoutRejectsInvalidDurationAndSignalWith125(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout --signal invalid 0.02 sleep 1 || true\ntimeout invalid sleep 1 || true\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "timeout: invalid signal \"invalid\"") {
		t.Fatalf("Stderr = %q, want invalid signal error", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "timeout: invalid time interval \"invalid\"") {
		t.Fatalf("Stderr = %q, want invalid duration error", result.Stderr)
	}
}

func TestTimeoutStopsOptionParsingAtDuration(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout 0.02 echo --foreground\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "--foreground\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
