package commands

import (
	"context"
	"fmt"
	"strings"
)

type Echo struct{}

func NewEcho() *Echo {
	return &Echo{}
}

func (c *Echo) Name() string {
	return "echo"
}

func (c *Echo) Run(_ context.Context, inv *Invocation) error {
	args := inv.Args
	newline := true
	for len(args) > 0 && args[0] == "-n" {
		newline = false
		args = args[1:]
	}

	output := strings.Join(args, " ")
	if newline {
		_, err := fmt.Fprintln(inv.Stdout, output)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	_, err := fmt.Fprint(inv.Stdout, output)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Echo)(nil)
