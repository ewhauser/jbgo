package runtime

import (
	"context"
	"testing"

	jbfs "github.com/ewhauser/jbgo/fs"
)

type benchmarkOverlayFactory struct {
	lower jbfs.FileSystem
}

func (f benchmarkOverlayFactory) New(context.Context) (jbfs.FileSystem, error) {
	return jbfs.NewOverlay(f.lower), nil
}

func newSeededRuntime(tb testing.TB, files map[string]string) *Runtime {
	tb.Helper()

	base := newSession(tb, nil)
	seedSessionFiles(tb, base, files)

	lower, err := jbfs.NewSnapshot(context.Background(), base.FileSystem())
	if err != nil {
		tb.Fatalf("NewSnapshot() error = %v", err)
	}

	return newRuntime(tb, &Config{
		FSFactory: benchmarkOverlayFactory{lower: lower},
	})
}

func mustNewBenchmarkSession(tb testing.TB, rt *Runtime) *Session {
	tb.Helper()

	session, err := rt.NewSession(context.Background())
	if err != nil {
		tb.Fatalf("NewSession() error = %v", err)
	}
	return session
}

func mustExecRequest(tb testing.TB, session *Session, req *ExecutionRequest) *ExecutionResult {
	tb.Helper()

	result, err := session.Exec(context.Background(), req)
	if err != nil {
		tb.Fatalf("Exec() error = %v", err)
	}
	return result
}

func mustRunRequest(tb testing.TB, rt *Runtime, req *ExecutionRequest) *ExecutionResult {
	tb.Helper()

	result, err := rt.Run(context.Background(), req)
	if err != nil {
		tb.Fatalf("Run() error = %v", err)
	}
	return result
}

func requireExecutionOutcome(tb testing.TB, result *ExecutionResult, wantStdout string) {
	tb.Helper()

	if result == nil {
		tb.Fatalf("result is nil")
		return
	}
	if result.ExitCode != 0 {
		tb.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stderr != "" {
		tb.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
	if result.Stdout != wantStdout {
		tb.Fatalf("Stdout = %q, want %q", result.Stdout, wantStdout)
	}
}

func benchmarkRuntimeRun(b *testing.B, rt *Runtime, req *ExecutionRequest, wantStdout string) {
	b.Helper()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		requireExecutionOutcome(b, mustRunRequest(b, rt, req), wantStdout)
	}
}

func benchmarkSessionExec(b *testing.B, session *Session, req *ExecutionRequest, wantStdout string) {
	b.Helper()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		requireExecutionOutcome(b, mustExecRequest(b, session, req), wantStdout)
	}
}

func benchmarkFreshSessionExec(b *testing.B, rt *Runtime, req *ExecutionRequest, wantStdout string) {
	b.Helper()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustNewBenchmarkSession(b, rt)
		requireExecutionOutcome(b, mustExecRequest(b, session, req), wantStdout)
	}
}

type benchmarkWorkflowStep struct {
	req        ExecutionRequest
	wantStdout string
}

func benchmarkWorkflow(b *testing.B, rt *Runtime, steps ...benchmarkWorkflowStep) {
	b.Helper()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustNewBenchmarkSession(b, rt)
		for j := range steps {
			requireExecutionOutcome(b, mustExecRequest(b, session, &steps[j].req), steps[j].wantStdout)
		}
	}
}
