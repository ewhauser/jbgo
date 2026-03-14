package builtins

import "context"

type Nproc struct{}

func NewNproc() *Nproc {
	return &Nproc{}
}

func (c *Nproc) Name() string {
	return "nproc"
}

func (c *Nproc) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Nproc)(nil)
