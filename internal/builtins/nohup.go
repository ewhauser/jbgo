package builtins

import "context"

type Nohup struct{}

func NewNohup() *Nohup {
	return &Nohup{}
}

func (c *Nohup) Name() string {
	return "nohup"
}

func (c *Nohup) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Nohup)(nil)
