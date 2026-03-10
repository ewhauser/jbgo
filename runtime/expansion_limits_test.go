package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/cadencerpm/just-bash-go/policy"
)

func TestMaxSubstitutionDepthEnforced(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxSubstitutionDepth: 2})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo $(echo $(echo $(echo too-deep)))\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Command substitution nesting limit exceeded") {
		t.Fatalf("Stderr = %q, want substitution-depth message", result.Stderr)
	}
}

func TestMaxSubstitutionDepthAllowsNestedCommandSubstitutionWithinLimit(t *testing.T) {
	rt := newRuntimeWithLimits(t, policy.Limits{MaxSubstitutionDepth: 2})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo $(echo $(echo ok))\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "ok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMaxGlobOperationsEnforced(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits: policy.Limits{
				MaxGlobOperations: 1,
			},
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/globtest", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/globtest/a.txt", []byte("a\n"))
	writeSessionFile(t, session, "/tmp/globtest/b.txt", []byte("b\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo /tmp/globtest/*.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Glob operation limit exceeded") {
		t.Fatalf("Stderr = %q, want glob-limit message", result.Stderr)
	}
}

func TestMaxGlobOperationsAllowsExpansionWithinLimit(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits: policy.Limits{
				MaxGlobOperations: 10,
			},
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/globtest", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/globtest/a.txt", []byte("a\n"))
	writeSessionFile(t, session, "/tmp/globtest/b.txt", []byte("b\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo /tmp/globtest/*.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/globtest/a.txt /tmp/globtest/b.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
