package commands

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type Whoami struct{}

func NewWhoami() *Whoami {
	return &Whoami{}
}

func (c *Whoami) Name() string {
	return "whoami"
}

func (c *Whoami) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Whoami) Spec() CommandSpec {
	return CommandSpec{
		Name:  "whoami",
		Usage: "whoami",
		HelpRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, "usage: whoami\n")
			return err
		},
		Args: []ArgSpec{
			{Name: "args", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
			AutoVersion:      true,
		},
	}
}

func (c *Whoami) NormalizeParseError(inv *Invocation, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	trimmed := strings.TrimSuffix(msg, "Try 'whoami --help' for more information.\n")
	trimmed = strings.TrimSuffix(trimmed, "Try 'whoami --help' for more information.")
	if trimmed != msg && (strings.Contains(trimmed, "unrecognized option") || strings.Contains(trimmed, "invalid option --")) {
		return exitf(inv, 1, "%s", strings.TrimRight(trimmed, "\n"))
	}
	return err
}

func (c *Whoami) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if args := matches.Args("args"); len(args) > 0 {
		return exitf(inv, 1, "whoami: extra operand %s\nTry 'whoami --help' for more information.", quoteGNUOperand(args[0]))
	}
	_, err := fmt.Fprintln(inv.Stdout, idCurrentIdentity(inv).userName)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Whoami)(nil)
var _ SpecProvider = (*Whoami)(nil)
var _ ParsedRunner = (*Whoami)(nil)
var _ ParseErrorNormalizer = (*Whoami)(nil)
