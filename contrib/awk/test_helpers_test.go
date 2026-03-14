package awk

import (
	"context"

	"testing"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
)

func newAWKRegistry(tb testing.TB) *commands.Registry {
	tb.Helper()

	registry := gbruntime.DefaultRegistry()
	if err := Register(registry); err != nil {
		tb.Fatalf("Register(awk) error = %v", err)
	}
	return registry
}

func newAWKRuntime(tb testing.TB) *gbruntime.Runtime {
	tb.Helper()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{Registry: newAWKRegistry(tb)}))
	if err != nil {
		tb.Fatalf("runtime.New() error = %v", err)
	}
	return rt
}

func mustExecAWK(tb testing.TB, script string) *gbruntime.ExecutionResult {
	tb.Helper()

	result, err := newAWKRuntime(tb).Run(context.Background(), &gbruntime.ExecutionRequest{Script: script})
	if err != nil {
		tb.Fatalf("Run() error = %v", err)
	}
	return result
}
