package builtins

import "context"

type Kill struct{}

func NewKill() *Kill {
	return &Kill{}
}

func (c *Kill) Name() string {
	return "kill"
}

func (c *Kill) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Kill)(nil)
