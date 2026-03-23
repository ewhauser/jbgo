package gbasheval

import (
	"context"
	"os"
	"strings"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestScoreTaskSupportsCoreChecks(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	ctx := context.Background()
	if err := fsys.MkdirAll(ctx, "/workspace/out", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file, err := fsys.OpenFile(ctx, "/workspace/out/report.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.Write([]byte("hello report\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	call := toolCallResult{
		Stdout:   "hello report\ncount=3",
		Stderr:   "",
		ExitCode: 0,
	}
	trace := agentTrace{
		ToolCalls:        []toolCallResult{call},
		ToolCallCount:    1,
		LastToolResponse: &call,
	}

	score := scoreTask(context.Background(), "task-1", trace, fsys, []Expectation{
		{Check: "exit_code:0", Weight: 1},
		{Check: "stdout_contains:count=3", Weight: 1},
		{Check: "stdout_regex:hello\\s+report", Weight: 1},
		{Check: "stderr_empty", Weight: 1},
		{Check: "file_exists:/workspace/out/report.txt", Weight: 1},
		{Check: "dir_exists:/workspace/out", Weight: 1},
		{Check: "file_contains:/workspace/out/report.txt:hello report", Weight: 1},
	})

	if !score.AllPassed() {
		t.Fatalf("score.AllPassed() = false, results = %#v", score.Results)
	}
	if score.Score != score.MaxScore {
		t.Fatalf("score = %.1f/%.1f, want full score", score.Score, score.MaxScore)
	}
}

func TestScoreTaskReportsRegexAndFilesystemFailures(t *testing.T) {
	t.Parallel()

	call := toolCallResult{
		Stdout:   "nothing useful here",
		Stderr:   "warning: partial output",
		ExitCode: 7,
	}
	trace := agentTrace{
		ToolCalls:        []toolCallResult{call},
		ToolCallCount:    1,
		LastToolResponse: &call,
	}

	score := scoreTask(context.Background(), "task-2", trace, nil, []Expectation{
		{Check: "stdout_regex:[", Weight: 1},
		{Check: "stdout_regex:must-match", Weight: 1},
		{Check: "stderr_empty", Weight: 1},
		{Check: "file_exists:/missing.txt", Weight: 1},
		{Check: "dir_exists:/missing-dir", Weight: 1},
		{Check: "file_contains:/missing.txt:hello", Weight: 1},
	})

	if score.AllPassed() {
		t.Fatal("score.AllPassed() = true, want failures")
	}
	got := make([]string, 0, len(score.Results))
	for _, result := range score.Results {
		got = append(got, result.Detail)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"invalid regex", "not matched", "stderr:", "filesystem is nil"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("combined details missing %q:\n%s", want, joined)
		}
	}
}
