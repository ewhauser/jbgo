//nolint:forbidigo,gocritic // The host-side evaluator persists reports on disk and keeps report shaping straightforward.
package gbasheval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ScriptingEvalResult struct {
	Task  ScriptingEvalTask `json:"task"`
	Trace ScriptingTrace    `json:"trace"`
	Score TaskScore         `json:"score"`
}

type ScriptingEvalReport struct {
	Provider  string                `json:"provider"`
	Model     string                `json:"model"`
	Timestamp string                `json:"timestamp"`
	MaxTurns  int                   `json:"max_turns"`
	Baseline  bool                  `json:"baseline"`
	Results   []ScriptingEvalResult `json:"results"`
	Summary   ScriptingSummary      `json:"summary"`
}

type ScriptingSummary struct {
	TotalTasks              int                        `json:"total_tasks"`
	TotalPassed             int                        `json:"total_passed"`
	TotalScore              float64                    `json:"total_score"`
	TotalMaxScore           float64                    `json:"total_max_score"`
	OverallRate             float64                    `json:"overall_rate"`
	TotalInputTokens        uint32                     `json:"total_input_tokens"`
	TotalOutputTokens       uint32                     `json:"total_output_tokens"`
	TotalTurns              int                        `json:"total_turns"`
	TotalToolCalls          int                        `json:"total_tool_calls"`
	ToolCallsOK             int                        `json:"tool_calls_ok"`
	ToolCallsError          int                        `json:"tool_calls_error"`
	ToolCallSuccessRate     float64                    `json:"tool_call_success_rate"`
	TotalDurationMS         uint64                     `json:"total_duration_ms"`
	AverageTurnsPerTask     float64                    `json:"avg_turns_per_task"`
	AverageCallsPerTask     float64                    `json:"avg_tool_calls_per_task"`
	TotalInnerCommands      int                        `json:"total_inner_commands"`
	TotalInnerToolCalls     int                        `json:"total_inner_tool_calls"`
	TotalInnerHelpCalls     int                        `json:"total_inner_help_calls"`
	TotalInnerDiscoverCalls int                        `json:"total_inner_discover_calls"`
	AverageInnerPerTask     float64                    `json:"avg_inner_commands_per_task"`
	AverageDurationMS       float64                    `json:"avg_duration_ms"`
	TotalRawOutputBytes     int                        `json:"total_raw_tool_output_bytes"`
	TotalSentOutputBytes    int                        `json:"total_tool_output_sent_bytes"`
	ByCategory              map[string]CategorySummary `json:"by_category"`
}

func buildScriptingReport(providerName, model string, maxTurns int, baseline bool, results []ScriptingEvalResult) ScriptingEvalReport {
	summary := ScriptingSummary{
		TotalTasks: len(results),
		ByCategory: map[string]CategorySummary{},
	}
	for i := range results {
		result := &results[i]
		if result.Score.AllPassed() {
			summary.TotalPassed++
		}
		summary.TotalScore += result.Score.Score
		summary.TotalMaxScore += result.Score.MaxScore
		summary.TotalInputTokens += result.Trace.TotalInputTokens
		summary.TotalOutputTokens += result.Trace.TotalOutputTokens
		summary.TotalTurns += result.Trace.Turns
		summary.TotalToolCalls += result.Trace.ToolCallCount
		summary.TotalDurationMS += result.Trace.DurationMS
		summary.TotalInnerCommands += result.Trace.InnerCommandCount()
		summary.TotalInnerToolCalls += result.Trace.InnerCommandCountByKind(ScriptedCommandKindTool)
		summary.TotalInnerHelpCalls += result.Trace.InnerCommandCountByKind(ScriptedCommandKindHelp)
		summary.TotalInnerDiscoverCalls += result.Trace.InnerCommandCountByKind(ScriptedCommandKindDiscover)
		summary.TotalRawOutputBytes += result.Trace.RawToolOutputBytes
		summary.TotalSentOutputBytes += result.Trace.ToolOutputSentBytes
		for _, call := range result.Trace.ToolCalls {
			if call.ExitCode == 0 {
				summary.ToolCallsOK++
			}
		}

		cat := summary.ByCategory[result.Task.Category]
		cat.Tasks++
		if result.Score.AllPassed() {
			cat.Passed++
		}
		cat.Score += result.Score.Score
		cat.MaxScore += result.Score.MaxScore
		summary.ByCategory[result.Task.Category] = cat
	}
	summary.ToolCallsError = summary.TotalToolCalls - summary.ToolCallsOK
	if summary.TotalMaxScore > 0 {
		summary.OverallRate = summary.TotalScore / summary.TotalMaxScore
	}
	if summary.TotalToolCalls > 0 {
		summary.ToolCallSuccessRate = float64(summary.ToolCallsOK) / float64(summary.TotalToolCalls)
	} else {
		summary.ToolCallSuccessRate = 1
	}
	if summary.TotalTasks > 0 {
		n := float64(summary.TotalTasks)
		summary.AverageTurnsPerTask = float64(summary.TotalTurns) / n
		summary.AverageCallsPerTask = float64(summary.TotalToolCalls) / n
		summary.AverageInnerPerTask = float64(summary.TotalInnerCommands) / n
		summary.AverageDurationMS = float64(summary.TotalDurationMS) / n
	}
	for key, cat := range summary.ByCategory {
		if cat.MaxScore > 0 {
			cat.Rate = cat.Score / cat.MaxScore
		} else {
			cat.Rate = 1
		}
		summary.ByCategory[key] = cat
	}

	return ScriptingEvalReport{
		Provider:  providerName,
		Model:     model,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		MaxTurns:  maxTurns,
		Baseline:  baseline,
		Results:   results,
		Summary:   summary,
	}
}

func printScriptingTerminalReport(w io.Writer, report *ScriptingEvalReport) {
	if w == nil {
		w = io.Discard
	}
	if report == nil {
		return
	}
	mode := "scripted"
	if report.Baseline {
		mode = "baseline"
	}
	_, _ = fmt.Fprintf(w, "\n=== Scripting Tool Eval: %s/%s (%s) ===\n\n", report.Provider, report.Model, mode)
	for i := range report.Results {
		result := &report.Results[i]
		status := "FAIL"
		if result.Score.AllPassed() {
			status = "PASS"
		}
		_, _ = fmt.Fprintf(w, "  [%s] %s (%s) - %.0f/%.0f\n", status, result.Task.ID, result.Task.Category, result.Score.Score, result.Score.MaxScore)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "--- Summary ---")
	if report.Baseline {
		_, _ = fmt.Fprintln(w, "  Mode: baseline (individual tools)")
	} else {
		_, _ = fmt.Fprintln(w, "  Mode: scripted (bash orchestration tool)")
	}
	_, _ = fmt.Fprintf(w, "  Tasks: %d/%d passed\n", report.Summary.TotalPassed, report.Summary.TotalTasks)
	_, _ = fmt.Fprintf(w, "  Score: %.1f/%.1f (%.0f%%)\n", report.Summary.TotalScore, report.Summary.TotalMaxScore, report.Summary.OverallRate*100)
	_, _ = fmt.Fprintf(w, "  Turns: %d total, %.1f avg/task\n", report.Summary.TotalTurns, report.Summary.AverageTurnsPerTask)
	_, _ = fmt.Fprintf(w, "  Tool calls: %d total, %.1f avg/task (%d ok, %d error, %.0f%% success)\n", report.Summary.TotalToolCalls, report.Summary.AverageCallsPerTask, report.Summary.ToolCallsOK, report.Summary.ToolCallsError, report.Summary.ToolCallSuccessRate*100)
	_, _ = fmt.Fprintf(w, "  Inner commands: %d total, %.1f avg/task (%d tool, %d help, %d discover)\n", report.Summary.TotalInnerCommands, report.Summary.AverageInnerPerTask, report.Summary.TotalInnerToolCalls, report.Summary.TotalInnerHelpCalls, report.Summary.TotalInnerDiscoverCalls)
	_, _ = fmt.Fprintf(w, "  Tool output bytes: %d raw, %d sent to model\n", report.Summary.TotalRawOutputBytes, report.Summary.TotalSentOutputBytes)
	_, _ = fmt.Fprintf(w, "  Tokens: %d input, %d output\n", report.Summary.TotalInputTokens, report.Summary.TotalOutputTokens)
	_, _ = fmt.Fprintf(w, "  Duration: %.1fs total, %.1fs avg/task\n", float64(report.Summary.TotalDurationMS)/1000, report.Summary.AverageDurationMS/1000)

	keys := make([]string, 0, len(report.Summary.ByCategory))
	for key := range report.Summary.ByCategory {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "--- By Category ---")
	for _, key := range keys {
		cat := report.Summary.ByCategory[key]
		_, _ = fmt.Fprintf(w, "  %-25s %d/%d tasks  %.0f%%\n", key, cat.Passed, cat.Tasks, cat.Rate*100)
	}
	_, _ = fmt.Fprintln(w)
}

