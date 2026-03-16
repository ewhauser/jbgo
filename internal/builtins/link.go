package builtins

import (
	"context"
	"errors"
	stdfs "io/fs"
)

type Link struct{}

func NewLink() *Link {
	return &Link{}
}

func (c *Link) Name() string {
	return "link"
}

func (c *Link) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Link) Spec() CommandSpec {
	return CommandSpec{
		Name:  "link",
		About: "Call the link function to create a link named FILE2 to an existing FILE1.",
		Usage: "link FILE1 FILE2",
		Args: []ArgSpec{
			{Name: "file1", ValueName: "FILE1", Required: true},
			{Name: "file2", ValueName: "FILE2", Required: true},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
			AutoVersion:      true,
		},
	}
}

func (c *Link) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	oldName := matches.Arg("file1")
	newName := matches.Arg("file2")

	oldInfo, oldAbs, err := lstatPath(ctx, inv, oldName)
	if err != nil {
		return exitf(inv, 1, "link: cannot create link %s to %s: %s", quoteGNUOperand(newName), quoteGNUOperand(oldName), linkErrText(err))
	}
	if oldInfo.IsDir() {
		return exitf(inv, 1, "link: cannot create link %s to %s: Operation not permitted", quoteGNUOperand(newName), quoteGNUOperand(oldName))
	}

	newAbs := allowPath(inv, newName)
	if err := ensureParentDirExists(ctx, inv, newAbs); err != nil {
		return exitf(inv, 1, "link: cannot create link %s to %s: %s", quoteGNUOperand(newName), quoteGNUOperand(oldName), linkErrText(err))
	}
	if err := inv.FS.Link(ctx, oldAbs, newAbs); err != nil {
		return exitf(inv, 1, "link: cannot create link %s to %s: %s", quoteGNUOperand(newName), quoteGNUOperand(oldName), linkErrText(err))
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

var _ Command = (*Link)(nil)
var _ SpecProvider = (*Link)(nil)
var _ ParsedRunner = (*Link)(nil)
