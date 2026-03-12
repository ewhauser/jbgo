package commands

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"syscall"
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
		_, _ = io.WriteString(inv.Stdout, yesHelpText)
		return nil
	case "version":
		_, _ = io.WriteString(inv.Stdout, yesVersionText)
		return nil
	}

	buffer := prepareYesBuffer(yesArgsIntoBuffer(operands))

	writer := bufio.NewWriterSize(inv.Stdout, yesBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := writer.Write(buffer); err != nil {
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

func yesArgsIntoBuffer(operands []string) []byte {
	if len(operands) == 0 {
		return []byte("y\n")
	}
	line := strings.Join(operands, " ") + "\n"
	return []byte(line)
}

func prepareYesBuffer(buffer []byte) []byte {
	if len(buffer) == 0 || len(buffer)*2 > yesBufferSize {
		return buffer
	}

	lineLen := len(buffer)
	targetSize := lineLen * (yesBufferSize / lineLen)
	for len(buffer) < targetSize {
		toCopy := min(targetSize-len(buffer), len(buffer))
		buffer = append(buffer, buffer[:toCopy]...)
	}
	return buffer
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

		switch {
		case arg == "--":
			parsingOptions = false
		case arg == "-h":
			return nil, "help", nil
		case arg == "-V":
			return nil, "version", nil
		case yesMatchesLongOption(arg, "help"):
			return nil, "help", nil
		case yesMatchesLongOption(arg, "version"):
			return nil, "version", nil
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			operands = append(operands, arg)
			parsingOptions = false
		case strings.HasPrefix(arg, "--"):
			return nil, "", exitf(inv, 1, "yes: unrecognized option '%s'\nTry 'yes --help' for more information.", arg)
		default:
			return nil, "", exitf(inv, 1, "yes: invalid option -- '%s'\nTry 'yes --help' for more information.", strings.TrimPrefix(arg, "-"))
		}
	}
	return operands, "", nil
}

func yesMatchesLongOption(arg, name string) bool {
	if !strings.HasPrefix(arg, "--") || arg == "--" {
		return false
	}
	return strings.HasPrefix(name, strings.TrimPrefix(arg, "--"))
}

func yesBrokenPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.EPIPE) ||
		strings.Contains(strings.ToLower(err.Error()), "broken pipe")
}

const yesHelpText = `Repeatedly display a line with STRING (or 'y')

Usage: yes [STRING]...

Arguments:
  [STRING]...  [default: y]

Options:
  -h, --help     Print help
  -V, --version  Print version
`

const yesVersionText = "yes (uutils coreutils) 0.7.0\n"

var _ Command = (*Yes)(nil)
