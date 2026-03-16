package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/policy"
)

func TestDirectCommandRequestRespectsPATHAndWorkDir(t *testing.T) {
	session := newSession(t, &Config{})
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/work", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp/work) error = %v", err)
	}

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Command: []string{"pwd"},
		Env:     map[string]string{"PATH": "/bin"},
		WorkDir: "/tmp/work",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/work\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDirectCommandRequestReturns127ForMissingCommand(t *testing.T) {
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Command: []string{"definitely-missing-command"},
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "definitely-missing-command: command not found") {
		t.Fatalf("Stderr = %q, want command-not-found message", result.Stderr)
	}
}

func TestDirectCommandRequestCancellationReturns130(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(20*time.Millisecond, cancel)

	result, err := rt.Run(ctx, &ExecutionRequest{
		Command: []string{"sleep", "1"},
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

func TestDirectCommandRequestRejectsScriptAndCommand(t *testing.T) {
	session := newSession(t, &Config{})

	if _, err := session.Exec(context.Background(), &ExecutionRequest{
		Script:  "echo hi\n",
		Command: []string{"echo", "hi"},
	}); err == nil {
		t.Fatal("Exec() error = nil, want validation failure")
	}
}
