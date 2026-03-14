package builtins

import "context"

type Pr struct{}

func NewPr() *Pr {
	return &Pr{}
}

func (c *Pr) Name() string {
	return "pr"
}

func (c *Pr) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Pr)(nil)
