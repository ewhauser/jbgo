package builtins

import "context"

type Df struct{}

func NewDf() *Df {
	return &Df{}
}

func (c *Df) Name() string {
	return "df"
}

func (c *Df) Run(_ context.Context, inv *Invocation) error {
	return runNotImplemented(inv, c.Name())
}

var _ Command = (*Df)(nil)
