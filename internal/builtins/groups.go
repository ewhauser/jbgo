package builtins

import "context"

type Groups struct{}

func NewGroups() *Groups {
	return &Groups{}
}

func (c *Groups) Name() string {
	return "groups"
}

func (c *Groups) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Groups)(nil)
