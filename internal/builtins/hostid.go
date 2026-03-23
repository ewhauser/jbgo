package builtins

import (
	"context"
	"fmt"
)

// defaultHostID is a deterministic 32-bit host identifier used inside the
// sandbox.  It corresponds to the loopback address 127.0.0.1, which is the
// value gethostid(3) returns on most Linux systems that lack /etc/hostid.
const defaultHostID = 0x007f0101

type Hostid struct{}

func NewHostid() *Hostid {
	return &Hostid{}
}

func (c *Hostid) Name() string {
	return "hostid"
}

func (c *Hostid) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Hostid) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print the numeric identifier for the current host",
		Usage: "hostid",
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

func (c *Hostid) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}
	if positionals := matches.Positionals(); len(positionals) > 0 {
		return commandUsageError(inv, c.Name(), "extra operand %s", quoteGNUOperand(positionals[0]))
	}

	if _, err := fmt.Fprintf(inv.Stdout, "%08x\n", defaultHostID); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Hostid)(nil)
var _ SpecProvider = (*Hostid)(nil)
var _ ParsedRunner = (*Hostid)(nil)
