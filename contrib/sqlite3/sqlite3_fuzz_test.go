package sqlite3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/policy"
	gbruntime "github.com/ewhauser/gbash/runtime"
)

const (
	fuzzMaxScriptBytes = 4 << 10
	fuzzMaxValueBytes  = 2 << 10
	fuzzTimeout        = 1 * time.Second
	fuzzWarmupTimeout  = 5 * time.Second
)

func newFuzzRuntime(tb testing.TB) *gbruntime.Runtime {
	tb.Helper()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{
		Registry: newSQLiteRegistry(tb),
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits: policy.Limits{
				MaxCommandCount:      200,
				MaxGlobOperations:    2000,
				MaxLoopIterations:    200,
				MaxSubstitutionDepth: 8,
				MaxStdoutBytes:       16 << 10,
				MaxStderrBytes:       16 << 10,
				MaxFileBytes:         128 << 10,
			},
		}),
	}))
	if err != nil {
		tb.Fatalf("runtime.New() error = %v", err)
	}
	return rt
}

func newFuzzSession(tb testing.TB, rt *gbruntime.Runtime) *gbruntime.Session {
	tb.Helper()

	session, err := rt.NewSession(context.Background())
	if err != nil {
		tb.Fatalf("Runtime.NewSession() error = %v", err)
	}
	return session
}

func runFuzzSessionScript(t *testing.T, session *gbruntime.Session, script []byte) (*gbruntime.ExecutionResult, error) {
	t.Helper()

	if len(script) > fuzzMaxScriptBytes {
		t.Skip()
	}

	return session.Exec(context.Background(), &gbruntime.ExecutionRequest{
		Name:    "sqlite-fuzz.sh",
		Script:  string(script),
		Timeout: fuzzTimeout,
	})
}

func warmFuzzSQLite(tb testing.TB, rt *gbruntime.Runtime) {
	tb.Helper()

	session := newFuzzSession(tb, rt)
	result, err := session.Exec(context.Background(), &gbruntime.ExecutionRequest{
		Name:    "sqlite-warmup.sh",
		Script:  "sqlite3 :memory: \"select 1;\"\n",
		Timeout: fuzzWarmupTimeout,
	})
	if err != nil {
		tb.Fatalf("sqlite fuzz warmup error = %v", err)
	}
	if result == nil {
		tb.Fatalf("sqlite fuzz warmup returned nil result")
		return
	}
	if result.ExitCode != 0 {
		tb.Fatalf("sqlite fuzz warmup ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
}

func assertSuccessfulFuzzExecution(t *testing.T, script []byte, result *gbruntime.ExecutionResult, err error) {
	t.Helper()

	assertSecureFuzzOutcome(t, script, result, err)
	if err != nil {
		return
	}
	if result == nil {
		t.Fatalf("nil result for script %q", previewFuzzScript(script))
		return
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code %d\nscript=%q\nstderr=%q", result.ExitCode, previewFuzzScript(script), result.Stderr)
	}
}

func assertSecureFuzzOutcome(t *testing.T, script []byte, result *gbruntime.ExecutionResult, err error) {
	t.Helper()

	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error %T: %v\nscript=%q", err, err, previewFuzzScript(script))
	}
	assertNoHostPathLeak(t, script, result, err)
	assertNoSensitiveDisclosure(t, script, result, err)
	assertNoInternalCrashOutput(t, script, result, err)
}

func assertNoHostPathLeak(t *testing.T, script []byte, result *gbruntime.ExecutionResult, err error) {
	t.Helper()

	checks := []string{}
	if cwd, cwdErr := os.Getwd(); cwdErr == nil && cwd != "" {
		checks = append(checks, cwd)
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
		checks = append(checks, home)
	}

	var haystacks []string
	if result != nil {
		haystacks = append(haystacks, result.Stderr)
	}
	if err != nil {
		haystacks = append(haystacks, err.Error())
	}

	for _, token := range checks {
		if token == "" || bytes.Contains(script, []byte(token)) {
			continue
		}
		for _, haystack := range haystacks {
			if strings.Contains(haystack, token) {
				t.Fatalf("host path leak %q in fuzz outcome\nscript=%q\noutput=%q", token, previewFuzzScript(script), haystack)
			}
		}
	}
}

func assertNoSensitiveDisclosure(t *testing.T, script []byte, result *gbruntime.ExecutionResult, err error) {
	t.Helper()

	tokens := []string{os.Getenv("HOME"), os.Getenv("USER"), os.Getenv("LOGNAME"), os.Getenv("SHELL"), os.Getenv("TMPDIR")}
	var haystacks []string
	if result != nil {
		haystacks = append(haystacks, result.Stdout, result.Stderr)
	}
	if err != nil {
		haystacks = append(haystacks, err.Error())
	}

	for _, token := range tokens {
		if token == "" || len(token) < 4 || bytes.Contains(script, []byte(token)) {
			continue
		}
		for _, haystack := range haystacks {
			if strings.Contains(haystack, token) {
				t.Fatalf("sensitive host token leak %q in fuzz outcome\nscript=%q\noutput=%q", token, previewFuzzScript(script), haystack)
			}
		}
	}
}

func assertNoInternalCrashOutput(t *testing.T, script []byte, result *gbruntime.ExecutionResult, err error) {
	t.Helper()

	var haystacks []string
	if result != nil {
		haystacks = append(haystacks, result.Stderr)
	}
	if err != nil {
		haystacks = append(haystacks, err.Error())
	}

	for _, haystack := range haystacks {
		if strings.Contains(haystack, "panic:") || strings.Contains(haystack, "fatal error:") {
			t.Fatalf("internal crash output in fuzz outcome\nscript=%q\noutput=%q", previewFuzzScript(script), haystack)
		}
	}
}

func previewFuzzScript(script []byte) string {
	const maxPreviewBytes = 160
	if len(script) <= maxPreviewBytes {
		return string(script)
	}
	return string(script[:maxPreviewBytes]) + "...(truncated)"
}

func sanitizeSQLiteValue(raw string) string {
	raw = strings.ToValidUTF8(raw, "?")
	raw = strings.ReplaceAll(raw, "\x00", "?")
	if len(raw) > fuzzMaxValueBytes {
		raw = raw[:fuzzMaxValueBytes]
	}
	return raw
}

func sqliteStringLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func FuzzSQLiteCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	warmFuzzSQLite(f, rt)

	for _, seed := range []string{"alpha", "beta,gamma", "O'Reilly", ""} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		session := newFuzzSession(t, rt)
		value := sanitizeSQLiteValue(raw)
		sql := fmt.Sprintf(
			"create table t(value text); insert into t values ('%s'); select value from t;",
			sqliteStringLiteral(value),
		)

		script := []byte("sqlite3 :memory: " + shellQuote(sql) + " >/tmp/sqlite-value.txt\n")
		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzSQLiteFileCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	warmFuzzSQLite(f, rt)

	for _, seed := range []string{"alpha", "beta,gamma", "O'Reilly", ""} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		session := newFuzzSession(t, rt)
		value := sanitizeSQLiteValue(raw)
		sql := fmt.Sprintf(
			"create table if not exists items(value text); insert into items values ('%s'); select value from items order by value;",
			sqliteStringLiteral(value),
		)

		script := []byte("sqlite3 -json /tmp/data.db " + shellQuote(sql) + " >/tmp/sqlite-json.txt\n")
		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
