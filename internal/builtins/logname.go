package builtins

import (
	"context"
	"fmt"
	"strings"
)

type Logname struct{}

func NewLogname() *Logname {
	return &Logname{}
}

func (c *Logname) Name() string {
	return "logname"
}

func (c *Logname) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Logname) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print the user's login name",
		Usage: "logname",
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

func (c *Logname) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}
	if positionals := matches.Positionals(); len(positionals) > 0 {
		return commandUsageError(inv, c.Name(), "extra operand %s", quoteGNUOperand(positionals[0]))
	}

	// In the sandbox, derive the login name from LOGNAME, then USER, then
	// fall back to the same default identity used by id/whoami.
	login := strings.TrimSpace(inv.Env["LOGNAME"])
	if login == "" {
		login = strings.TrimSpace(inv.Env["USER"])
	}
	if login == "" {
		login = idDefaultUserName
	}

	if _, err := fmt.Fprintln(inv.Stdout, login); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Logname)(nil)
var _ SpecProvider = (*Logname)(nil)
var _ ParsedRunner = (*Logname)(nil)
