package commands

import "context"

type Install struct {
	name string
}

func NewInstall() *Install {
	return &Install{name: "install"}
}

func NewGInstall() *Install {
	return &Install{name: "ginstall"}
}

func (c *Install) Name() string {
	if c == nil || c.name == "" {
		return "install"
	}
	return c.name
}

func (c *Install) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Install)(nil)
