package commands

import (
	"context"
	"fmt"
)

type Pwd struct{}

func NewPwd() *Pwd {
	return &Pwd{}
}

func (c *Pwd) Name() string {
	return "pwd"
}

func (c *Pwd) Run(_ context.Context, inv *Invocation) error {
	if len(inv.Args) != 0 {
		return exitf(inv, 1, "pwd: unexpected arguments")
	}
	_, err := fmt.Fprintln(inv.Stdout, inv.Dir)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Pwd)(nil)
