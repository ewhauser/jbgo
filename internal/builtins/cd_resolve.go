package builtins

import (
	"context"
	"fmt"

	gbfs "github.com/ewhauser/gbash/fs"
)

const cdResolveCommandName = "__jb_cd_resolve"

type CDResolve struct{}

func NewCDResolve() *CDResolve {
	return &CDResolve{}
}

func (c *CDResolve) Name() string {
	return cdResolveCommandName
}

func (c *CDResolve) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) != 2 && len(inv.Args) != 3 {
		return exitf(inv, 2, "%s: usage: %s <cwd> <target> [command]", c.Name(), c.Name())
	}

	commandName := "cd"
	if len(inv.Args) == 3 && inv.Args[2] != "" {
		commandName = inv.Args[2]
	}
	next := gbfs.Resolve(inv.Args[0], inv.Args[1])
	info, _, err := statPath(ctx, inv, next)
	if err != nil {
		if commandName != "cd" {
			return exitf(inv, 1, "%s: %s: No such file or directory", commandName, inv.Args[1])
		}
		return exitf(inv, 1, "cd: no such file or directory: %q", inv.Args[1])
	}
	if !info.IsDir() {
		if commandName != "cd" {
			return exitf(inv, 1, "%s: %s: Not a directory", commandName, inv.Args[1])
		}
		return exitf(inv, 1, "cd: not a directory: %q", inv.Args[1])
	}

	if _, err := fmt.Fprintln(inv.Stdout, next); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*CDResolve)(nil)
