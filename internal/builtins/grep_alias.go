package builtins

import "context"

type grepAlias struct {
	name       string
	forcedFlag string
	delegate   *Grep
}

func NewEGrep() Command {
	return &grepAlias{
		name:       "egrep",
		forcedFlag: "-E",
		delegate:   NewGrep(),
	}
}

func NewFGrep() Command {
	return &grepAlias{
		name:       "fgrep",
		forcedFlag: "-F",
		delegate:   NewGrep(),
	}
}

func (c *grepAlias) Name() string {
	return c.name
}

func (c *grepAlias) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *grepAlias) Spec() CommandSpec {
	spec := c.delegate.Spec()
	spec.Name = c.name
	spec.Usage = c.name + " [OPTION]... PATTERNS [FILE]..."
	return spec
}

func (c *grepAlias) RunParsed(ctx context.Context, inv *Invocation, _ *ParsedCommand) error {
	if inv == nil {
		return nil
	}
	clone := *inv
	clone.Args = append([]string{c.forcedFlag}, inv.Args...)
	return c.delegate.Run(ctx, &clone)
}

func (c *grepAlias) NormalizeParseError(inv *Invocation, err error) error {
	return c.delegate.NormalizeParseError(inv, err)
}

var _ Command = (*grepAlias)(nil)
var _ SpecProvider = (*grepAlias)(nil)
var _ ParsedRunner = (*grepAlias)(nil)
var _ ParseErrorNormalizer = (*grepAlias)(nil)
