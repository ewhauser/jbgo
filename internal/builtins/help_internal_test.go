package builtins

import (
	"bytes"
	"context"
	"testing"
)

func runHelpCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	err := NewHelp().Run(context.Background(), &Invocation{
		Args:   args,
		Stdout: &stdout,
		Stderr: &stderr,
		Env: map[string]string{
			archEnvKey:         "aarch64",
			unameReleaseEnvKey: "25.2.0",
		},
	})
	return stdout.String(), stderr.String(), err
}

func TestHelpDefaultOutputMatchesBashListShape(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runHelpCommand(t)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "GNU bash, version 5.3.9(1)-release (aarch64-apple-darwin25.2.0)\n" + bashHelpListBody
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestHelpDetailedTopicMatchesBashHelpBuiltin(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runHelpCommand(t, "help")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout, builtinHelp["help"].Body; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestHelpShortSynopsisMatchesBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runHelpCommand(t, "-s", "pwd")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout, "pwd: pwd [-LP]\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
