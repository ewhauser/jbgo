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
	if inv.Exec == nil {
		return fmt.Errorf("%s: subexec callback missing", c.name)
	}
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintf(inv.Stdout, "usage: %s [-c command_string [name [arg ...]]] [script [arg ...]]\n", c.name)
		return nil
	}
	if len(inv.Args) == 0 {
		data, err := io.ReadAll(inv.Stdin)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if len(data) == 0 {
			return nil
		}
		result, err := inv.Exec(ctx, &ExecutionRequest{
			Script:  string(data),
			Env:     inv.Env,
			WorkDir: inv.Dir,
		})
		if err != nil {
			return err
		}
		if err := writeExecutionOutputs(inv, result); err != nil {
			return err
		}
		return exitForExecutionResult(result)
	}

	if inv.Args[0] == "-c" {
		if len(inv.Args) < 2 {
			return exitf(inv, 1, "%s: option requires an argument -- c", c.name)
		}
		script := inv.Args[1]
		var positional []string
		if len(inv.Args) > 3 {
			positional = inv.Args[3:]
		}
		result, err := inv.Exec(ctx, &ExecutionRequest{
			Script:  script,
			Args:    positional,
			Env:     inv.Env,
			WorkDir: inv.Dir,
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

	scriptData, _, err := readAllFile(ctx, inv, inv.Args[0])
	if err != nil {
		return exitf(inv, 127, "%s: %s: No such file or directory", c.name, inv.Args[0])
	}
	result, err := inv.Exec(ctx, &ExecutionRequest{
		Script:  string(scriptData),
		Args:    inv.Args[1:],
		Env:     inv.Env,
		WorkDir: inv.Dir,
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

var _ Command = (*Bash)(nil)
