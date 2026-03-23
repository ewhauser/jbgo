package gbasheval

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadDatasetSkipsCommentsAndNormalizesDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeTempFile(t, dir, "dataset.jsonl", `# comment
// another comment

{"id":"task-1","category":"demo","description":"demo task","prompt":"echo hello","expectations":[{"check":"stdout_contains:hello"}]}
`)

	tasks, err := loadDataset(path)
	if err != nil {
		t.Fatalf("loadDataset() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Files == nil || len(task.Files) != 0 {
		t.Fatalf("task.Files = %#v, want empty map", task.Files)
	}
	if got := task.Expectations[0].Weight; got != 1 {
		t.Fatalf("expectation weight = %v, want 1", got)
	}
}

func TestLoadScriptingDatasetParsesMockVariantsAndSchemaDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeTempFile(t, dir, "scripting.jsonl", `{"id":"task-1","category":"demo","description":"demo task","prompt":"do something","tools":[{"name":"say_hello","description":"Say hello","mock":"hello world"},{"name":"lookup_user","description":"Lookup a user","schema":{"properties":{"account_ref":{"type":"string"}}},"mock":{"param":"account_ref","responses":{"C-1":"{\"name\":\"alice\"}"},"default":"{}"}}],"expectations":[{"check":"exit_code:0"}]}
`)

	tasks, err := loadScriptingDataset(path)
	if err != nil {
		t.Fatalf("loadScriptingDataset() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Files == nil || len(task.Files) != 0 {
		t.Fatalf("task.Files = %#v, want empty map", task.Files)
	}
	if got := task.Expectations[0].Weight; got != 1 {
		t.Fatalf("expectation weight = %v, want 1", got)
	}
	if task.Tools[0].Mock.Static == nil || *task.Tools[0].Mock.Static != "hello world" {
		t.Fatalf("static mock = %#v, want hello world", task.Tools[0].Mock.Static)
	}
	if task.Tools[1].Mock.Param != "account_ref" {
		t.Fatalf("mock param = %q, want account_ref", task.Tools[1].Mock.Param)
	}
	if got := asString(task.Tools[1].Schema["type"]); got != "object" {
		t.Fatalf("schema type = %q, want object", got)
	}
	if task.Tools[1].Tags == nil {
		t.Fatal("tool tags should be normalized to an empty slice")
	}
}

func TestVendoredDatasetsLoad(t *testing.T) {
	t.Parallel()

	bashTasks, err := loadDataset(filepath.Join(DefaultDataDir(), "smoke-test.jsonl"))
	if err != nil {
		t.Fatalf("loadDataset(smoke-test) error = %v", err)
	}
	if len(bashTasks) == 0 {
		t.Fatal("smoke-test dataset should not be empty")
	}

	scriptingTasks, err := loadScriptingDataset(filepath.Join(DefaultDataDir(), "scripting-tool", "discovery.jsonl"))
	if err != nil {
		t.Fatalf("loadScriptingDataset(discovery) error = %v", err)
	}
	if len(scriptingTasks) == 0 {
		t.Fatal("discovery dataset should not be empty")
	}
}

func TestVendoredEvalDatasetUsesGbashSystemInfoIdentity(t *testing.T) {
	t.Parallel()

	tasks, err := loadDataset(filepath.Join(DefaultDataDir(), "eval-tasks.jsonl"))
	if err != nil {
		t.Fatalf("loadDataset(eval-tasks) error = %v", err)
	}

	for _, task := range tasks {
		if task.ID != "sysinfo_env_report" {
			continue
		}
		checks := make([]string, 0, len(task.Expectations))
		for _, exp := range task.Expectations {
			checks = append(checks, exp.Check)
		}
		if !slices.Contains(checks, "stdout_contains:user: agent") {
			t.Fatalf("sysinfo_env_report checks = %#v, want user: agent expectation", checks)
		}
		if !slices.Contains(checks, "stdout_contains:host: gbash") {
			t.Fatalf("sysinfo_env_report checks = %#v, want host: gbash expectation", checks)
		}
		if !strings.Contains(task.Prompt, "single bash command or short bash script") {
			t.Fatalf("sysinfo_env_report prompt = %q, want single-command guidance", task.Prompt)
		}
		if !strings.Contains(task.Prompt, "bash tool output") {
			t.Fatalf("sysinfo_env_report prompt = %q, want bash-tool-output guidance", task.Prompt)
		}
		return
	}

	t.Fatal("sysinfo_env_report task not found in eval-tasks.jsonl")
}
