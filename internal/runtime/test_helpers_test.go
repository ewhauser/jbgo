package runtime

import (
	"context"
	"io"
	"os"
	"path"
	"slices"
	"testing"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/policy"
)

func newRuntime(t testing.TB, cfg *Config) *Runtime {
	t.Helper()

	rt, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return rt
}

func newSession(t testing.TB, cfg *Config) *Session {
	t.Helper()

	session, err := newRuntime(t, cfg).NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	return session
}

func newRuntimeWithLimits(t testing.TB, limits policy.Limits) *Runtime {
	t.Helper()

	return newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits:     limits,
		}),
	})
}

func registryWithCommands(t testing.TB, extras ...commands.Command) *commands.Registry {
	t.Helper()

	registry := builtins.DefaultRegistry()
	for _, cmd := range extras {
		if err := registry.Register(cmd); err != nil {
			t.Fatalf("Register(%s) error = %v", cmd.Name(), err)
		}
	}
	return registry
}

func containsLine(lines []string, want string) bool {
	return slices.Contains(lines, want)
}

func mustExecSession(t testing.TB, session *Session, script string) *ExecutionResult {
	t.Helper()

	result, err := session.Exec(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	return result
}

func writeSessionFile(t testing.TB, session *Session, name string, data []byte) {
	t.Helper()

	if err := session.FileSystem().MkdirAll(context.Background(), path.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path.Dir(name), err)
	}

	file, err := session.FileSystem().OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(data); err != nil {
		t.Fatalf("Write(%q) error = %v", name, err)
	}
}

func readSessionFile(t testing.TB, session *Session, name string) []byte {
	t.Helper()

	file, err := session.FileSystem().Open(context.Background(), name)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", name, err)
	}
	return data
}
