//go:build !cgo || (darwin && amd64)

package python

import (
	"context"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestPythonReportsUnavailableWithoutNativeBindings(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python -c 'print(\"hi\")'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "monty native bindings are unavailable") {
		t.Fatalf("Stderr = %q, want gomonty unavailable diagnostic", result.Stderr)
	}
}

func TestPythonTTYREPLReportsUnavailableWithoutNativeBindings(t *testing.T) {
	t.Parallel()

	session := newPythonSession(t)
	stdout, stderr, err := runPythonCommand(t, session, map[string]string{
		"TTY": "/dev/tty",
	}, strings.NewReader("exit()\n"))
	if err == nil {
		t.Fatal("Run() error = nil, want unavailable diagnostic")
	}
	if stdout != "" {
		t.Fatalf("Stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "monty native bindings are unavailable") {
		t.Fatalf("Stderr = %q, want gomonty unavailable diagnostic", stderr)
	}
}
