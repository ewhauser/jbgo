package commands

import (
	"context"
	"fmt"
	"io"
)

type Bash struct {
	name string
}

func NewBash() *Bash {
	return &Bash{name: "bash"}
}

func NewSh() *Bash {
	return &Bash{name: "sh"}
}

func (c *Bash) Name() string {
	return c.name
}

func (c *Bash) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Bash) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.name,
		Usage: c.name + " [-c command_string [name [arg ...]]] [script [arg ...]]",
		Options: []OptionSpec{
			{Name: "command", Short: 'c', ValueName: "command_string", Arity: OptionRequiredValue, Help: "read commands from command_string"},
			{Name: "stdin", Short: 's', Help: "read commands from standard input"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "arg", Repeatable: true},
		},
		Parse: ParseConfig{
			StopAtFirstPositional: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := fmt.Fprintf(w, "usage: %s\n", spec.Usage)
			return err
		},
	}
}

func (c *Bash) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if inv.Exec == nil {
		return fmt.Errorf("%s: subexec callback missing", c.name)
	}

	if matches.Has("command") {
		positional := matches.Args("arg")
		if len(positional) > 0 {
			positional = positional[1:]
		}
		return c.executeInlineScript(ctx, inv, matches.Value("command"), positional, inv.Stdin)
	}

	if matches.Has("stdin") || len(matches.Args("arg")) == 0 {
		return c.executeStdinScript(ctx, inv, matches.Args("arg"))
	}

	args := matches.Args("arg")
	scriptData, _, err := readAllFile(ctx, inv, args[0])
	if err != nil {
		return exitf(inv, 127, "%s: %s: No such file or directory", c.name, args[0])
	}
	result, err := inv.Exec(ctx, &ExecutionRequest{
		Script:  string(scriptData),
		Args:    args[1:],
		Env:     inv.Env,
		WorkDir: inv.Cwd,
		Stdin:   inv.Stdin,
	})
	if err != nil {
		return err
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func (c *Bash) executeStdinScript(ctx context.Context, inv *Invocation, positional []string) error {
	data, err := io.ReadAll(inv.Stdin)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if len(data) == 0 {
		return nil
	}
	return c.executeInlineScript(ctx, inv, string(data), positional, nil)
}

func (c *Bash) executeInlineScript(ctx context.Context, inv *Invocation, script string, positional []string, stdin io.Reader) error {
	result, err := inv.Exec(ctx, &ExecutionRequest{
		Script:  script,
		Args:    positional,
		Env:     inv.Env,
		WorkDir: inv.Cwd,
		Stdin:   stdin,
	})
	if err != nil {
		return err
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

var _ Command = (*Bash)(nil)
var _ SpecProvider = (*Bash)(nil)
var _ ParsedRunner = (*Bash)(nil)
