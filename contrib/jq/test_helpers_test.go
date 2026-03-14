package jq

import (
	"context"
	"os"
	"path"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
)

func newJQRegistry(tb testing.TB) *commands.Registry {
	tb.Helper()

	registry := commands.DefaultRegistry()
	if err := Register(registry); err != nil {
		tb.Fatalf("Register(jq) error = %v", err)
	}
	return registry
}

func newJQRuntime(tb testing.TB) *gbruntime.Runtime {
	tb.Helper()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{Registry: newJQRegistry(tb)}))
	if err != nil {
		tb.Fatalf("runtime.New() error = %v", err)
	}
	return rt
}

func newJQSession(tb testing.TB) *gbruntime.Session {
	tb.Helper()

	session, err := newJQRuntime(tb).NewSession(context.Background())
	if err != nil {
		tb.Fatalf("Runtime.NewSession() error = %v", err)
	}
	return session
}

func mustExecSession(tb testing.TB, session *gbruntime.Session, script string) *gbruntime.ExecutionResult {
	tb.Helper()

	result, err := session.Exec(context.Background(), &gbruntime.ExecutionRequest{Script: script})
	if err != nil {
		tb.Fatalf("Session.Exec() error = %v", err)
	}
	return result
}

func writeSessionFile(tb testing.TB, session *gbruntime.Session, name string, data []byte) {
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
