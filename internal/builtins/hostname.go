package builtins

import "context"

type Hostname struct{}

func NewHostname() *Hostname {
	return &Hostname{}
}

func (c *Hostname) Name() string {
	return "hostname"
}

func (c *Hostname) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Hostname)(nil)
