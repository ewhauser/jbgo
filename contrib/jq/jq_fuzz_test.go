package jq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/policy"
)

const (
	fuzzMaxDataBytes   = 4 << 10
	fuzzMaxScriptBytes = 4 << 10
	fuzzTimeout        = 1 * time.Second
)

func newFuzzRuntime(tb testing.TB) *gbruntime.Runtime {
	tb.Helper()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{
		Registry: newJQRegistry(tb),
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
		Name:    "jq-fuzz.sh",
		Script:  string(script),
		Timeout: fuzzTimeout,
	})
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

func clampFuzzData(data []byte) []byte {
	if len(data) <= fuzzMaxDataBytes {
		return data
	}
	return data[:fuzzMaxDataBytes]
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

func addStructuredDataSeeds(f *testing.F) {
	f.Helper()

	seeds := []struct {
		value string
		raw   []byte
	}{
		{"alpha", []byte(`{"value":"alpha","items":[1,2,3]}`)},
		{"beta", []byte(`{"value":"beta","items":["x","y"]}`)},
		{"gamma", []byte(`not-json`)},
	}
	for _, seed := range seeds {
		f.Add(seed.value, seed.raw)
	}
}

func prepareStructuredDataFixtures(t *testing.T, session *gbruntime.Session, rawValue string, rawJSON []byte) string {
	t.Helper()

	value := sanitizeFuzzToken(rawValue)
	validDoc := map[string]any{
		"value": value,
		"items": []string{value, strings.ToUpper(value)},
	}
	validBytes, err := json.Marshal(validDoc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	writeSessionFile(t, session, "/tmp/input.json", validBytes)
	writeSessionFile(t, session, "/tmp/raw.json", clampFuzzData(rawJSON))
	return value
}

func FuzzJQCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	addStructuredDataSeeds(f)

	f.Fuzz(func(t *testing.T, rawValue string, rawJSON []byte) {
		session := newFuzzSession(t, rt)
		value := prepareStructuredDataFixtures(t, session, rawValue, rawJSON)

		script := fmt.Appendf(nil,
			"jq -r '.value' /tmp/input.json >/tmp/jq-value.txt\n"+
				"jq -c '.items' /tmp/input.json >/tmp/jq-items.txt\n"+
				"jq -n --arg value %s '{value:$value}' >/tmp/jq-build.txt\n"+
				"jq '.value' /tmp/raw.json >/tmp/jq-raw.txt || true\n",
			shellQuote(value),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
