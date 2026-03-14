package builtins

import "context"

type Users struct{}

func NewUsers() *Users {
	return &Users{}
}

func (c *Users) Name() string {
	return "users"
}

func (c *Users) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Users)(nil)
