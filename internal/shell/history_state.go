package shell

import (
	"encoding/json"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/interp"
)

const shellHistoryEnvVar = "BASH_HISTORY"

func rememberInteractiveHistory(runner *interp.Runner, script string) {
	if runner == nil {
		return
	}
	entry := strings.TrimRight(script, "\n")
	if strings.TrimSpace(entry) == "" {
		return
	}
	history := historyEntriesFromRunner(runner)
	history = append(history, entry)
	raw, err := json.Marshal(history)
	if err != nil {
		return
	}
	_ = runner.SetShellVar(shellHistoryEnvVar, expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  string(raw),
	})
}

func syncCommandHistory(hc *interp.HandlerContext, before, after map[string]string) error {
	if hc == nil {
		return nil
	}
	beforeValue, beforeOK := before[shellHistoryEnvVar]
	afterValue, afterOK := after[shellHistoryEnvVar]
	if beforeOK == afterOK && beforeValue == afterValue {
		return nil
	}
	if !afterOK {
		return hc.UnsetShellVar(shellHistoryEnvVar)
	}
	return hc.SetShellVar(shellHistoryEnvVar, expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  afterValue,
	})
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
