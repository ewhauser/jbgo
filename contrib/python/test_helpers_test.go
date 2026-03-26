package python

import (
	"context"
	"io"
	"maps"
	"os"
	"path"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
)

func newPythonRegistry(tb testing.TB) *commands.Registry {
	tb.Helper()

	registry := gbruntime.DefaultRegistry()
	if err := Register(registry); err != nil {
		tb.Fatalf("Register(python) error = %v", err)
	}
	return registry
}

func newPythonRuntime(tb testing.TB) *gbruntime.Runtime {
	tb.Helper()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{Registry: newPythonRegistry(tb)}))
	if err != nil {
		tb.Fatalf("runtime.New() error = %v", err)
	}
	return rt
}

func newPythonRuntimeWithRegistry(tb testing.TB, registry *commands.Registry) *gbruntime.Runtime {
	tb.Helper()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{Registry: registry}))
	if err != nil {
		tb.Fatalf("runtime.New() error = %v", err)
	}
	return rt
}

func newPythonSession(tb testing.TB) *gbruntime.Session {
	tb.Helper()

	session, err := newPythonRuntime(tb).NewSession(context.Background())
	if err != nil {
		tb.Fatalf("Runtime.NewSession() error = %v", err)
	}
	return session
}

func newPythonSessionWithRegistry(tb testing.TB, registry *commands.Registry) *gbruntime.Session {
	tb.Helper()

	session, err := newPythonRuntimeWithRegistry(tb, registry).NewSession(context.Background())
	if err != nil {
		tb.Fatalf("Runtime.NewSession() error = %v", err)
	}
	return session
}

func mustExecPythonSession(tb testing.TB, session *gbruntime.Session, script string) *gbruntime.ExecutionResult {
	tb.Helper()

	result, err := session.Exec(context.Background(), &gbruntime.ExecutionRequest{Script: script})
	if err != nil {
		tb.Fatalf("Session.Exec() error = %v", err)
	}
	return result
}

func readPythonSessionFile(tb testing.TB, session *gbruntime.Session, name string) []byte {
	tb.Helper()

	file, err := session.FileSystem().Open(context.Background(), name)
	if err != nil {
		tb.Fatalf("Open(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		tb.Fatalf("ReadAll(%q) error = %v", name, err)
	}
	return data
}

func writePythonSessionFile(tb testing.TB, session *gbruntime.Session, name string, data []byte) {
	tb.Helper()

	if err := session.FileSystem().MkdirAll(context.Background(), path.Dir(name), 0o755); err != nil {
		tb.Fatalf("MkdirAll(%q) error = %v", path.Dir(name), err)
	}

	file, err := session.FileSystem().OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		tb.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(data); err != nil {
		tb.Fatalf("Write(%q) error = %v", name, err)
	}
}

func runPythonCommand(tb testing.TB, session *gbruntime.Session, env map[string]string, stdin io.Reader, args ...string) (string, string, error) {
	tb.Helper()

	clone := maps.Clone(env)
	if clone == nil {
		clone = map[string]string{}
	}
	if _, ok := clone["HOME"]; !ok {
		clone["HOME"] = "/home/agent"
	}
	if _, ok := clone["PWD"]; !ok {
		clone["PWD"] = "/home/agent"
	}

	var stdout strings.Builder
	var stderr strings.Builder
	inv := commands.NewInvocation(&commands.InvocationOptions{
		Args:       args,
		Env:        clone,
		Cwd:        "/home/agent",
		Stdin:      stdin,
		Stdout:     &stdout,
		Stderr:     &stderr,
		FileSystem: session.FileSystem(),
	})
	err := New("python").Run(context.Background(), inv)
	return stdout.String(), stderr.String(), err
}
