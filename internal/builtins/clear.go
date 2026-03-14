package builtins

import (
	"context"
	"io"
)

const clearEscapeSequence = "\x1b[2J\x1b[H"

type Clear struct{}

func NewClear() *Clear {
	return &Clear{}
}

func (c *Clear) Name() string {
	return "clear"
}

func (c *Clear) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Clear) Spec() CommandSpec {
	return CommandSpec{
		Name:  "clear",
		About: "Clear the terminal screen.",
		Usage: "clear [OPTION]...",
		Parse: ParseConfig{
			AutoHelp:    true,
			AutoVersion: true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := io.WriteString(w, "Usage: clear [OPTION]...\nClear the terminal screen.\n\nOptions:\n  -h, --help                display this help and exit\n      --version             output version information and exit\n")
			return err
		},
	}
}

func (c *Clear) RunParsed(_ context.Context, inv *Invocation, _ *ParsedCommand) error {
	if _, err := io.WriteString(inv.Stdout, clearEscapeSequence); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Clear)(nil)
var _ SpecProvider = (*Clear)(nil)
var _ ParsedRunner = (*Clear)(nil)
