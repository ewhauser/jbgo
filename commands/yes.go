package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

const yesBufferSize = 16 * 1024

type Yes struct{}

func NewYes() *Yes {
	return &Yes{}
}

func (c *Yes) Name() string {
	return "yes"
}

func (c *Yes) Run(ctx context.Context, inv *Invocation) error {
	operands, mode, err := parseYesArgs(inv)
	if err != nil {
		return err
	}
	switch mode {
	case "help":
		_, _ = fmt.Fprintln(inv.Stdout, "usage: yes [STRING]...")
		return nil
	case "version":
		_, _ = fmt.Fprintln(inv.Stdout, "yes (gbash)")
		return nil
	}

	line := "y\n"
	if len(operands) > 0 {
		line = strings.Join(operands, " ") + "\n"
	}
	chunk := []byte(line)
	if len(chunk) == 0 {
		chunk = []byte{'\n'}
	}
	for len(chunk) < yesBufferSize {
		remaining := yesBufferSize - len(chunk)
		if remaining >= len(line) {
			chunk = append(chunk, line...)
			continue
		}
		chunk = append(chunk, line[:remaining]...)
		break
	}

	writer := bufio.NewWriterSize(inv.Stdout, yesBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := writer.Write(chunk); err != nil {
			if yesBrokenPipe(err) {
				return nil
			}
			return exitf(inv, 1, "yes: standard output: %v", err)
		}
		if err := writer.Flush(); err != nil {
			if yesBrokenPipe(err) {
				return nil
			}
			return exitf(inv, 1, "yes: standard output: %v", err)
		}
	}
}

func parseYesArgs(inv *Invocation) (operands []string, mode string, err error) {
	args := append([]string(nil), inv.Args...)
	operands = make([]string, 0, len(args))
	parsingOptions := true
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]

		if !parsingOptions {
			operands = append(operands, arg)
			continue
		}

		switch arg {
		case "--help":
			return nil, "help", nil
		case "--version":
			return nil, "version", nil
		case "--":
			parsingOptions = false
		default:
			if arg == "-" || !strings.HasPrefix(arg, "-") {
				operands = append(operands, arg)
				parsingOptions = false
				continue
			}
			if strings.HasPrefix(arg, "--") {
				return nil, "", exitf(inv, 1, "yes: unrecognized option '%s'", arg)
			}
			return nil, "", exitf(inv, 1, "yes: invalid option -- '%s'", strings.TrimPrefix(arg, "-"))
		}
	}
	return operands, "", nil
}

func yesBrokenPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || strings.Contains(strings.ToLower(err.Error()), "broken pipe")
}

var _ Command = (*Yes)(nil)
