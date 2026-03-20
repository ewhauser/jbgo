package builtins

import (
	"context"

	"github.com/ewhauser/gbash/internal/completionutil"
)

type Compadjust struct{}

func NewCompadjust() *Compadjust {
	return &Compadjust{}
}

func (c *Compadjust) Name() string {
	return "compadjust"
}

func (c *Compadjust) Run(ctx context.Context, inv *Invocation) error {
	if err := completionutil.ApplyCompadjust(completionBackend(ctx, inv), inv.Args); err != nil {
		return exitf(inv, 2, err.Error())
	}
	return nil
}

var _ Command = (*Compadjust)(nil)
