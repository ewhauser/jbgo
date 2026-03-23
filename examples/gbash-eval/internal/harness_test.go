package gbasheval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAgentLoopPersistsFilesystemAcrossTurns(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		t: t,
		responses: []providerResponse{
			assistantToolResponse("call_1", "bash", map[string]any{"commands": "mkdir -p /tmp && printf hello >/tmp/note.txt"}),
			assistantToolResponse("call_2", "bash", map[string]any{"commands": "cat /tmp/note.txt"}),
			assistantStopResponse(),
		},
	}

	trace, fsys, err := runAgentLoop(context.Background(), provider, EvalTask{
		ID:          "persist",
		Category:    "demo",
		Description: "persist across turns",
		Prompt:      "create a file and read it back",
	}, 5)
	if err != nil {
		t.Fatalf("runAgentLoop() error = %v", err)
	}

	if trace.ToolCallCount != 2 {
		t.Fatalf("trace.ToolCallCount = %d, want 2", trace.ToolCallCount)
	}
	data, err := readFile(context.Background(), fsys, "/tmp/note.txt")
	if err != nil {
		t.Fatalf("readFile(/tmp/note.txt) error = %v", err)
	}
	if got := string(data); got != "hello" {
		t.Fatalf("file contents = %q, want hello", got)
	}
	if got := trace.ToolCalls[1].Stdout; strings.TrimSpace(got) != "hello" {
		t.Fatalf("second tool stdout = %q, want hello", got)
	}
}

func TestRunAgentLoopSupportsExtrasCommands(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		t: t,
		responses: []providerResponse{
			assistantToolResponse("call_1", "bash", map[string]any{"commands": `printf '{"name":"alice"}' | jq -r '.name'`}),
			assistantStopResponse(),
		},
	}

	trace, _, err := runAgentLoop(context.Background(), provider, EvalTask{
		ID:          "extras-jq",
		Category:    "demo",
		Description: "jq is available",
		Prompt:      "read JSON with jq",
	}, 3)
	if err != nil {
		t.Fatalf("runAgentLoop() error = %v", err)
	}

	if trace.ToolCallCount != 1 {
		t.Fatalf("trace.ToolCallCount = %d, want 1", trace.ToolCallCount)
	}
	if got := strings.TrimSpace(trace.ToolCalls[0].Stdout); got != "alice" {
		t.Fatalf("tool stdout = %q, want alice", got)
	}
	if got := trace.ToolCalls[0].ExitCode; got != 0 {
		t.Fatalf("tool exit code = %d, want 0; stderr=%q", got, trace.ToolCalls[0].Stderr)
	}
}

func TestRunAgentLoopUsesGbashIdentity(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		t: t,
		responses: []providerResponse{
			assistantToolResponse("call_1", "bash", map[string]any{
				"commands": `printf 'user: %s
host: %s
cwd: %s
' "$(whoami)" "$(hostname)" "$(pwd)"`,
			}),
			assistantStopResponse(),
		},
	}

	trace, _, err := runAgentLoop(context.Background(), provider, EvalTask{
		ID:          "identity",
		Category:    "system_info",
		Description: "report eval identity",
		Prompt:      "print the current user, host, and cwd",
	}, 3)
	if err != nil {
		t.Fatalf("runAgentLoop() error = %v", err)
	}

	if trace.ToolCallCount != 1 {
		t.Fatalf("trace.ToolCallCount = %d, want 1", trace.ToolCallCount)
	}
	got := trace.ToolCalls[0].Stdout
	if !strings.Contains(got, "user: agent") {
		t.Fatalf("stdout = %q, want user: agent", got)
	}
	if !strings.Contains(got, "host: gbash") {
		t.Fatalf("stdout = %q, want host: gbash", got)
	}
	if !strings.Contains(got, "cwd: /home/agent") {
		t.Fatalf("stdout = %q, want cwd: /home/agent", got)
	}
}

