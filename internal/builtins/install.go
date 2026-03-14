package builtins

import "context"

type Install struct{}

func NewInstall() *Install {
	return &Install{}
}

func (c *Install) Name() string {
	return "install"
}

func (c *Install) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Install)(nil)
