package builtins

import "context"

type Expand struct{}

func NewExpand() *Expand {
	return &Expand{}
}

func (c *Expand) Name() string {
	return "expand"
}

func (c *Expand) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Expand)(nil)
