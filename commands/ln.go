package commands

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

type LN struct{}

func NewLN() *LN {
	return &LN{}
}

func (c *LN) Name() string {
	return "ln"
}

func (c *LN) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	symbolic := false
	force := false
	verbose := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-s":
			symbolic = true
		case "-f":
			force = true
		case "-v":
			verbose = true
		case "-n":
			// Accepted for compatibility; current runtime has no special directory-symlink handling here.
		default:
			return exitf(inv, 1, "ln: unsupported flag %s", args[0])
		}
		args = args[1:]
	}

	if len(args) < 2 {
		return exitf(inv, 1, "ln: missing file operand")
	}
	if len(args) > 2 {
		return exitf(inv, 1, "ln: extra operand %q", args[2])
	}

	target := args[0]
	linkName := args[1]
	linkAbs, err := allowPath(ctx, inv, policy.FileActionWrite, linkName)
	if err != nil {
		return err
	}
	if err := ensureParentDirExists(ctx, inv, linkAbs); err != nil {
		return err
	}

	if force {
		if err := inv.FS.Remove(ctx, linkAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return &ExitError{Code: 1, Err: err}
		}
	} else {
		if _, _, exists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, linkName); err != nil {
			return err
		} else if exists {
			return exitf(inv, 1, "ln: failed to create link %q: File exists", linkAbs)
		}
	}

	if symbolic {
		if err := inv.FS.Symlink(ctx, target, linkAbs); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		recordFileMutation(inv.Trace, "symlink", linkAbs, target, linkAbs)
	} else {
		info, sourceAbs, err := lstatPath(ctx, inv, target)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return exitf(inv, 1, "ln: failed to access %q: Is a directory", sourceAbs)
		}
		if err := inv.FS.Link(ctx, sourceAbs, linkAbs); err != nil {
			if errors.Is(err, stdfs.ErrNotExist) {
				return exitf(inv, 1, "ln: failed to access %q: No such file or directory", sourceAbs)
			}
			return &ExitError{Code: 1, Err: err}
		}
		recordFileMutation(inv.Trace, "link", linkAbs, sourceAbs, linkAbs)
	}

	if verbose {
		if _, err := fmt.Fprintf(inv.Stdout, "%q -> %q\n", linkAbs, target); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

var _ Command = (*LN)(nil)
