package builtins

import "context"

type Fold struct{}

func NewFold() *Fold {
	return &Fold{}
}

func (c *Fold) Name() string {
	return "fold"
}

func (c *Fold) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Fold)(nil)
