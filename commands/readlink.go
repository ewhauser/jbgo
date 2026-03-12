package commands

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type Readlink struct{}

func NewReadlink() *Readlink {
	return &Readlink{}
}

func (c *Readlink) Name() string {
	return "readlink"
}

func (c *Readlink) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	canonicalize := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-f", "--canonicalize":
			canonicalize = true
		case "--":
			args = args[1:]
			goto operands
		default:
			return exitf(inv, 1, "readlink: unsupported flag %s", args[0])
		}
		args = args[1:]
	}

operands:
	if len(args) == 0 {
		return exitf(inv, 1, "readlink: missing operand")
	}

	exitCode := 0
	for _, name := range args {
		if canonicalize {
			abs, err := allowPath(ctx, inv, policy.FileActionStat, name)
			if err != nil {
				return err
			}
			resolved, err := inv.FS.Realpath(ctx, abs)
			if err != nil {
				if errors.Is(err, stdfs.ErrNotExist) {
					resolved = abs
				} else {
					return &ExitError{Code: 1, Err: err}
				}
			}
			if _, err := fmt.Fprintln(inv.Stdout, resolved); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			continue
		}

		abs, err := allowPath(ctx, inv, policy.FileActionReadlink, name)
		if err != nil {
			return err
		}
		target, err := inv.FS.Readlink(ctx, abs)
		if err != nil {
			if errors.Is(err, stdfs.ErrInvalid) || errors.Is(err, stdfs.ErrNotExist) {
				exitCode = 1
				continue
			}
			return &ExitError{Code: 1, Err: err}
		}
		if _, err := fmt.Fprintln(inv.Stdout, target); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

var _ Command = (*Readlink)(nil)
