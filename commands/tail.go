package commands

import (
	"context"
	"fmt"
)

type Tail struct{}

func NewTail() *Tail {
	return &Tail{}
}

func (c *Tail) Name() string {
	return "tail"
}

func (c *Tail) Run(ctx context.Context, inv *Invocation) error {
	opts, err := parseHeadTailArgs(inv, "tail", true)
	if err != nil {
		return err
	}

	process := func(data []byte) []byte {
		if opts.hasBytes {
			return lastBytes(data, opts.bytes)
		}
		if opts.fromLine {
			return linesFrom(data, opts.lines)
		}
		return lastLines(data, opts.lines)
	}

	if len(opts.files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		_, err = inv.Stdout.Write(process(data))
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	showHeaders := opts.verbose || (!opts.quiet && len(opts.files) > 1)
	exitCode := 0
	for i, file := range opts.files {
		data, _, err := readAllFile(ctx, inv, file)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "tail: %s: No such file or directory\n", file)
			exitCode = 1
			continue
		}
		if showHeaders {
			if i > 0 {
				_, _ = fmt.Fprintln(inv.Stdout)
			}
			if _, err := fmt.Fprintf(inv.Stdout, "==> %s <==\n", file); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if _, err := inv.Stdout.Write(process(data)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

var _ Command = (*Tail)(nil)
