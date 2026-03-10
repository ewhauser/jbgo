package commands

import (
	"context"
	"fmt"
)

type Head struct{}

func NewHead() *Head {
	return &Head{}
}

func (c *Head) Name() string {
	return "head"
}

func (c *Head) Run(ctx context.Context, inv *Invocation) error {
	opts, err := parseHeadTailArgs(inv, "head", false)
	if err != nil {
		return err
	}

	if len(opts.files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		_, err = inv.Stdout.Write(firstLines(data, opts.lines))
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	exitCode := 0
	for i, file := range opts.files {
		data, _, err := readAllFile(ctx, inv, file)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "head: %s: No such file or directory\n", file)
			exitCode = 1
			continue
		}
		if len(opts.files) > 1 {
			if i > 0 {
				_, _ = fmt.Fprintln(inv.Stdout)
			}
			if _, err := fmt.Fprintf(inv.Stdout, "==> %s <==\n", file); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if _, err := inv.Stdout.Write(firstLines(data, opts.lines)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

var _ Command = (*Head)(nil)
