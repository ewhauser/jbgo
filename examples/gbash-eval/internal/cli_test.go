package gbasheval

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestParseRunFlagsRejectsInvalidCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing dataset",
			args: []string{"--provider", "openai", "--model", "gpt-4.1"},
			want: "--dataset is required",
		},
		{
			name: "invalid eval type",
			args: []string{"--dataset", "tasks.jsonl", "--provider", "openai", "--model", "gpt-4.1", "--eval-type", "nope"},
			want: "unknown eval type",
		},
		{
			name: "baseline only for scripting",
			args: []string{"--dataset", "tasks.jsonl", "--provider", "openai", "--model", "gpt-4.1", "--baseline"},
			want: "--baseline is only valid",
		},
		{
			name: "max turns positive",
			args: []string{"--dataset", "tasks.jsonl", "--provider", "openai", "--model", "gpt-4.1", "--max-turns", "0"},
			want: "--max-turns must be positive",
		},
		{
			name: "unexpected positional args",
			args: []string{"--dataset", "tasks.jsonl", "--provider", "openai", "--model", "gpt-4.1", "extra"},
			want: "unexpected positional arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseRunFlags(tc.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseRunFlags(%v) error = %v, want substring %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestRunCLIRoutesRunCommandWithParsedConfig(t *testing.T) {
	t.Parallel()

	var captured RunConfig
	prev := runCLIExecutor
	runCLIExecutor = func(_ context.Context, cfg RunConfig, _, _ io.Writer) error {
		captured = cfg
		return nil
	}
	defer func() { runCLIExecutor = prev }()

	err := RunCLI(context.Background(), []string{
		"run",
		"--dataset", "/tmp/tasks.jsonl",
		"--provider", "openresponses",
		"--model", "gpt-5-codex",
		"--eval-type", "scripting-tool",
		"--baseline",
		"--max-turns", "7",
		"--save",
		"--output", "/tmp/results",
		"--moniker", "demo",
		"--task", "alpha",
		"--task", "beta,gamma",
	}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("RunCLI() error = %v", err)
	}

	if captured.DatasetPath != "/tmp/tasks.jsonl" ||
		captured.ProviderName != "openresponses" ||
		captured.Model != "gpt-5-codex" ||
		captured.EvalType != "scripting-tool" ||
		!captured.Baseline ||
		captured.MaxTurns != 7 ||
		!captured.Save ||
		captured.OutputDir != "/tmp/results" ||
		captured.Moniker != "demo" {
		t.Fatalf("captured cfg = %#v", captured)
	}
	if got, want := strings.Join(captured.TaskIDs, ","), "alpha,beta,gamma"; got != want {
		t.Fatalf("captured.TaskIDs = %q, want %q", got, want)
	}
}
