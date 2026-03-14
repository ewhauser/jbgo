package builtins

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
	return RunCommand(ctx, c, inv)
}

func (c *Yes) Spec() CommandSpec {
	return CommandSpec{
		Name: "yes",
		HelpRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, yesHelpText)
			return err
		},
		VersionRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, yesVersionText)
			return err
		},
		Options: []OptionSpec{
			{Name: "help", Short: 'h', Long: "help", Help: "Print help"},
			{Name: "version", Short: 'V', Long: "version", Help: "Print version"},
		},
		Args: []ArgSpec{
			{Name: "string", ValueName: "STRING", Repeatable: true, Default: []string{"y"}},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			StopAtFirstPositional: true,
		},
	}
}

func (c *Yes) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		spec := c.Spec()
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	if matches.Has("version") {
		spec := c.Spec()
		return RenderCommandVersion(inv.Stdout, &spec)
	}

	buffer := prepareYesBuffer(yesArgsIntoBuffer(matches.Args("string")))

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
var _ SpecProvider = (*Yes)(nil)
var _ ParsedRunner = (*Yes)(nil)
