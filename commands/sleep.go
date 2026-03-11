package commands

import (
	"context"
	"fmt"
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
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: sleep NUMBER[SUFFIX]...")
		_, _ = fmt.Fprintln(inv.Stdout, "delay for the combined duration")
		return nil
	}
	if len(inv.Args) == 0 {
		return exitf(inv, 1, "sleep: missing operand")
	}
	var total time.Duration
	for _, value := range inv.Args {
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
