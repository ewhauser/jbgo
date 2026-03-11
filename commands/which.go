package commands

import (
	"context"
	"fmt"
	"strings"
)

type Which struct{}

func NewWhich() *Which {
	return &Which{}
}

func (c *Which) Name() string {
	return "which"
}

func (c *Which) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: which [-as] NAME...")
		return nil
	}
	all, silent, names, err := parseWhichArgs(inv)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return &ExitError{Code: 1}
	}
	exitCode := 0
	for _, name := range names {
		matches, err := resolveAllCommands(ctx, inv, inv.Env, inv.Dir, name)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			exitCode = 1
			continue
		}
		if silent {
			continue
		}
		if !all {
			matches = matches[:1]
		}
		for _, match := range matches {
			if _, err := fmt.Fprintln(inv.Stdout, match); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseWhichArgs(inv *Invocation) (all, silent bool, names []string, err error) {
	args := inv.Args
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch arg {
		case "-a":
			all = true
		case "-s":
			silent = true
		default:
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				for _, flag := range arg[1:] {
					switch flag {
					case 'a':
						all = true
					case 's':
						silent = true
					default:
						return false, false, nil, exitf(inv, 1, "which: unsupported flag -%c", flag)
					}
				}
			} else {
				return false, false, nil, exitf(inv, 1, "which: unsupported flag %s", arg)
			}
		}
		args = args[1:]
	}
	return all, silent, args, nil
}

var _ Command = (*Which)(nil)
