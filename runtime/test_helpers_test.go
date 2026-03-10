package runtime

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/cadencerpm/just-bash-go/commands"
	"github.com/cadencerpm/just-bash-go/policy"
)

func newRuntime(t *testing.T, cfg *Config) *Runtime {
	t.Helper()

	rt, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return rt
}

func newSession(t *testing.T, cfg *Config) *Session {
	t.Helper()

	session, err := newRuntime(t, cfg).NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	return session
}

func newRuntimeWithLimits(t *testing.T, limits policy.Limits) *Runtime {
	t.Helper()

	return newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits:     limits,
		}),
	})
}

func registryWithCommands(t *testing.T, extras ...commands.Command) *commands.Registry {
	t.Helper()

	registry := commands.DefaultRegistry()
	for _, cmd := range extras {
		if err := registry.Register(cmd); err != nil {
			t.Fatalf("Register(%s) error = %v", cmd.Name(), err)
		}
	}
	return registry
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func mustExecSession(t *testing.T, session *Session, script string) *ExecutionResult {
	t.Helper()

	result, err := session.Exec(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	return result
}

func writeSessionFile(t *testing.T, session *Session, name string, data []byte) {
	t.Helper()

	if err := session.FileSystem().MkdirAll(context.Background(), path.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path.Dir(name), err)
	}

	file, err := session.FileSystem().OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		t.Fatalf("Write(%q) error = %v", name, err)
	}
}
