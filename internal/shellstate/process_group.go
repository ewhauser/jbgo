package shellstate

import "context"

type processGroupKey struct{}

func WithProcessGroup(ctx context.Context, pgrp int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if pgrp <= 0 {
		return ctx
	}
	return context.WithValue(ctx, processGroupKey{}, pgrp)
}

func ProcessGroupFromContext(ctx context.Context) (int, bool) {
	if ctx == nil {
		return 0, false
	}
	pgrp, ok := ctx.Value(processGroupKey{}).(int)
	return pgrp, ok
}
