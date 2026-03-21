package interp

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func TestLookupHandlerContextOutsideHandler(t *testing.T) {
	t.Parallel()

	if _, ok := LookupHandlerContext(context.Background()); ok {
		t.Fatal("LookupHandlerContext should report false outside handler execution")
	}
}

func TestLookupHandlerContextInExecHandler(t *testing.T) {
	t.Parallel()

	file, err := syntax.NewParser().Parse(strings.NewReader("external\n"), "exec-handler.sh")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}

	var (
		seen bool
		hc   *HandlerContext
	)
	runner, err := NewRunner(&RunnerConfig{
		Dir:   "/sandbox",
		Stdin: strings.NewReader(""),
		ExecHandler: func(ctx context.Context, args []string) error {
			var ok bool
			hc, ok = LookupHandlerContext(ctx)
			seen = ok
			if !ok {
				return nil
			}
			if len(args) != 1 || args[0] != "external" {
				t.Fatalf("args = %q, want [external]", args)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	if err := runner.Run(context.Background(), file); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if !seen {
		t.Fatal("exec handler did not receive a HandlerContext")
	}
	if hc.Dir != "/sandbox" {
		t.Fatalf("HandlerContext.Dir = %q, want /sandbox", hc.Dir)
	}
	if !hc.Pos.IsValid() {
		t.Fatal("HandlerContext.Pos should be valid inside exec handler")
	}
	if hc.Stdin == nil {
		t.Fatal("HandlerContext.Stdin should be set inside exec handler")
	}
}

func TestLookupHandlerContextInProcSubstHandler(t *testing.T) {
	t.Parallel()

	file, err := syntax.NewParser().Parse(strings.NewReader("echo <(printf hi)\n"), "proc-subst.sh")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}

	var (
		seen bool
		hc   *HandlerContext
	)
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/sandbox",
		Stdout: io.Discard,
		Stderr: io.Discard,
		ProcSubstHandler: func(ctx context.Context, ps *syntax.ProcSubst) (*ProcSubstEndpoint, error) {
			var ok bool
			hc, ok = LookupHandlerContext(ctx)
			seen = ok
			if !ok {
				return nil, nil
			}
			if ps.Op != syntax.CmdIn {
				t.Fatalf("ProcSubst op = %v, want %v", ps.Op, syntax.CmdIn)
			}
			return &ProcSubstEndpoint{
				Path:   "/sandbox/.procsub",
				Writer: nopWriteCloser{Writer: io.Discard},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	if err := runner.Run(context.Background(), file); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if !seen {
		t.Fatal("process substitution handler did not receive a HandlerContext")
	}
	if hc.Dir != "/sandbox" {
		t.Fatalf("HandlerContext.Dir = %q, want /sandbox", hc.Dir)
	}
	if !hc.Pos.IsValid() {
		t.Fatal("HandlerContext.Pos should be valid inside process substitution handler")
	}
}

func TestShellStateAccessors(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{
		Env: expand.ListEnviron("HOME=/sandbox"),
		Dir: "/sandbox",
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	if env := runner.ShellEnv(); env != nil {
		t.Fatalf("ShellEnv before reset = %v, want nil", env)
	}
	if got, ok := runner.ShellVarString("FOO"); ok || got != "" {
		t.Fatalf("ShellVarString before set = (%q, %v), want (\"\", false)", got, ok)
	}

	if err := runner.SetShellVar("FOO", expand.Variable{Set: true, Kind: expand.String, Str: "bar"}); err != nil {
		t.Fatalf("SetShellVar error = %v", err)
	}
	if got, ok := runner.ShellVarString("FOO"); !ok || got != "bar" {
		t.Fatalf("ShellVarString after set = (%q, %v), want (bar, true)", got, ok)
	}
	if env := runner.ShellEnv(); env["FOO"] != "bar" {
		t.Fatalf("ShellEnv[FOO] = %q, want bar", env["FOO"])
	}

	if err := runner.UnsetShellVar("FOO"); err != nil {
		t.Fatalf("UnsetShellVar error = %v", err)
	}
	if got, ok := runner.ShellVarString("FOO"); ok || got != "" {
		t.Fatalf("ShellVarString after unset = (%q, %v), want (\"\", false)", got, ok)
	}
	if env := runner.ShellEnv(); env["FOO"] != "" {
		t.Fatalf("ShellEnv should not contain FOO after unset: %v", env)
	}
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }
