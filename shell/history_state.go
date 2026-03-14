package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/interp"
)

const shellHistoryEnvVar = "BASH_HISTORY"

func withInteractiveHistory(runner *interp.Runner, script string) string {
	if runner == nil {
		return script
	}
	entry := strings.TrimRight(script, "\n")
	if strings.TrimSpace(entry) == "" {
		return script
	}
	history := historyEntriesFromRunner(runner)
	history = append(history, entry)
	raw, err := json.Marshal(history)
	if err != nil {
		return script
	}
	return fmt.Sprintf("%s='%s'\n%s", shellHistoryEnvVar, shellSingleQuote(string(raw)), script)
}

func syncCommandHistory(ctx context.Context, hc interp.HandlerContext, before map[string]string, after map[string]string) error {
	beforeValue, beforeOK := before[shellHistoryEnvVar]
	afterValue, afterOK := after[shellHistoryEnvVar]
	if beforeOK == afterOK && beforeValue == afterValue {
		return nil
	}
	if !afterOK {
		return hc.Builtin(ctx, []string{"unset", shellHistoryEnvVar})
	}
	return hc.Builtin(ctx, []string{"eval", fmt.Sprintf("%s='%s'", shellHistoryEnvVar, shellSingleQuote(afterValue))})
}

func historyEntriesFromRunner(runner *interp.Runner) []string {
	if runner == nil || runner.Vars == nil {
		return nil
	}
	return parseHistoryEntries(runner.Vars[shellHistoryEnvVar].String())
}

func parseHistoryEntries(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var history []string
	if err := json.Unmarshal([]byte(raw), &history); err != nil {
		return nil
	}
	return history
}
