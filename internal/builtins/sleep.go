package builtins

import (
	"context"
	"time"
)

type Sleep struct{}

const maxSleepDuration = time.Hour

func NewSleep() *Sleep {
	return &Sleep{}
}

func (c *Sleep) Name() string {
	return "sleep"
}

func (c *Sleep) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Sleep) Spec() CommandSpec {
	return CommandSpec{
		Name:  "sleep",
		About: "delay for the combined duration of one or more NUMBER[SUFFIX] values",
		Usage: "sleep NUMBER[SUFFIX]...",
		Args: []ArgSpec{
			{Name: "number", ValueName: "NUMBER[SUFFIX]", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
			AutoVersion:      true,
		},
	}
}

func (c *Sleep) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	var total time.Duration
	for _, value := range matches.Args("number") {
		current, err := parseFlexibleDuration(value)
		if err != nil {
			return exitf(inv, 1, "sleep: invalid time interval %q", value)
		}
		total += current
	}
	if total > maxSleepDuration {
		total = maxSleepDuration
	}
	timer := time.NewTimer(total)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ Command = (*Sleep)(nil)
