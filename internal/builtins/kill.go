package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/internal/shellstate"
)

type Kill struct{}

func NewKill() *Kill {
	return &Kill{}
}

func (c *Kill) Name() string {
	return "kill"
}

func (c *Kill) Run(ctx context.Context, inv *Invocation) error {
	if inv == nil {
		return &ExitError{Code: 1}
	}
	args := append([]string(nil), inv.Args...)
	if len(args) == 0 {
		return exitf(inv, 1, "kill: usage: kill [-s sigspec | -SIGNAL] pid | jobspec ... or kill -l [sigspec]")
	}

	listMode := false
	signal := 15
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if arg == "-l" {
			listMode = true
			args = args[1:]
			break
		}
		if arg == "-s" {
			if len(args) < 2 {
				return exitf(inv, 1, "kill: option requires an argument -- 's'")
			}
			info, err := interp.ResolveSignal(args[1])
			if err != nil {
				return exitf(inv, 1, "kill: invalid signal %q", args[1])
			}
			signal = info.Number
			args = args[2:]
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			info, err := interp.ResolveSignal(strings.TrimPrefix(arg, "-"))
			if err != nil {
				break
			}
			signal = info.Number
			args = args[1:]
			continue
		}
		break
	}

	if listMode {
		signals := interp.ListSignals()
		if len(args) == 0 {
			for _, info := range signals {
				if _, err := fmt.Fprintf(inv.Stdout, "%d) %s\n", info.Number, info.Name); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			return nil
		}
		for _, raw := range args {
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
				found := false
				for _, info := range signals {
					if info.Number == n {
						if _, err := fmt.Fprintln(inv.Stdout, info.Name); err != nil {
							return &ExitError{Code: 1, Err: err}
						}
						found = true
						break
					}
				}
				if !found {
					return exitf(inv, 1, "kill: %s: invalid signal specification", raw)
				}
				continue
			}
			info, err := interp.ResolveSignal(raw)
			if err != nil {
				return exitf(inv, 1, "kill: %s: invalid signal specification", raw)
			}
			if _, err := fmt.Fprintln(inv.Stdout, info.Name); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	if len(args) == 0 {
		return exitf(inv, 1, "kill: usage: kill [-s sigspec | -SIGNAL] pid | jobspec ... or kill -l [sigspec]")
	}
	dispatch := shellstate.SignalDispatcherFromContext(ctx)
	if dispatch == nil {
		return exitf(inv, 1, "kill: signaling is only available from shell contexts")
	}
	for _, target := range args {
		if err := dispatch(target, signal); err != nil {
			return exitf(inv, 1, "kill: %v", err)
		}
	}
	return nil
}

var _ Command = (*Kill)(nil)
