package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"

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
	if len(inv.Args) == 0 {
		if inv != nil && inv.Stderr != nil {
			_, _ = io.WriteString(inv.Stderr, "printf: usage: printf [-v var] format [arguments]\n")
		}
		return &ExitError{Code: 2}
	}
	varName, assign, args, err := normalizePrintfArgs(inv.Args)
	if err != nil {
		return exitf(inv, 2, "printf: %v", err)
	}
	result := printfutil.Format(args[0], args[1:], printfutil.Options{
		LookupEnv: func(name string) (string, bool) {
			if inv == nil || inv.Env == nil {
				return "", false
			}
			value, ok := inv.Env[name]
			return value, ok
		},
	})
	for _, diag := range result.Diagnostics {
		if inv != nil && inv.Stderr != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "printf: %s\n", diag)
		}
	}
	if assign {
		if assignments := shellstate.ShellVarAssignmentsFromContext(ctx); assignments != nil {
			assignments.Set(varName, result.Output)
		} else {
			if inv.Env == nil {
				inv.Env = make(map[string]string)
			}
			inv.Env[varName] = result.Output
		}
		if result.ExitCode != 0 {
			return &ExitError{Code: int(result.ExitCode)}
		}
		return nil
	}
	if _, err := io.WriteString(inv.Stdout, result.Output); err != nil {
		if printfBrokenPipe(err) {
			if result.ExitCode != 0 {
				return &ExitError{Code: int(result.ExitCode)}
			}
			return nil
		}
		if diag, ok := shellWriteErrorDiagnostic(err); ok {
			return exitf(inv, 1, "%s", diag)
		}
		return &ExitError{Code: 1, Err: err}
	}
	if result.ExitCode != 0 {
		return &ExitError{Code: int(result.ExitCode)}
	}
	return nil
}

func printfBrokenPipe(err error) bool {
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "broken pipe") || strings.Contains(lower, "closed pipe")
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
		return "", false, nil, fmt.Errorf("`%s': not a valid identifier", varName)
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
