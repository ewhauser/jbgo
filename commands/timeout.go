package commands

import (
	"context"
	"fmt"
	"time"
)

type Timeout struct{}

func NewTimeout() *Timeout {
	return &Timeout{}
}

func (c *Timeout) Name() string {
	return "timeout"
}

func (c *Timeout) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: timeout [OPTION] DURATION COMMAND [ARG...]")
		return nil
	}
	timeout, argv, err := parseTimeoutArgs(inv)
	if err != nil {
		return err
	}
	result, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:    argv,
		Env:     inv.Env,
		WorkDir: inv.Dir,
		Stdin:   inv.Stdin,
		Timeout: timeout,
	})
	if err != nil {
		return err
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func parseTimeoutArgs(inv *Invocation) (timeout time.Duration, argv []string, err error) {
	args := inv.Args
	for len(args) > 0 {
		switch args[0] {
		case "--foreground":
			args = args[1:]
		case "-k", "-s":
			if len(args) < 2 {
				return 0, nil, exitf(inv, 1, "timeout: option requires an argument -- %s", args[0])
			}
			args = args[2:]
		default:
			if args[0] != "" && args[0][0] == '-' {
				return 0, nil, exitf(inv, 1, "timeout: unrecognized option %s", args[0])
			}
			goto done
		}
	}
done:
	if len(args) < 2 {
		return 0, nil, exitf(inv, 1, "timeout: missing operand")
	}
	timeout, err = parseFlexibleDuration(args[0])
	if err != nil {
		return 0, nil, exitf(inv, 1, "timeout: invalid time interval %q", args[0])
	}
	return timeout, args[1:], nil
}

var _ Command = (*Timeout)(nil)
