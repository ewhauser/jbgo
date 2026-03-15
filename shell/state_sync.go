package shell

import (
	"context"
	"fmt"

	"mvdan.cc/sh/v3/interp"
)

const shellCompletionSpecsEnvVar = "GBASH_COMPLETE_SPECS"

func syncCommandState(ctx context.Context, hc *interp.HandlerContext, before, after map[string]string) error {
	for _, key := range []string{shellHistoryEnvVar, shellCompletionSpecsEnvVar} {
		if err := syncShellVar(ctx, hc, key, before, after); err != nil {
			return err
		}
	}
	return nil
}

func syncShellVar(ctx context.Context, hc *interp.HandlerContext, key string, before, after map[string]string) error {
	if hc == nil {
		return nil
	}
	beforeValue, beforeOK := before[key]
	afterValue, afterOK := after[key]
	if beforeOK == afterOK && beforeValue == afterValue {
		return nil
	}
	if !afterOK {
		return hc.Builtin(ctx, []string{"unset", key})
	}
	return hc.Builtin(ctx, []string{"eval", fmt.Sprintf("%s='%s'", key, shellSingleQuote(afterValue))})
}
