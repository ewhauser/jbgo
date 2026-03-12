package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
		if err := headWriteFromReader(inv.Stdin, inv.Stdout, opts); err != nil {
			return err
		}
		return nil
	}

	showHeaders := opts.verbose || (!opts.quiet && len(opts.files) > 1)
	exitCode := 0
	for i, file := range opts.files {
		var (
			reader  io.Reader
			closeFn func()
		)
		switch file {
		case "-":
			reader = inv.Stdin
			closeFn = func() {}
		default:
			handle, _, err := openRead(ctx, inv, file)
			if err != nil {
				_, _ = fmt.Fprintf(inv.Stderr, "head: %s: No such file or directory\n", file)
				exitCode = 1
				continue
			}
			reader = handle
			closeFn = func() { _ = handle.Close() }
		}
		if showHeaders {
			if i > 0 {
				_, _ = fmt.Fprintln(inv.Stdout)
			}
			if _, err := fmt.Fprintf(inv.Stdout, "==> %s <==\n", file); err != nil {
				closeFn()
				return &ExitError{Code: 1, Err: err}
			}
		}
		if err := headWriteFromReader(reader, inv.Stdout, opts); err != nil {
			closeFn()
			return err
		}
		closeFn()
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func headWriteFromReader(src io.Reader, dst io.Writer, opts headTailOptions) error {
	if opts.hasBytes {
		return headWriteBytes(src, dst, opts.bytes)
	}
	return headWriteLines(src, dst, opts.lines)
}

func headWriteBytes(src io.Reader, dst io.Writer, count int) error {
	if count <= 0 {
		return nil
	}
	if _, err := io.Copy(dst, io.LimitReader(src, int64(count))); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func headWriteLines(src io.Reader, dst io.Writer, count int) error {
	if count <= 0 {
		return nil
	}

	reader := bufio.NewReader(src)
	for remaining := count; remaining > 0; {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			if _, writeErr := dst.Write(chunk); writeErr != nil {
				return &ExitError{Code: 1, Err: writeErr}
			}
			if chunk[len(chunk)-1] == '\n' {
				remaining--
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			return nil
		}
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Head)(nil)
