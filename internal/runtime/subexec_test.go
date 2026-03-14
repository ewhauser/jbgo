package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/policy"
)

type subexecProbe struct{}

func (c *subexecProbe) Name() string {
	return "subexecprobe"
}

func (c *subexecProbe) Run(ctx context.Context, inv *commands.Invocation) error {
	if inv.Exec == nil {
		return fmt.Errorf("subexec callback missing")
	}
	if len(inv.Args) == 0 {
		return fmt.Errorf("missing mode")
	}

	var (
		result *commands.ExecutionResult
		err    error
	)

	switch inv.Args[0] {
	case "inherit":
		result, err = inv.Exec(ctx, &commands.ExecutionRequest{
			Script: "echo \"$FOO\"\npwd\ncat note.txt\n",
		})
	case "write":
		result, err = inv.Exec(ctx, &commands.ExecutionRequest{
			Script: "echo nested > nested.txt\n",
		})
	case "deny":
		result, err = inv.Exec(ctx, &commands.ExecutionRequest{
			Script: "cat /denied.txt\n",
		})
	default:
		return fmt.Errorf("unknown mode %q", inv.Args[0])
	}
	if err != nil {
		return err
	}

	if result.Stdout != "" {
		if _, err := io.WriteString(inv.Stdout, result.Stdout); err != nil {
			return err
		}
	}
	if result.Stderr != "" {
		if _, err := io.WriteString(inv.Stderr, result.Stderr); err != nil {
			return err
		}
	}
	if inv.Args[0] == "deny" {
		if _, err := fmt.Fprintf(inv.Stdout, "exit=%d\n", result.ExitCode); err != nil {
			return err
		}
	}
	return nil
}

func registryWithSubexecProbe(t *testing.T) *commands.Registry {
	t.Helper()

	registry := commands.DefaultRegistry()
	if err := registry.Register(&subexecProbe{}); err != nil {
		t.Fatalf("Register(subexecprobe) error = %v", err)
	}
	return registry
}

func TestInvocationExecInheritsEnvDirAndSessionState(t *testing.T) {
	rt := newRuntime(t, &Config{Registry: registryWithSubexecProbe(t)})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p work\n echo note > work/note.txt\n cd work\n FOO=bar subexecprobe inherit\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "bar\n/home/agent/work\nnote\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestInvocationExecUsesSameSessionFilesystem(t *testing.T) {
	session := newSession(t, &Config{Registry: registryWithSubexecProbe(t)})

	result := mustExecSession(t, session, "subexecprobe write\ncat nested.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "nested\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestInvocationExecStaysWithinSessionPolicyBoundary(t *testing.T) {
	registry := registryWithSubexecProbe(t)
	rt := newRuntime(t, &Config{
		Registry: registry,
		Policy: policy.NewStatic(&policy.Config{
			AllowedCommands: []string{"subexecprobe"},
			ReadRoots:       []string{"/"},
			WriteRoots:      []string{"/"},
			Limits: policy.Limits{
				MaxStdoutBytes: 1 << 20,
				MaxStderrBytes: 1 << 20,
				MaxFileBytes:   8 << 20,
			},
			NetworkMode: policy.NetworkDisabled,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "subexecprobe deny\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "exit=126") {
		t.Fatalf("Stdout = %q, want nested policy denial exit code", result.Stdout)
	}
	if !strings.Contains(result.Stderr, `command "cat" denied`) {
		t.Fatalf("Stderr = %q, want nested policy denial message", result.Stderr)
	}
}

var _ commands.Command = (*subexecProbe)(nil)
