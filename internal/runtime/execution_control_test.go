package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/policy"
)

type timeoutProbe struct{}

func (c *timeoutProbe) Name() string {
	return "timeoutprobe"
}

func (c *timeoutProbe) Run(ctx context.Context, inv *commands.Invocation) error {
	if inv.Exec == nil {
		return fmt.Errorf("subexec callback missing")
	}

	result, err := inv.Exec(ctx, &commands.ExecutionRequest{
		Command: []string{"sleep", "1"},
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(inv.Stdout, "exit=%d\n", result.ExitCode); err != nil {
		return err
	}
	if result.Stderr != "" {
		if _, err := io.WriteString(inv.Stderr, result.Stderr); err != nil {
			return err
		}
	}
	return nil
}

func TestExecutionTimeoutReturns124(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script:  "while true; do :; done\n",
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want 124", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout message", result.Stderr)
	}
}

func TestExecutionCancellationReturns130(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(20*time.Millisecond, cancel)

	result, err := rt.Run(ctx, &ExecutionRequest{
		Script: "while true; do :; done\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 130 {
		t.Fatalf("ExitCode = %d, want 130", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "execution canceled") {
		t.Fatalf("Stderr = %q, want cancellation message", result.Stderr)
	}
}

func TestInvocationExecTimeoutIsScopedToSubexecution(t *testing.T) {
	rt := newRuntime(t, &Config{
		Registry: registryWithCommands(t, &timeoutProbe{}),
		Policy: policy.NewStatic(&policy.Config{
			AllowedCommands: []string{"echo", "sleep", "timeoutprobe"},
			ReadRoots:       []string{"/"},
			WriteRoots:      []string{"/"},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeoutprobe\necho after\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "exit=124\nafter\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want nested timeout message", result.Stderr)
	}
}

func TestRedirectPolicyDenialReturns126(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{defaultHomeDir},
			WriteRoots: []string{defaultHomeDir},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hi > /tmp/out\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, `write "/tmp/out" denied`) {
		t.Fatalf("Stderr = %q, want redirect policy denial message", result.Stderr)
	}
}

func TestCommandResolutionPolicyDenialReturns126(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{defaultHomeDir},
			WriteRoots: []string{defaultHomeDir},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/bin/echo hi\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, `stat "/bin/echo" denied`) {
		t.Fatalf("Stderr = %q, want command-resolution policy denial message", result.Stderr)
	}
}

var _ commands.Command = (*timeoutProbe)(nil)
