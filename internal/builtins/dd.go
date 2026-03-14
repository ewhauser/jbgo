package builtins

import "context"

type Dd struct{}

func NewDd() *Dd {
	return &Dd{}
}

func (c *Dd) Name() string {
	return "dd"
}

func (c *Dd) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Dd)(nil)
