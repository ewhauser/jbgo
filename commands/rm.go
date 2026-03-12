package commands

import (
	"context"
	"errors"
	stdfs "io/fs"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type RM struct{}

func NewRM() *RM {
	return &RM{}
}

func (c *RM) Name() string {
	return "rm"
}

func (c *RM) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	recursive := false
	force := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-r", "-R":
			recursive = true
		case "-f":
			force = true
		case "-rf", "-fr":
			recursive = true
			force = true
		default:
			return exitf(inv, 1, "rm: unsupported flag %s", args[0])
		}
		args = args[1:]
	}

	if len(args) == 0 {
		return exitf(inv, 1, "rm: missing operand")
	}

	for _, name := range args {
		abs, err := allowPath(ctx, inv, policy.FileActionRemove, name)
		if err != nil {
			return err
		}
		if err := inv.FS.Remove(ctx, abs, recursive); err != nil {
			if force && errors.Is(err, stdfs.ErrNotExist) {
				continue
			}
			return &ExitError{Code: 1, Err: err}
		}
	}

	return nil
}

var _ Command = (*RM)(nil)
