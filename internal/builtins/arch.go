package builtins

import (
	"context"
	"fmt"
)

type Arch struct{}

func NewArch() *Arch {
	return &Arch{}
}

func (c *Arch) Name() string {
	return "arch"
}

func (c *Arch) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Arch) Spec() CommandSpec {
	return CommandSpec{
		Name:      c.Name(),
		About:     "Display machine architecture",
		Usage:     "arch",
		AfterHelp: "Determine architecture name for current machine.",
		Options: []OptionSpec{
			{
				Name:  "version",
				Short: 'V',
				Long:  "version",
				Help:  "output version information and exit",
			},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
		},
	}
}

func (c *Arch) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}
	if positionals := matches.Positionals(); len(positionals) > 0 {
		return commandUsageError(inv, c.Name(), "extra operand %s", quoteGNUOperand(positionals[0]))
	}

	machine := archMachine(inv)
	if _, err := fmt.Fprintln(inv.Stdout, machine); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Arch)(nil)
var _ SpecProvider = (*Arch)(nil)
var _ ParsedRunner = (*Arch)(nil)