func TestRunScriptedAgentTracksDiscoverAndHelpJSON(t *testing.T) {
	t.Parallel()

	staticResponse := `{"ticket_id":"TK-5001","status":"open"}`
	task := ScriptingEvalTask{
		ID:            "scripted-discovery",
		Category:      "discovery",
		Description:   "discover then call a tool",
		Prompt:        "create a ticket",
		DiscoveryMode: true,
		Tools: []MockToolDef{
			{
				Name:        "create_ticket",
				Description: "Create a ticket",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"account_ref":    map[string]any{"type": "string"},
						"ticket_subject": map[string]any{"type": "string"},
						"severity_level": map[string]any{"type": "string"},
						"details":        map[string]any{"type": "string"},
					},
				},
				Category: "support",
				Tags:     []string{"write"},
				Mock: MockBehavior{
					Static: &staticResponse,
				},
			},
		},
		Expectations: []Expectation{{Check: "stdout_contains:TK-5001"}},
	}

	provider := &fakeProvider{
		t: t,
		hook: func(call int, _ []message, tools []toolDefinition, system string) {
			if len(tools) != 1 || tools[0].Name != task.ID {
				t.Fatalf("tools = %#v, want single scripted tool named %q", tools, task.ID)
			}
			if !strings.Contains(system, "discover --categories") {
				t.Fatalf("system prompt = %q, want discover guidance", system)
			}
			_ = call
		},
		responses: []providerResponse{
			assistantToolResponse("call_1", task.ID, map[string]any{"commands": "discover --category support"}),
			assistantToolResponse("call_2", task.ID, map[string]any{"commands": "help create_ticket --json"}),
			assistantToolResponse("call_3", task.ID, map[string]any{"commands": `create_ticket --account_ref C-50 --ticket_subject "Login broken" --severity_level high --details "Login broken"`}),
			assistantStopResponse(),
		},
	}

	trace, err := runScriptedAgent(context.Background(), provider, task, 6)
	if err != nil {
		t.Fatalf("runScriptedAgent() error = %v", err)
	}

	if trace.ToolCallCount != 3 {
		t.Fatalf("trace.ToolCallCount = %d, want 3", trace.ToolCallCount)
	}
	if got := trace.InnerCommandCountByKind(ScriptedCommandKindDiscover); got != 1 {
		t.Fatalf("discover count = %d, want 1", got)
	}
	if got := trace.InnerCommandCountByKind(ScriptedCommandKindHelp); got != 1 {
		t.Fatalf("help count = %d, want 1", got)
	}
	if got := trace.InnerCommandCountByKind(ScriptedCommandKindTool); got != 1 {
		t.Fatalf("tool count = %d, want 1", got)
	}
	if got := trace.ToolCalls[1].Output; !strings.Contains(got, `"input_schema"`) {
		t.Fatalf("help output = %q, want JSON schema", got)
	}
	if got := trace.ToolCalls[2].Output; !strings.Contains(got, "TK-5001") {
		t.Fatalf("tool output = %q, want ticket id", got)
	}
}

func TestRunBaselineAgentCallsMockToolsDirectly(t *testing.T) {
	t.Parallel()

	task := ScriptingEvalTask{
		ID:          "baseline-tool",
		Category:    "inventory",
		Description: "call a mock tool directly",
		Prompt:      "check stock",
		Tools: []MockToolDef{
			{
				Name:        "check_inventory",
				Description: "Check inventory",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"product_code": map[string]any{"type": "string"},
					},
				},
				Mock: MockBehavior{
					Param:     "product_code",
					Responses: map[string]string{"SKU-200": `{"sku":"SKU-200","quantity":142}`},
				},
			},
		},
		Expectations: []Expectation{{Check: "stdout_contains:142"}},
	}

	provider := &fakeProvider{
		t: t,
		hook: func(call int, _ []message, tools []toolDefinition, system string) {
			if call == 0 {
				if len(tools) != 1 || tools[0].Name != "check_inventory" {
					t.Fatalf("tools = %#v, want direct baseline tool", tools)
				}
				if !strings.Contains(system, "check_inventory") {
					t.Fatalf("system prompt = %q, want tool listing", system)
				}
			}
		},
		responses: []providerResponse{
			assistantToolResponse("call_1", "check_inventory", map[string]any{"product_code": "SKU-200"}),
			assistantStopResponse(),
		},
	}

	trace, err := runBaselineAgent(context.Background(), provider, task, 4)
	if err != nil {
		t.Fatalf("runBaselineAgent() error = %v", err)
	}

	if trace.ToolCallCount != 1 {
		t.Fatalf("trace.ToolCallCount = %d, want 1", trace.ToolCallCount)
	}
	if trace.InnerCommandCount() != 1 {
		t.Fatalf("trace.InnerCommandCount() = %d, want 1", trace.InnerCommandCount())
	}
	call := trace.ToolCalls[0]
	if call.ToolName != "check_inventory" {
		t.Fatalf("tool name = %q, want check_inventory", call.ToolName)
	}
	if len(call.Invocations) != 1 || call.Invocations[0].Kind != ScriptedCommandKindTool {
		t.Fatalf("invocations = %#v, want one tool invocation", call.Invocations)
	}
	if !strings.Contains(call.Output, "SKU-200") {
		t.Fatalf("tool output = %q, want SKU-200 payload", call.Output)
	}
}

