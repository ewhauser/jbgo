package commands

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

type Rmdir struct{}

func NewRmdir() *Rmdir {
	return &Rmdir{}
}

func (c *Rmdir) Name() string {
	return "rmdir"
}

func (c *Rmdir) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	parents := false
	verbose := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-p", "--parents":
			parents = true
		case "-v", "--verbose":
			verbose = true
		default:
			return exitf(inv, 1, "rmdir: unsupported flag %s", args[0])
		}
		args = args[1:]
	}

	if len(args) == 0 {
		return exitf(inv, 1, "rmdir: missing operand")
	}

	for _, dir := range args {
		abs, err := allowPath(ctx, inv, policy.FileActionRemove, dir)
		if err != nil {
			return err
		}
		if err := removeEmptyDir(ctx, inv, abs, verbose); err != nil {
			return err
		}
		if parents {
			for parent := path.Dir(abs); parent != "/" && parent != "."; parent = path.Dir(parent) {
				if err := removeEmptyDir(ctx, inv, parent, verbose); err != nil {
					break
				}
			}
		}
	}
	return nil
}

func removeEmptyDir(ctx context.Context, inv *Invocation, abs string, verbose bool) error {
	info, err := inv.FS.Lstat(ctx, abs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if !info.IsDir() {
		return exitf(inv, 1, "rmdir: failed to remove %q: Not a directory", abs)
	}
	if err := inv.FS.Remove(ctx, abs, false); err != nil {
		switch {
		case errors.Is(err, stdfs.ErrInvalid):
			return exitf(inv, 1, "rmdir: failed to remove %q: Directory not empty", abs)
		case errors.Is(err, stdfs.ErrNotExist):
			return exitf(inv, 1, "rmdir: failed to remove %q: No such file or directory", abs)
		default:
			return &ExitError{Code: 1, Err: err}
		}
	}
	recordFileMutation(inv.Trace, "remove", abs, abs, "")
	if verbose {
		if _, err := fmt.Fprintf(inv.Stdout, "rmdir: removing directory, %q\n", abs); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

var _ Command = (*Rmdir)(nil)
