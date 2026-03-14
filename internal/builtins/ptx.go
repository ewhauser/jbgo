package builtins

import "context"

type Ptx struct{}

func NewPtx() *Ptx {
	return &Ptx{}
}

func (c *Ptx) Name() string {
	return "ptx"
}

func (c *Ptx) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Ptx)(nil)
