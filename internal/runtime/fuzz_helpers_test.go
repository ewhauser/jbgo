package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"
)

const (
	fuzzMaxScriptBytes = 4 << 10
	fuzzTimeout        = 500 * time.Millisecond
	fuzzMaxDataBytes   = 2 << 10
	fuzzWarmupTimeout  = 5 * time.Second
)

func newFuzzRuntime(tb testing.TB) *Runtime {
	tb.Helper()

	rt, err := New(WithConfig(&Config{
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
		tb.Fatalf("New() error = %v", err)
	}
	return rt
}

func runFuzzScript(t *testing.T, rt *Runtime, script []byte) (*ExecutionResult, error) {
	t.Helper()

	if len(script) > fuzzMaxScriptBytes {
		t.Skip()
	}

	return rt.Run(context.Background(), &ExecutionRequest{
		Name:    "fuzz.sh",
		Script:  string(script),
		Timeout: fuzzTimeout,
	})
}

func runFuzzSessionScript(t *testing.T, session *Session, script []byte) (*ExecutionResult, error) {
	t.Helper()

	if len(script) > fuzzMaxScriptBytes {
		t.Skip()
	}

	return session.Exec(context.Background(), &ExecutionRequest{
		Name:    "fuzz.sh",
		Script:  string(script),
		Timeout: fuzzTimeout,
	})
}

func newFuzzSession(tb testing.TB, rt *Runtime) *Session {
	tb.Helper()

	session, err := rt.NewSession(context.Background())
	if err != nil {
		tb.Fatalf("NewSession() error = %v", err)
	}
	return session
}

func assertSuccessfulFuzzExecution(t *testing.T, script []byte, result *ExecutionResult, err error) {
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

func assertBaseFuzzOutcome(t *testing.T, script []byte, result *ExecutionResult, err error) {
	t.Helper()

	if err != nil && !isExpectedFuzzError(err) {
		t.Fatalf("unexpected error %T: %v\nscript=%q", err, err, previewFuzzScript(script))
	}
	assertNoHostPathLeak(t, script, result, err)
	assertNoSensitiveDisclosure(t, script, result, err)
	assertNoInternalCrashOutput(t, script, result, err)
}

func isExpectedFuzzError(err error) bool {
	if err == nil {
		return true
	}

	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		return true
	}

	var quoteErr syntax.QuoteError
	if errors.As(err, &quoteErr) {
		return true
	}

	var langErr syntax.LangError
	return errors.As(err, &langErr)
}

func assertNoHostPathLeak(t *testing.T, script []byte, result *ExecutionResult, err error) {
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

func assertNoSensitiveDisclosure(t *testing.T, script []byte, result *ExecutionResult, err error) {
	t.Helper()

	tokens := fuzzSensitiveTokens()
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

func assertNoInternalCrashOutput(t *testing.T, script []byte, result *ExecutionResult, err error) {
	t.Helper()

	var haystacks []string
	if result != nil {
		haystacks = append(haystacks, result.Stderr)
	}
	if err != nil {
		haystacks = append(haystacks, err.Error())
	}

	for _, haystack := range haystacks {
		if containsInternalCrashOutput(haystack) {
			t.Fatalf("internal crash output detected\nscript=%q\noutput=%q", previewFuzzScript(script), haystack)
		}
	}
}

func containsInternalCrashOutput(output string) bool {
	patterns := []string{
		"panic:",
		"fatal error:",
		"runtime error:",
		"SIGSEGV",
		"stack trace",
		"goroutine ",
	}
	for _, pattern := range patterns {
		if strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

func fuzzSensitiveTokens() []string {
	tokens := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		tokens = append(tokens, value)
	}

	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
	}
	add(os.Getenv("HOME"))
	add(os.Getenv("USER"))
	add(os.Getenv("LOGNAME"))
	add(os.Getenv("SHELL"))
	add(os.Getenv("TMPDIR"))
	if hostname, err := os.Hostname(); err == nil {
		add(hostname)
	}
	return tokens
}

func previewFuzzScript(script []byte) string {
	const maxPreviewBytes = 160
	if len(script) <= maxPreviewBytes {
		return string(script)
	}
	return string(script[:maxPreviewBytes]) + "...(truncated)"
}

func clampFuzzData(data []byte) []byte {
	if len(data) <= fuzzMaxDataBytes {
		return data
	}
	return data[:fuzzMaxDataBytes]
}

func normalizeFuzzText(data []byte) []byte {
	data = clampFuzzData(data)
	text := strings.ToValidUTF8(string(data), "?")
	text = strings.ReplaceAll(text, "\x00", "\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		text = "alpha\nbeta\n"
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(text)
}

func sanitizeFuzzPathComponent(raw string) string {
	raw = strings.ToValidUTF8(raw, "")
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	if b.Len() == 0 {
		return "item"
	}
	return b.String()
}

func sanitizeFuzzToken(raw string) string {
	raw = strings.ToValidUTF8(raw, "")
	raw = strings.ReplaceAll(raw, "\x00", "")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "value"
	}
	if len(raw) > 32 {
		raw = raw[:32]
	}
	return raw
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func fuzzPath(name string) string {
	return path.Join("/tmp", sanitizeFuzzPathComponent(name))
}
