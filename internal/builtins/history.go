package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

const historyEnvVar = "BASH_HISTORY"

type History struct{}

func NewHistory() *History {
	return &History{}
}

func (c *History) Name() string {
	return "history"
}

func (c *History) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *History) Spec() CommandSpec {
	return CommandSpec{
		Name:  "history",
		About: "Display or clear the current shell history.",
		Usage: "history [-c] [n]",
		Options: []OptionSpec{
			{Name: "clear", Short: 'c', Help: "clear the history list"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions: true,
			AutoHelp:          true,
			AutoVersion:       true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := io.WriteString(w, "Usage: history [-c] [n]\nDisplay or clear the current shell history.\n\nOptions:\n  -c                        clear the history list\n  -h, --help                display this help and exit\n      --version             output version information and exit\n")
			return err
		},
	}
}

func (c *History) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	history := parseHistoryEntries(inv)
	if matches.Has("clear") {
		if inv.Env == nil {
			inv.Env = map[string]string{}
		}
		inv.Env[historyEnvVar] = "[]"
		return nil
	}

	count := len(history)
	if args := matches.Args("arg"); len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n >= 0 && n < count {
			count = n
		}
	}

	start := len(history) - count
	for i := start; i < len(history); i++ {
		if _, err := fmt.Fprintf(inv.Stdout, "%5d  %s\n", i+1, history[i]); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func parseHistoryEntries(inv *Invocation) []string {
	if inv == nil || len(inv.Env) == 0 {
		return nil
	}
	raw := inv.Env[historyEnvVar]
	if raw == "" {
		return nil
	}
	var history []string
	if err := json.Unmarshal([]byte(raw), &history); err != nil {
		return nil
	}
	return history
}

var _ Command = (*History)(nil)
var _ SpecProvider = (*History)(nil)
var _ ParsedRunner = (*History)(nil)
