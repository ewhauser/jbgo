package builtins

import "context"

type Pathchk struct{}

func NewPathchk() *Pathchk {
	return &Pathchk{}
}

func (c *Pathchk) Name() string {
	return "pathchk"
}

func (c *Pathchk) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Pathchk)(nil)
