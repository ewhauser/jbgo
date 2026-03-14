package jq

import (
	"context"
	"fmt"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func BenchmarkCommandJQTransform(b *testing.B) {
	content := jqBenchmarkInput()
	session := mustSeedBenchmarkSession(b, map[string]string{
		"/bench/jq/input.json": content,
	})
	b.SetBytes(int64(len(content)))

	benchmarkSessionExec(b, session, &gbruntime.ExecutionRequest{
		Script: "jq '[.items[] | select(.enabled) | .id] | length' /bench/jq/input.json\n",
	}, "1000\n")
}

func mustSeedBenchmarkSession(tb testing.TB, files map[string]string) *gbruntime.Session {
	tb.Helper()

	session, err := newJQRuntime(tb).NewSession(context.Background())
	if err != nil {
		tb.Fatalf("Runtime.NewSession() error = %v", err)
	}
	for name, content := range files {
		writeSessionFile(tb, session, name, []byte(content))
	}
	return session
}

func benchmarkSessionExec(b *testing.B, session *gbruntime.Session, req *gbruntime.ExecutionRequest, wantStdout string) {
	b.Helper()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := session.Exec(context.Background(), req)
		if err != nil {
			b.Fatalf("Session.Exec() error = %v", err)
		}
		if result.ExitCode != 0 {
			b.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		if result.Stdout != wantStdout {
			b.Fatalf("Stdout = %q, want %q", result.Stdout, wantStdout)
		}
	}
}

func jqBenchmarkInput() string {
	var body strings.Builder
	body.WriteString("{\"items\":[")
	for i := range 2000 {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, "{\"id\":%d,\"enabled\":%t,\"name\":\"item-%04d\",\"team\":\"core\"}",
			i,
			i%2 == 0,
			i)
	}
	body.WriteString("]}\n")
	return body.String()
}
