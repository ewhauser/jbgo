package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestRunSimpleScript(t *testing.T) {
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

func TestRunRedirectAndCat(t *testing.T) {
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
