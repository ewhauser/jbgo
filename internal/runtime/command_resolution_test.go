package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestPathBasedCommandResolution(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/bin/echo hi\n/usr/bin/pwd\n",
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

func TestBareCommandResolutionRespectsPATH(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=/bin\nls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	for _, entry := range []string{"bin", "dev", "home", "tmp", "usr"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), entry) {
			t.Fatalf("Stdout missing root entry %q: %q", entry, result.Stdout)
		}
	}
}

func TestBareCommandResolutionFailsWhenPATHHasNoCommandDirs(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=/tmp\nls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "ls: command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}

func TestEmptyPATHDisablesBareCommandResolution(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=\nls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "ls: command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}

func TestExplicitPathResolutionBypassesPATH(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=\n/bin/ls /\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	for _, entry := range []string{"bin", "dev", "home", "tmp", "usr"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), entry) {
			t.Fatalf("Stdout missing root entry %q: %q", entry, result.Stdout)
		}
	}
}

func TestUnknownCommandPathReturnsCommandNotFound(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/bin/missing-command\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/bin/missing-command: command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}
