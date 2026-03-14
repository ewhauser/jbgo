package builtins

import "context"

type Shuf struct{}

func NewShuf() *Shuf {
	return &Shuf{}
}

func (c *Shuf) Name() string {
	return "shuf"
}

func (c *Shuf) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Shuf)(nil)
