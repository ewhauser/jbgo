//nolint:gocritic // Internal evaluator wiring favors simple value semantics over pointer-heavy plumbing.
package gbasheval

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
)

func Run(ctx context.Context, cfg RunConfig, stdout, stderr io.Writer) error {
	provider, err := createProvider(cfg.ProviderName, cfg.Model, stderr)
	if err != nil {
		return err
	}
	if cfg.EvalType == "scripting-tool" {
		return runScriptingEval(ctx, cfg, provider, stdout)
	}
	return runBashEval(ctx, cfg, provider, stdout)
}

func runBashEval(ctx context.Context, cfg RunConfig, provider Provider, stdout io.Writer) error {
	tasks, err := loadDataset(cfg.DatasetPath)
	if err != nil {
		return err
	}
	tasks, err = filterTasksByID(tasks, cfg.TaskIDs, func(task EvalTask) string { return task.ID })
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "Running %d tasks with %s/%s  (max_turns=%d)\n\n", len(tasks), cfg.ProviderName, cfg.Model, cfg.MaxTurns)

	results := make([]EvalResult, 0, len(tasks))
	for i, task := range tasks {
		_, _ = fmt.Fprintf(stdout, "[%d/%d] %s - %s\n", i+1, len(tasks), task.ID, task.Description)

		trace, fsys, err := runAgentLoop(ctx, provider, task, cfg.MaxTurns)
		if err != nil {
			_, _ = fmt.Fprintf(stdout, "  ERROR: %v\n\n", err)
			results = append(results, EvalResult{
				Task:  task,
				Trace: trace,
				Score: failureScore(task.ID, task.Expectations, err),
			})
			continue
		}
		score := scoreTask(ctx, task.ID, trace, fsys, task.Expectations)
		for _, result := range score.Results {
			icon := "FAIL"
			if result.Passed {
				icon = "PASS"
			}
			_, _ = fmt.Fprintf(stdout, "  [%s] %s - %s\n", icon, result.Check, result.Detail)
		}
		callsOK := 0
		for _, call := range trace.ToolCalls {
			if call.ExitCode == 0 {
				callsOK++
			}
		}
		_, _ = fmt.Fprintf(stdout, "  Score: %.0f/%.0f | Turns: %d | Calls: %d (%d ok, %d err) | Tokens: %din/%dout | %.1fs\n\n",
			score.Score, score.MaxScore, trace.Turns, trace.ToolCallCount, callsOK, trace.ToolCallCount-callsOK,
			trace.TotalInputTokens, trace.TotalOutputTokens, float64(trace.DurationMS)/1000,
		)
		results = append(results, EvalResult{Task: task, Trace: trace, Score: score})
	}

	report := buildEvalReport(cfg.ProviderName, cfg.Model, "bash", cfg.MaxTurns, results)
	printEvalTerminalReport(stdout, &report)
	if cfg.Save {
		if err := saveEvalReport(&report, cfg.OutputDir, cfg.Moniker, stdout); err != nil {
			return err
		}
	}
	return nil
}

func filterTasksByID[T any](tasks []T, taskIDs []string, idOf func(T) string) ([]T, error) {
	if len(taskIDs) == 0 {
		return tasks, nil
	}

	wanted := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		wanted[taskID] = struct{}{}
	}

	filtered := make([]T, 0, len(taskIDs))
	found := make(map[string]struct{}, len(taskIDs))
	for _, task := range tasks {
		taskID := idOf(task)
		if _, ok := wanted[taskID]; !ok {
			continue
		}
		filtered = append(filtered, task)
		found[taskID] = struct{}{}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no tasks matched requested IDs: %s", strings.Join(taskIDs, ", "))
	}

	missing := make([]string, 0)
	for _, taskID := range taskIDs {
		if _, ok := found[taskID]; !ok {
			missing = append(missing, taskID)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("task IDs not found in dataset: %s", strings.Join(missing, ", "))
	}

	return filtered, nil
}
