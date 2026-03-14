package runtime

import (
	"context"
	"testing"
)

func TestWorkflowCodebaseExploration(t *testing.T) {
	results := runCodebaseExplorationWorkflow(t)

	assertExecutionOutcome(t, results[0], 0, "", "")
	assertExecutionOutcome(t, results[1], 0, ""+
		"src/app/main.go\n"+
		"src/lib/math.go\n"+
		"/home/agent/project/src/app/main.go:// TODO: wire config\n"+
		"/home/agent/project/src/lib/math.go:// TODO: add tests\n"+
		"2\n"+
		"2\n", "")
}

func TestWorkflowRefactorPreparation(t *testing.T) {
	results := runRefactorPreparationWorkflow(t)

	assertExecutionOutcome(t, results[0], 0, "", "")
	assertExecutionOutcome(t, results[1], 0, ""+
		"snapshot/module/a.txt\n"+
		"snapshot/module/b.txt\n"+
		"/home/agent/project/notes/TODO.md:TODO: rewrite readme\n", "")
}

func runCodebaseExplorationWorkflow(t *testing.T) []normalizedExecutionResult {
	t.Helper()

	session := newSession(t, &Config{})
	seedSessionFiles(t, session, map[string]string{
		"/home/agent/project/src/app/main.go": "package main\n// TODO: wire config\nfunc main() {}\n",
		"/home/agent/project/src/lib/math.go": "package lib\n// TODO: add tests\nfunc Sum() int { return 1 }\n",
	})

	return execWorkflow(t, session, []*ExecutionRequest{
		{
			WorkDir: "/home/agent/project",
			Script: "" +
				"find src -type f > inventory.txt\n" +
				"grep -r \"TODO\" src > todos.txt\n",
		},
		{
			WorkDir: "/home/agent/project",
			Script: "" +
				"grep -c \"\\\\.go\" inventory.txt > inventory.count\n" +
				"grep -c \"TODO\" todos.txt > todos.count\n" +
				"cat inventory.txt\n" +
				"cat todos.txt\n" +
				"cat inventory.count\n" +
				"cat todos.count\n",
		},
	})
}

func runRefactorPreparationWorkflow(t *testing.T) []normalizedExecutionResult {
	t.Helper()

	session := newSession(t, &Config{})
	seedSessionFiles(t, session, map[string]string{
		"/home/agent/project/docs/TODO.md":         "TODO: rewrite readme\n",
		"/home/agent/project/src/module/a.txt":     "alpha\n",
		"/home/agent/project/src/module/b.txt":     "beta\n",
		"/home/agent/project/src/module/readme.md": "notes\n",
	})

	return execWorkflow(t, session, []*ExecutionRequest{
		{
			WorkDir: "/home/agent/project",
			Script: "" +
				"cp -r src snapshot\n" +
				"mv docs notes\n" +
				"find snapshot -name \"*.txt\" -type f > snapshot.files\n" +
				"grep -r \"TODO\" notes > notes.todo\n",
		},
		{
			WorkDir: "/home/agent/project",
			Script: "" +
				"cat snapshot.files\n" +
				"cat notes.todo\n",
		},
	})
}

func execWorkflow(t *testing.T, session *Session, requests []*ExecutionRequest) []normalizedExecutionResult {
	t.Helper()

	results := make([]normalizedExecutionResult, 0, len(requests))
	for i, req := range requests {
		result, err := session.Exec(context.Background(), req)
		if err != nil {
			t.Fatalf("Exec(step %d) error = %v", i+1, err)
		}
		results = append(results, normalizeResult(result))
	}
	return results
}
