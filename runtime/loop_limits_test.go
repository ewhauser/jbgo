package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/cadencerpm/just-bash-go/policy"
)

func TestMaxLoopIterationsEnforcedInWhile(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 10})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "while true; do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "while loop: too many iterations") {
		t.Fatalf("Stderr = %q, want while-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInFor(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for i in 1 2 3 4 5 6; do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "for loop: too many iterations") {
		t.Fatalf("Stderr = %q, want for-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInUntil(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "until false; do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "until loop: too many iterations") {
		t.Fatalf("Stderr = %q, want until-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInNestedLoops(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for i in 1 2 3; do\n  for j in 1 2 3 4 5 6; do\n    :\n  done\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "for loop: too many iterations") {
		t.Fatalf("Stderr = %q, want nested-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInCStyleFor(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for ((;;)); do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "for loop: too many iterations") {
		t.Fatalf("Stderr = %q, want C-style for-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsAllowsLoopsWithinLimit(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 100})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for i in 1 2 3 4 5; do\n  echo $i\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "1\n2\n3\n4\n5\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
