package builtins

import "context"

type Logname struct{}

func NewLogname() *Logname {
	return &Logname{}
}

func (c *Logname) Name() string {
	return "logname"
}

func (c *Logname) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Logname)(nil)
