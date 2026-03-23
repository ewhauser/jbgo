//nolint:gocritic // Internal evaluator wiring favors simple value semantics over pointer-heavy plumbing.
package gbasheval

import (
	"context"
	"fmt"
	"io"
	"strings"
)

func runScriptingEval(ctx context.Context, cfg RunConfig, provider Provider, stdout io.Writer) error {
	tasks, err := loadScriptingDataset(cfg.DatasetPath)
	if err != nil {
		return err
	}
	tasks, err = filterTasksByID(tasks, cfg.TaskIDs, func(task ScriptingEvalTask) string { return task.ID })
	if err != nil {
		return err
	}
	mode := "scripted"
	if cfg.Baseline {
		mode = "baseline"
	}
	_, _ = fmt.Fprintf(stdout, "Running %d scripting-tool tasks (%s mode) with %s/%s  (max_turns=%d)\n\n", len(tasks), mode, cfg.ProviderName, cfg.Model, cfg.MaxTurns)

	results := make([]ScriptingEvalResult, 0, len(tasks))
	for i, task := range tasks {
		_, _ = fmt.Fprintf(stdout, "[%d/%d] %s - %s (tools: %d)\n", i+1, len(tasks), task.ID, task.Description, len(task.Tools))

		var trace ScriptingTrace
		if cfg.Baseline {
			trace, err = runBaselineAgent(ctx, provider, task, cfg.MaxTurns)
		} else {
			trace, err = runScriptedAgent(ctx, provider, task, cfg.MaxTurns)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stdout, "  ERROR: %v\n\n", err)
			results = append(results, ScriptingEvalResult{
				Task:  task,
				Trace: trace,
				Score: failureScore(task.ID, task.Expectations, err),
			})
			continue
		}

		score := scoreTask(ctx, task.ID, compatTraceFromScripting(trace), nil, task.Expectations)
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
		_, _ = fmt.Fprintf(stdout, "  Score: %.0f/%.0f | Turns: %d | Calls: %d (%d ok, %d err) | Inner: %d (%d tool, %d help, %d discover) | Tokens: %din/%dout | Raw output: %d bytes | %.1fs\n\n",
			score.Score, score.MaxScore, trace.Turns, trace.ToolCallCount, callsOK, trace.ToolCallCount-callsOK,
			trace.InnerCommandCount(), trace.InnerCommandCountByKind(ScriptedCommandKindTool), trace.InnerCommandCountByKind(ScriptedCommandKindHelp), trace.InnerCommandCountByKind(ScriptedCommandKindDiscover),
			trace.TotalInputTokens, trace.TotalOutputTokens, trace.RawToolOutputBytes, float64(trace.DurationMS)/1000,
		)
		results = append(results, ScriptingEvalResult{Task: task, Trace: trace, Score: score})
	}

	report := buildScriptingReport(cfg.ProviderName, cfg.Model, cfg.MaxTurns, cfg.Baseline, results)
	printScriptingTerminalReport(stdout, &report)
	if cfg.Save {
		if err := saveScriptingReport(&report, cfg.OutputDir, cfg.Moniker, stdout); err != nil {
			return err
		}
	}
	return nil
}

func compatTraceFromScripting(trace ScriptingTrace) agentTrace {
	toolCalls := make([]toolCallResult, 0, len(trace.ToolCalls))
	for _, call := range trace.ToolCalls {
		toolCalls = append(toolCalls, toolCallResult{
			Commands: strings.TrimSpace(toJSONString(call.Input)),
			Stdout:   call.Output,
			Stderr:   call.Stderr,
			ExitCode: call.ExitCode,
		})
	}
	var last *toolCallResult
	if len(toolCalls) > 0 {
		tmp := toolCalls[len(toolCalls)-1]
		last = &tmp
	}
	return agentTrace{
		Messages:          trace.Messages,
		ToolCalls:         toolCalls,
		ToolCallCount:     len(toolCalls),
		Turns:             trace.Turns,
		LastToolResponse:  last,
		NaturalStop:       trace.NaturalStop,
		TotalInputTokens:  trace.TotalInputTokens,
		TotalOutputTokens: trace.TotalOutputTokens,
		DurationMS:        trace.DurationMS,
	}
}
