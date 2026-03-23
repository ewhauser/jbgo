package gbasheval

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestFilterTasksByIDReturnsDatasetOrder(t *testing.T) {
	t.Parallel()

	tasks := []EvalTask{
		{ID: "first"},
		{ID: "second"},
		{ID: "third"},
	}

	filtered, err := filterTasksByID(tasks, []string{"third", "first"}, func(task EvalTask) string { return task.ID })
	if err != nil {
		t.Fatalf("filterTasksByID() error = %v", err)
	}

	if got := len(filtered); got != 2 {
		t.Fatalf("len(filtered) = %d, want 2", got)
	}
	if filtered[0].ID != "first" || filtered[1].ID != "third" {
		t.Fatalf("filtered IDs = [%s %s], want [first third]", filtered[0].ID, filtered[1].ID)
	}
}

func TestFilterTasksByIDRejectsMissingIDs(t *testing.T) {
	t.Parallel()

	_, err := filterTasksByID([]EvalTask{{ID: "first"}}, []string{"missing"}, func(task EvalTask) string { return task.ID })
	if err == nil || !strings.Contains(err.Error(), "no tasks matched requested IDs") {
		t.Fatalf("filterTasksByID() error = %v, want no-tasks-matched error", err)
	}

	_, err = filterTasksByID([]EvalTask{{ID: "first"}}, []string{"first", "missing"}, func(task EvalTask) string { return task.ID })
	if err == nil || !strings.Contains(err.Error(), "task IDs not found in dataset: missing") {
		t.Fatalf("filterTasksByID() error = %v, want missing-id error", err)
	}
}

func TestRunBashEvalFiltersRequestedTasks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	datasetPath := writeTempFile(t, dir, "dataset.jsonl", `{"id":"first","category":"demo","description":"first task","prompt":"echo first","expectations":[{"check":"stdout_contains:first"},{"check":"exit_code:0"}]}
{"id":"second","category":"demo","description":"second task","prompt":"echo second","expectations":[{"check":"stdout_contains:second"},{"check":"exit_code:0"}]}
`)

	provider := &fakeProvider{
		t: t,
		hook: func(call int, messages []message, _ []toolDefinition, _ string) {
			if call != 0 {
				return
			}
			if got := messages[0].Content[0].Text; got != "echo second" {
				t.Fatalf("provider saw prompt %q, want second task prompt", got)
			}
		},
		responses: []providerResponse{
			assistantToolResponse("call_1", "bash", map[string]any{"commands": "echo second"}),
			assistantStopResponse(),
		},
	}

	cfg := RunConfig{
		DatasetPath:  datasetPath,
		ProviderName: "fake",
		Model:        "demo",
		MaxTurns:     2,
		TaskIDs:      []string{"second"},
	}

	var out strings.Builder
	if err := runBashEval(context.Background(), cfg, provider, &out); err != nil {
		t.Fatalf("runBashEval() error = %v", err)
	}

	if got := provider.calls; got != 2 {
		t.Fatalf("provider.calls = %d, want 2", got)
	}
	if got := out.String(); !strings.Contains(got, "Running 1 tasks") || !strings.Contains(got, "[1/1] second - second task") {
		t.Fatalf("run output = %q, want filtered task output", got)
	}
}

func TestRunScriptingEvalFiltersRequestedTasks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	datasetPath := writeTempFile(t, dir, "dataset.jsonl", `{"id":"first","category":"demo","description":"first task","prompt":"call first","tools":[{"name":"first_tool","description":"first","schema":{"type":"object","properties":{}},"mock":"first"}],"expectations":[{"check":"stdout_contains:first"},{"check":"exit_code:0"}]}
{"id":"second","category":"demo","description":"second task","prompt":"call second","tools":[{"name":"second_tool","description":"second","schema":{"type":"object","properties":{}},"mock":"second"}],"expectations":[{"check":"stdout_contains:second"},{"check":"exit_code:0"}]}
`)

	provider := &fakeProvider{
		t: t,
		hook: func(call int, messages []message, tools []toolDefinition, _ string) {
			if call != 0 {
				return
			}
			if got := messages[0].Content[0].Text; got != "call second" {
				t.Fatalf("provider saw prompt %q, want second task prompt", got)
			}
			if len(tools) != 1 || tools[0].Name != "second" {
				t.Fatalf("tools = %#v, want scripted tool for second task", tools)
			}
		},
		responses: []providerResponse{
			assistantToolResponse("call_1", "second", map[string]any{"commands": "second_tool"}),
			assistantStopResponse(),
		},
	}

	cfg := RunConfig{
		DatasetPath:  datasetPath,
		ProviderName: "fake",
		Model:        "demo",
		EvalType:     "scripting-tool",
		MaxTurns:     2,
		TaskIDs:      []string{"second"},
	}

	var out strings.Builder
	if err := runScriptingEval(context.Background(), cfg, provider, io.Discard); err != nil {
		t.Fatalf("runScriptingEval() error = %v", err)
	}

	if got := provider.calls; got != 2 {
		t.Fatalf("provider.calls = %d, want 2", got)
	}
	_ = out
}
