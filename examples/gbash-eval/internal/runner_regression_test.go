package gbasheval

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBashEvalCountsAgentLoopFailuresInSummary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	datasetPath := writeTempFile(t, dir, "dataset.jsonl", singleJSONLTask(EvalTask{
		ID:          "provider-fail",
		Category:    "demo",
		Description: "provider failure still counts",
		Prompt:      "echo hello",
		Expectations: []Expectation{
			{Check: "stdout_contains:hello", Weight: 1},
			{Check: "exit_code:0", Weight: 1},
		},
	}))

	provider := &fakeProvider{
		t: t,
		responses: []providerResponse{
			assistantToolResponse("call_1", "bash", map[string]any{"commands": "echo hello"}),
			{},
		},
		errs: []error{nil, errors.New("upstream boom")},
	}

	var out strings.Builder
	err := runBashEval(context.Background(), RunConfig{
		DatasetPath:  datasetPath,
		ProviderName: "fake",
		Model:        "demo",
		MaxTurns:     3,
		Save:         true,
		OutputDir:    dir,
		Moniker:      "provider-fail",
	}, provider, &out)
	if err != nil {
		t.Fatalf("runBashEval() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "eval-provider-fail-*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("saved reports = %v, want 1 JSON report", matches)
	}

	var report EvalReport
	if err := json.Unmarshal([]byte(mustReadOSFile(t, matches[0])), &report); err != nil {
		t.Fatalf("Unmarshal(report) error = %v", err)
	}

	if report.Summary.TotalTasks != 1 {
		t.Fatalf("report.Summary.TotalTasks = %d, want 1", report.Summary.TotalTasks)
	}
	if report.Summary.TotalPassed != 0 {
		t.Fatalf("report.Summary.TotalPassed = %d, want 0", report.Summary.TotalPassed)
	}
	if report.Summary.TotalToolCalls != 1 {
		t.Fatalf("report.Summary.TotalToolCalls = %d, want 1 partial call counted", report.Summary.TotalToolCalls)
	}
	if report.Results[0].Score.Score != 0 || report.Results[0].Score.MaxScore != 2 {
		t.Fatalf("score = %.1f/%.1f, want 0/2", report.Results[0].Score.Score, report.Results[0].Score.MaxScore)
	}
	if got := report.Results[0].Score.Results[0].Detail; !strings.Contains(got, "provider chat: upstream boom") {
		t.Fatalf("failure detail = %q, want provider error context", got)
	}
}

func TestRunScriptingEvalCountsExecutionFailuresInSummary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pong := "pong"
	datasetPath := writeTempFile(t, dir, "dataset.jsonl", singleJSONLTask(ScriptingEvalTask{
		ID:          "provider-fail",
		Category:    "demo",
		Description: "provider failure still counts",
		Prompt:      "call ping",
		Tools: []MockToolDef{{
			Name:        "ping",
			Description: "return pong",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
			Mock:        MockBehavior{Static: &pong},
		}},
		Expectations: []Expectation{
			{Check: "stdout_contains:pong", Weight: 1},
			{Check: "exit_code:0", Weight: 1},
		},
	}))

	provider := &fakeProvider{
		t: t,
		responses: []providerResponse{
			assistantToolResponse("call_1", "ping", map[string]any{}),
			{},
		},
		errs: []error{nil, errors.New("upstream boom")},
	}

	err := runScriptingEval(context.Background(), RunConfig{
		DatasetPath:  datasetPath,
		ProviderName: "fake",
		Model:        "demo",
		EvalType:     "scripting-tool",
		Baseline:     true,
		MaxTurns:     3,
		Save:         true,
		OutputDir:    dir,
		Moniker:      "provider-fail",
	}, provider, io.Discard)
	if err != nil {
		t.Fatalf("runScriptingEval() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "scripting-eval-baseline-provider-fail-*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("saved reports = %v, want 1 JSON report", matches)
	}

	var report ScriptingEvalReport
	if err := json.Unmarshal([]byte(mustReadOSFile(t, matches[0])), &report); err != nil {
		t.Fatalf("Unmarshal(report) error = %v", err)
	}

	if report.Summary.TotalTasks != 1 {
		t.Fatalf("report.Summary.TotalTasks = %d, want 1", report.Summary.TotalTasks)
	}
	if report.Summary.TotalPassed != 0 {
		t.Fatalf("report.Summary.TotalPassed = %d, want 0", report.Summary.TotalPassed)
	}
	if report.Summary.TotalToolCalls != 1 {
		t.Fatalf("report.Summary.TotalToolCalls = %d, want 1 partial call counted", report.Summary.TotalToolCalls)
	}
	if report.Results[0].Score.Score != 0 || report.Results[0].Score.MaxScore != 2 {
		t.Fatalf("score = %.1f/%.1f, want 0/2", report.Results[0].Score.Score, report.Results[0].Score.MaxScore)
	}
}

func TestCompatTraceFromScriptingPreservesStderr(t *testing.T) {
	t.Parallel()

	trace := compatTraceFromScripting(ScriptingTrace{
		ToolCalls: []ScriptingToolCall{{
			ToolName: "ping",
			Input:    map[string]any{"value": "x"},
			Output:   "partial output",
			Stderr:   "warning: partial output",
			ExitCode: 1,
		}},
	})

	if got := trace.ToolCalls[0].Stderr; got != "warning: partial output" {
		t.Fatalf("compat stderr = %q, want warning: partial output", got)
	}

	score := scoreTask(context.Background(), "stderr-check", trace, nil, []Expectation{{Check: "stderr_empty", Weight: 1}})
	if score.AllPassed() {
		t.Fatalf("score.AllPassed() = true, want stderr_empty failure")
	}
	if got := score.Results[0].Detail; !strings.Contains(got, "warning: partial output") {
		t.Fatalf("stderr_empty detail = %q, want stderr content", got)
	}
}

func TestFailureScoreWithoutExpectationsStillFails(t *testing.T) {
	t.Parallel()

	score := failureScore("no-expectations", nil, errors.New("boom"))

	if score.AllPassed() {
		t.Fatal("score.AllPassed() = true, want explicit task failure")
	}
	if len(score.Results) != 1 {
		t.Fatalf("len(score.Results) = %d, want 1", len(score.Results))
	}
	if got := score.Results[0].Check; got != "task_error" {
		t.Fatalf("score.Results[0].Check = %q, want task_error", got)
	}
	if score.Results[0].Passed {
		t.Fatal("score.Results[0].Passed = true, want false")
	}
	if got := score.Results[0].Weight; got != 0 {
		t.Fatalf("score.Results[0].Weight = %v, want 0", got)
	}
}
