package builtins

import "context"

type Unexpand struct{}

func NewUnexpand() *Unexpand {
	return &Unexpand{}
}

func (c *Unexpand) Name() string {
	return "unexpand"
}

func (c *Unexpand) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Unexpand)(nil)
