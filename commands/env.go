package commands

import (
	"context"
	"fmt"
	"strings"
)

type Env struct{}
type PrintEnv struct{}

func NewEnv() *Env {
	return &Env{}
}

func NewPrintEnv() *PrintEnv {
	return &PrintEnv{}
}

func (c *Env) Name() string {
	return "env"
}

func (c *PrintEnv) Name() string {
	return "printenv"
}

func (c *Env) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: env [-i] [-u NAME] [NAME=VALUE ...] [COMMAND [ARG...]]")
		return nil
	}
	replaceEnv, unset, setPairs, argv, err := parseEnvArgs(inv)
	if err != nil {
		return err
	}
	env := buildEnv(inv.Env, replaceEnv, unset, setPairs)
	if len(argv) == 0 {
		for _, line := range sortedEnvPairs(env) {
			if _, err := fmt.Fprintln(inv.Stdout, line); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	result, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:       argv,
		Env:        env,
		SearchEnv:  inv.Env,
		WorkDir:    inv.Dir,
		ReplaceEnv: replaceEnv,
		Stdin:      inv.Stdin,
	})
	if err != nil {
		return err
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func (c *PrintEnv) Run(_ context.Context, inv *Invocation) error {
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: printenv [NAME...]")
		return nil
	}
	if len(inv.Args) == 0 {
		for _, line := range sortedEnvPairs(inv.Env) {
			if _, err := fmt.Fprintln(inv.Stdout, line); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}
	exitCode := 0
	for _, name := range inv.Args {
		value, ok := inv.Env[name]
		if !ok {
			exitCode = 1
			continue
		}
		if _, err := fmt.Fprintln(inv.Stdout, value); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseEnvArgs(inv *Invocation) (replaceEnv bool, unset []string, setPairs map[string]string, argv []string, err error) {
	args := inv.Args
	setPairs = make(map[string]string)
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "-i":
			replaceEnv = true
			args = args[1:]
		case arg == "-u":
			if len(args) < 2 {
				return false, nil, nil, nil, exitf(inv, 1, "env: option requires an argument -- 'u'")
			}
			unset = append(unset, args[1])
			args = args[2:]
		case strings.HasPrefix(arg, "-u") && len(arg) > 2:
			unset = append(unset, arg[2:])
			args = args[1:]
		case strings.Contains(arg, "=") && !strings.HasPrefix(arg, "="):
			name, value, _ := strings.Cut(arg, "=")
			setPairs[name] = value
			args = args[1:]
		default:
			return replaceEnv, unset, setPairs, args, nil
		}
	}
	return replaceEnv, unset, setPairs, args, nil
}

func buildEnv(base map[string]string, replaceEnv bool, unset []string, pairs map[string]string) map[string]string {
	var env map[string]string
	if replaceEnv {
		env = make(map[string]string, len(pairs))
	} else {
		env = mergeStringMap(base)
	}
	for _, name := range unset {
		delete(env, name)
	}
	for name, value := range pairs {
		env[name] = value
	}
	return env
}

func mergeStringMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

var _ Command = (*Env)(nil)
var _ Command = (*PrintEnv)(nil)
