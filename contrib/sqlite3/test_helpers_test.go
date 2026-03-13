package sqlite3

import (
	"context"
	"io"
	"testing"

	"github.com/ewhauser/gbash/commands"
	gbruntime "github.com/ewhauser/gbash/runtime"
)

func newSQLiteRegistry(tb testing.TB) *commands.Registry {
	tb.Helper()

	registry := commands.DefaultRegistry()
	if err := Register(registry); err != nil {
		tb.Fatalf("Register(sqlite3) error = %v", err)
	}
	return registry
}

func newSQLiteSession(tb testing.TB) *gbruntime.Session {
	tb.Helper()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{Registry: newSQLiteRegistry(tb)}))
	if err != nil {
		tb.Fatalf("runtime.New() error = %v", err)
	}

	session, err := rt.NewSession(context.Background())
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