func TestRunBashEvalSavesReportsWithExpectedFilenames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	datasetPath := writeTempFile(t, dir, "dataset.jsonl", `{"id":"save-bash","category":"demo","description":"save report","prompt":"create then read file","expectations":[{"check":"stdout_contains:hello"},{"check":"file_exists:/tmp/note.txt"},{"check":"file_contains:/tmp/note.txt:hello"},{"check":"exit_code:0"}]}
`)

	provider := &fakeProvider{
		t: t,
		responses: []providerResponse{
			assistantToolResponse("call_1", "bash", map[string]any{"commands": "mkdir -p /tmp && printf hello >/tmp/note.txt"}),
			assistantToolResponse("call_2", "bash", map[string]any{"commands": "cat /tmp/note.txt"}),
			assistantStopResponse(),
		},
	}

	var out strings.Builder
	err := runBashEval(context.Background(), RunConfig{
		DatasetPath:  datasetPath,
		ProviderName: "fake",
		Model:        "model",
		MaxTurns:     5,
		Save:         true,
		OutputDir:    dir,
		Moniker:      "demo",
	}, provider, &out)
	if err != nil {
		t.Fatalf("runBashEval() error = %v", err)
	}

	jsonMatches, err := filepath.Glob(filepath.Join(dir, "eval-demo-*.json"))
	if err != nil {
		t.Fatalf("Glob(json) error = %v", err)
	}
	mdMatches, err := filepath.Glob(filepath.Join(dir, "eval-demo-*.md"))
	if err != nil {
		t.Fatalf("Glob(md) error = %v", err)
	}
	if len(jsonMatches) != 1 || len(mdMatches) != 1 {
		t.Fatalf("saved files = %v / %v, want one JSON and one Markdown report", jsonMatches, mdMatches)
	}
	if !strings.Contains(out.String(), "Saved JSON:") || !strings.Contains(out.String(), "Saved Markdown:") {
		t.Fatalf("stdout = %q, want saved report messages", out.String())
	}
}

func TestRunScriptingEvalSavesReportsWithExpectedFilenames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	datasetPath := writeTempFile(t, dir, "scripting.jsonl", `{"id":"save-script","category":"demo","description":"save scripting report","prompt":"call the tool","tools":[{"name":"ping","description":"Return pong","schema":{"type":"object","properties":{}},"mock":"pong"}],"expectations":[{"check":"stdout_contains:pong"},{"check":"exit_code:0"}]}
`)

	provider := &fakeProvider{
		t: t,
		responses: []providerResponse{
			assistantToolResponse("call_1", "ping", map[string]any{}),
			assistantStopResponse(),
		},
	}

	var out strings.Builder
	err := runScriptingEval(context.Background(), RunConfig{
		DatasetPath:  datasetPath,
		ProviderName: "fake",
		Model:        "model",
		EvalType:     "scripting-tool",
		Baseline:     true,
		MaxTurns:     4,
		Save:         true,
		OutputDir:    dir,
		Moniker:      "demo",
	}, provider, &out)
	if err != nil {
		t.Fatalf("runScriptingEval() error = %v", err)
	}

	jsonMatches, err := filepath.Glob(filepath.Join(dir, "scripting-eval-baseline-demo-*.json"))
	if err != nil {
		t.Fatalf("Glob(json) error = %v", err)
	}
	mdMatches, err := filepath.Glob(filepath.Join(dir, "scripting-eval-baseline-demo-*.md"))
	if err != nil {
		t.Fatalf("Glob(md) error = %v", err)
	}
	if len(jsonMatches) != 1 || len(mdMatches) != 1 {
		t.Fatalf("saved files = %v / %v, want one JSON and one Markdown report", jsonMatches, mdMatches)
	}
	if !strings.Contains(out.String(), "Saved JSON:") || !strings.Contains(out.String(), "Saved Markdown:") {
		t.Fatalf("stdout = %q, want saved report messages", out.String())
	}
}
