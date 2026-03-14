package builtins

import "context"

type Hostid struct{}

func NewHostid() *Hostid {
	return &Hostid{}
}

func (c *Hostid) Name() string {
	return "hostid"
}

func (c *Hostid) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Hostid)(nil)
