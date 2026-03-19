package builtins

import (
	"context"
	"fmt"

	"github.com/ewhauser/gbash/internal/printfutil"
	"github.com/ewhauser/gbash/internal/shellstate"
)

type Printf struct{}

func NewPrintf() *Printf {
	return &Printf{}
}

func (c *Printf) Name() string {
	return "printf"
}

func (c *Printf) Run(ctx context.Context, inv *Invocation) error {
	varName, assign, args, err := normalizePrintfArgs(inv.Args)
	if err != nil {
		return exitf(inv, 2, "printf: %v", err)
	}
	out, err := printfutil.Format(args[0], args[1:])
	if err != nil {
		return exitf(inv, 1, "printf: %v", err)
	}
	if assign {
		if assignments := shellstate.ShellVarAssignmentsFromContext(ctx); assignments != nil {
			assignments.Set(varName, out)
			return nil
		}
		if inv.Env == nil {
			inv.Env = make(map[string]string)
		}
		inv.Env[varName] = out
		return nil
	}
	if _, err := fmt.Fprint(inv.Stdout, out); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func normalizePrintfArgs(args []string) (varName string, assign bool, normalized []string, err error) {
	if len(args) == 0 {
		return "", false, nil, fmt.Errorf("missing format")
	}
	if args[0] == "--" {
		if len(args) == 1 {
			return "", false, nil, fmt.Errorf("missing format")
		}
		return "", false, args[1:], nil
	}
	if args[0] != "-v" {
		return "", false, args, nil
	}
	if len(args) < 2 {
		return "", false, nil, fmt.Errorf("-v: option requires a variable name")
	}
	varName = args[1]
	if !isPrintfVarName(varName) {
		return "", false, nil, fmt.Errorf("%q: invalid variable name for -v", varName)
	}
	args = args[2:]
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return "", false, nil, fmt.Errorf("missing format")
	}
	return varName, true, args, nil
}

func isPrintfVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case i == 0 && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'):
		case i > 0 && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9'):
		default:
			return false
		}
	}
	return true
}

var _ Command = (*Printf)(nil)
