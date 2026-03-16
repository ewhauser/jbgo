package builtins

import (
	"context"
	"errors"
	stdfs "io/fs"
)

type RM struct{}

func NewRM() *RM {
	return &RM{}
}

func (c *RM) Name() string {
	return "rm"
}

func (c *RM) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *RM) Spec() CommandSpec {
	return CommandSpec{
		Name:  "rm",
		About: "Remove files or directories",
		Usage: "rm [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "force", Short: 'f', Long: "force", Help: "ignore nonexistent files and arguments, never prompt"},
			{Name: "recursive", Short: 'r', ShortAliases: []rune{'R'}, Long: "recursive", Help: "remove directories and their contents recursively"},
			{Name: "dir", Short: 'd', Long: "dir", Help: "remove empty directories"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:     true,
			StopAtFirstPositional: true,
		},
	}
}

func (c *RM) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	recursive := matches.Has("recursive")
	force := matches.Has("force")
	allowDir := matches.Has("dir")
	args := matches.Args("file")

	for _, name := range args {
		abs := allowPath(inv, name)
		info, err := inv.FS.Lstat(ctx, abs)
		if err != nil {
			if force && errors.Is(err, stdfs.ErrNotExist) {
				continue
			}
			return &ExitError{Code: 1, Err: err}
		}
		if info.IsDir() && !recursive && !allowDir {
			return exitf(inv, 1, "rm: cannot remove '%s': Is a directory", name)
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
