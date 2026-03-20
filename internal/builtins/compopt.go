package builtins

import (
	"context"

	"github.com/ewhauser/gbash/internal/completionutil"
)

type Compopt struct{}

func NewCompopt() *Compopt {
	return &Compopt{}
}

func (c *Compopt) Name() string {
	return "compopt"
}

func (c *Compopt) Run(ctx context.Context, inv *Invocation) error {
	cfg, err := completionutil.ParseCompoptArgs(inv.Args)
	if err != nil {
		return exitf(inv, 2, err.Error())
	}
	if err := completionutil.ApplyCompopt(completionStateFromContext(ctx), cfg); err != nil {
		return exitf(inv, 1, err.Error())
	}
	return nil
}

var _ Command = (*Compopt)(nil)
