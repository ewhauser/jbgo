package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/builtins"
)

func TestRunSimpleScript(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hi\npwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "hi\n/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRunPreservesTrailingCarriageReturnWithoutInjectedNewline(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '[%s]\\n' -\r",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "[-\r]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRunRedirectAndCat(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hi > /tmp.txt\ncat /tmp.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "hi\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "missing-command\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "missing-command: command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}

func TestNewAcceptsOptions(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry(builtins.NewEcho())

	rt, err := New(WithRegistry(registry))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "echo hi\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.Stdout, "hi\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestNewAcceptsWithConfig(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry(builtins.NewEcho())

	rt, err := New(WithConfig(&Config{Registry: registry}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "echo hi\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.Stdout, "hi\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
