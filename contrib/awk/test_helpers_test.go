package awk

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
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

type awkCommandOptions struct {
	Args  []string
	Env   map[string]string
	Stdin string
	Files map[string]string
	Now   time.Time
}

type awkCommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

func runAWKCommand(tb testing.TB, opts *awkCommandOptions) awkCommandResult {
	tb.Helper()
	if opts == nil {
		opts = &awkCommandOptions{}
	}

	mem := gbfs.NewMemory()
	for name, contents := range opts.Files {
		file, err := mem.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			tb.Fatalf("OpenFile(%q) error = %v", name, err)
		}
		if _, err := file.Write([]byte(contents)); err != nil {
			_ = file.Close()
			tb.Fatalf("Write(%q) error = %v", name, err)
		}
		if err := file.Close(); err != nil {
			tb.Fatalf("Close(%q) error = %v", name, err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	invOpts := &commands.InvocationOptions{
		Args:       append([]string(nil), opts.Args...),
		Env:        opts.Env,
		Cwd:        "/",
		Stdin:      strings.NewReader(opts.Stdin),
		Stdout:     &stdout,
		Stderr:     &stderr,
		FileSystem: mem,
		Policy:     policy.NewStatic(&policy.Config{}),
	}
	if !opts.Now.IsZero() {
		fixedNow := opts.Now
		invOpts.Now = func() time.Time { return fixedNow }
	}

	err := NewAWK().Run(context.Background(), commands.NewInvocation(invOpts))
	result := awkCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
	if exitCode, ok := commands.ExitCode(err); ok {
		result.ExitCode = exitCode
		return result
	}
	if err != nil {
		result.ExitCode = 1
		return result
	}
	return result
}
