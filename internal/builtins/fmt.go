package builtins

import "context"

type Fmt struct{}

func NewFmt() *Fmt {
	return &Fmt{}
}

func (c *Fmt) Name() string {
	return "fmt"
}

func (c *Fmt) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Fmt)(nil)
