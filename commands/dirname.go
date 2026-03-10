package commands

import (
	"context"
	"fmt"
	"path"
	"strings"
)

type Dirname struct{}

func NewDirname() *Dirname {
	return &Dirname{}
}

func (c *Dirname) Name() string {
	return "dirname"
}

func (c *Dirname) Run(_ context.Context, inv *Invocation) error {
	args := inv.Args
	if len(args) == 0 {
		return exitf(inv, 1, "dirname: missing operand")
	}
	for _, arg := range args {
		if _, err := fmt.Fprintln(inv.Stdout, dirname(arg)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func dirname(name string) string {
	if name == "" {
		return "."
	}
	cleaned := strings.TrimRight(name, "/")
	if cleaned == "" {
		return "/"
	}
	dir := path.Dir(cleaned)
	if dir == "" {
		return "."
	}
	return dir
}

var _ Command = (*Dirname)(nil)
