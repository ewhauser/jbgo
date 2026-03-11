package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cadencerpm/just-bash-go/policy"
	"mvdan.cc/sh/v3/syntax"
)

const (
	fuzzMaxScriptBytes = 4 << 10
	fuzzTimeout        = 50 * time.Millisecond
)

func newFuzzRuntime(tb testing.TB) *Runtime {
	tb.Helper()

	rt, err := New(&Config{
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
	})
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

func assertExpectedFuzzOutcome(t *testing.T, script []byte, result *ExecutionResult, err error) {
	t.Helper()

	if err != nil && !isExpectedFuzzError(err) {
		t.Fatalf("unexpected error %T: %v\nscript=%q", err, err, previewFuzzScript(script))
	}
	assertNoHostPathLeak(t, script, result, err)
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

func previewFuzzScript(script []byte) string {
	const maxPreviewBytes = 160
	if len(script) <= maxPreviewBytes {
		return string(script)
	}
	return string(script[:maxPreviewBytes]) + "...(truncated)"
}
