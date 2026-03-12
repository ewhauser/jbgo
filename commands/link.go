package commands

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type Link struct{}

func NewLink() *Link {
	return &Link{}
}

func (c *Link) Name() string {
	return "link"
}

func (c *Link) Run(ctx context.Context, inv *Invocation) error {
	args := append([]string(nil), inv.Args...)
	if len(args) == 1 {
		switch args[0] {
		case "--help":
			_, _ = io.WriteString(inv.Stdout, linkHelpText)
			return nil
		case "--version":
			_, _ = io.WriteString(inv.Stdout, linkVersionText)
			return nil
		}
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) != 2 {
		return exitf(inv, 1, "link: expected exactly 2 file operands")
	}
	if strings.HasPrefix(args[0], "-") && args[0] != "-" {
		return exitf(inv, 1, "link: unsupported flag %s", args[0])
	}

	oldInfo, oldAbs, err := lstatPath(ctx, inv, args[0])
	if err != nil {
		if strings.HasPrefix(args[0], "-") && args[0] != "-" {
			return exitf(inv, 1, "link: unsupported flag %s", args[0])
		}
		return exitf(inv, 1, "link: cannot create link %s to %s: %s", quoteGNUOperand(args[1]), quoteGNUOperand(args[0]), linkErrText(err))
	}
	if oldInfo.IsDir() {
		return exitf(inv, 1, "link: cannot create link %s to %s: Operation not permitted", quoteGNUOperand(args[1]), quoteGNUOperand(args[0]))
	}

	newAbs, err := allowPath(ctx, inv, policy.FileActionWrite, args[1])
	if err != nil {
		return err
	}
	if err := ensureParentDirExists(ctx, inv, newAbs); err != nil {
		return exitf(inv, 1, "link: cannot create link %s to %s: %s", quoteGNUOperand(args[1]), quoteGNUOperand(args[0]), linkErrText(err))
	}
	if err := inv.FS.Link(ctx, oldAbs, newAbs); err != nil {
		return exitf(inv, 1, "link: cannot create link %s to %s: %s", quoteGNUOperand(args[1]), quoteGNUOperand(args[0]), linkErrText(err))
	}
	return nil
}

func linkErrText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrExist):
		return "File exists"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, stdfs.ErrInvalid):
		return "Invalid argument"
	default:
		return err.Error()
	}
}

const linkHelpText = `Usage: link FILE1 FILE2
Call the link function to create a link named FILE2 to an existing FILE1.
`

const linkVersionText = "link (gbash) dev\n"

var _ Command = (*Link)(nil)
