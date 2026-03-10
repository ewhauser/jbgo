package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/cadencerpm/just-bash-go/policy"
)

func TestMaxCommandCountEnforced(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxCommandCount: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 1\necho 2\necho 3\necho 4\necho 5\necho 6\necho 7\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "too many commands executed") {
		t.Fatalf("Stderr = %q, want command-count limit message", result.Stderr)
	}
}

func TestMaxCommandCountResetsBetweenExecs(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxCommandCount: 3})
	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	first, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo 1\necho 2\necho 3\n",
	})
	if err != nil {
		t.Fatalf("Exec(first) error = %v", err)
	}
	if first.ExitCode != 0 {
		t.Fatalf("first ExitCode = %d, want 0", first.ExitCode)
	}

	second, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo done\n",
	})
	if err != nil {
		t.Fatalf("Exec(second) error = %v", err)
	}
	if second.ExitCode != 0 {
		t.Fatalf("second ExitCode = %d, want 0", second.ExitCode)
	}
	if got, want := second.Stdout, "done\n"; got != want {
		t.Fatalf("second Stdout = %q, want %q", got, want)
	}
}

func TestMaxCommandCountCountsCommandsInSubshells(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxCommandCount: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 1\n(echo 2; echo 3; echo 4)\necho 5\necho 6\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "too many commands executed") {
		t.Fatalf("Stderr = %q, want command-count limit message", result.Stderr)
	}
}

func TestMaxCommandCountCountsPipelineStages(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxCommandCount: 2})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo a | cat\necho b | cat\necho c | cat\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "too many commands executed") {
		t.Fatalf("Stderr = %q, want command-count limit message", result.Stderr)
	}
}

func TestMaxCommandCountAllowsCommandsWithinLimit(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxCommandCount: 10})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 1\necho 2\necho 3\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "1\n2\n3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMaxCommandCountDoesNotChargeRuntimePrelude(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxCommandCount: 2})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cd /tmp\npwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "/tmp\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
