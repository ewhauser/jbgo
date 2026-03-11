package commands

import "context"

type True struct{}
type False struct{}

func NewTrue() *True {
	return &True{}
}

func NewFalse() *False {
	return &False{}
}

func (c *True) Name() string {
	return "true"
}

func (c *False) Name() string {
	return "false"
}

func (c *True) Run(context.Context, *Invocation) error {
	return nil
}

func (c *False) Run(context.Context, *Invocation) error {
	return &ExitError{Code: 1}
}

var _ Command = (*True)(nil)
var _ Command = (*False)(nil)