func saveScriptingReport(report *ScriptingEvalReport, outputDir, moniker string, stdout io.Writer) error {
	if report == nil {
		return fmt.Errorf("report is nil")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outputDir, err)
	}
	mode := "scripted"
	if report.Baseline {
		mode = "baseline"
	}
	base := filepath.Join(outputDir, fmt.Sprintf("scripting-eval-%s-%s-%s", mode, moniker, time.Now().UTC().Format("2006-01-02-150405")))

	jsonPath := base + ".json"
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report json: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonBytes, 0o644); err != nil {
		return fmt.Errorf("write report json: %w", err)
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "Saved JSON: %s\n", jsonPath)
	}

	mdPath := base + ".md"
	if err := os.WriteFile(mdPath, []byte(generateScriptingMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write report markdown: %w", err)
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "Saved Markdown: %s\n", mdPath)
	}
	return nil
}

func generateScriptingMarkdown(report *ScriptingEvalReport) string {
	if report == nil {
		return ""
	}
	mode := "scripted"
	if report.Baseline {
		mode = "baseline"
	}
	var out strings.Builder
	_, _ = fmt.Fprintf(&out, "# Scripting Eval Report: %s/%s (%s)\n\n", report.Provider, report.Model, mode)
	_, _ = fmt.Fprintf(&out, "- Timestamp: `%s`\n", report.Timestamp)
	_, _ = fmt.Fprintf(&out, "- Max turns: `%d`\n\n", report.MaxTurns)
	_, _ = fmt.Fprint(&out, "## Summary\n\n")
	_, _ = fmt.Fprintf(&out, "- Tasks passed: `%d/%d`\n", report.Summary.TotalPassed, report.Summary.TotalTasks)
	_, _ = fmt.Fprintf(&out, "- Score: `%.1f/%.1f` (`%.0f%%`)\n", report.Summary.TotalScore, report.Summary.TotalMaxScore, report.Summary.OverallRate*100)
	_, _ = fmt.Fprintf(&out, "- Tool calls: `%d` total, `%.0f%%` success\n", report.Summary.TotalToolCalls, report.Summary.ToolCallSuccessRate*100)
	_, _ = fmt.Fprintf(&out, "- Inner commands: `%d` total (`%d` tool, `%d` help, `%d` discover)\n", report.Summary.TotalInnerCommands, report.Summary.TotalInnerToolCalls, report.Summary.TotalInnerHelpCalls, report.Summary.TotalInnerDiscoverCalls)
	_, _ = fmt.Fprintf(&out, "- Tool output bytes: `%d` raw, `%d` sent\n\n", report.Summary.TotalRawOutputBytes, report.Summary.TotalSentOutputBytes)

	_, _ = fmt.Fprint(&out, "## Task Results\n\n")
	_, _ = fmt.Fprint(&out, "| Task | Category | Status | Score | Turns | Calls | Inner |\n")
	_, _ = fmt.Fprint(&out, "|---|---|---|---:|---:|---:|---:|\n")
	for i := range report.Results {
		result := &report.Results[i]
		status := "FAIL"
		if result.Score.AllPassed() {
			status = "PASS"
		}
		_, _ = fmt.Fprintf(&out, "| %s | %s | %s | %.0f/%.0f | %d | %d | %d |\n", result.Task.ID, result.Task.Category, status, result.Score.Score, result.Score.MaxScore, result.Trace.Turns, result.Trace.ToolCallCount, result.Trace.InnerCommandCount())
	}
	return out.String()
}
