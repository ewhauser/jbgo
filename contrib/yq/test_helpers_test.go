package yq

import (
	"context"
	"io"
	"os"
	"path"
	"testing"

	"github.com/ewhauser/gbash/commands"
	gbruntime "github.com/ewhauser/gbash/runtime"
)

func newYQRegistry(tb testing.TB) *commands.Registry {
	tb.Helper()

	registry := commands.DefaultRegistry()
	if err := Register(registry); err != nil {
		tb.Fatalf("Register(yq) error = %v", err)
	}
	return registry
}

func newYQRuntime(tb testing.TB) *gbruntime.Runtime {
	tb.Helper()

	rt, err := gbruntime.New(&gbruntime.Config{Registry: newYQRegistry(tb)})
	if err != nil {
		tb.Fatalf("runtime.New() error = %v", err)
	}
	return rt
}

func newYQSession(tb testing.TB) *gbruntime.Session {
	tb.Helper()

	session, err := newYQRuntime(tb).NewSession(context.Background())
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

func readSessionFile(tb testing.TB, session *gbruntime.Session, name string) []byte {
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
