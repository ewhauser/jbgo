package builtins

import (
	"context"
	"fmt"

	"github.com/ewhauser/gbash/internal/completionutil"
)

type Complete struct{}

func NewComplete() *Complete {
	return &Complete{}
}

func (c *Complete) Name() string {
	return "complete"
}

func (c *Complete) Run(ctx context.Context, inv *Invocation) error {
	cfg, err := completionutil.ParseCompleteArgs(inv.Args)
	if err != nil {
		return exitf(inv, 2, err.Error())
	}
	lines, err := completionutil.ApplyComplete(completionStateFromContext(ctx), completionBackend(ctx, inv), cfg)
	if err != nil {
		if cfg != nil && cfg.PrintMode {
			return exitf(inv, 1, err.Error())
		}
		return exitf(inv, 2, err.Error())
	}
	for _, line := range lines {
		if _, writeErr := fmt.Fprintln(inv.Stdout, line); writeErr != nil {
			return &ExitError{Code: 1, Err: writeErr}
		}
	}
	return nil
}

var _ Command = (*Complete)(nil)
