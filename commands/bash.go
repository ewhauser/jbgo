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

	parsed, err := ParseBashInvocation(inv.Args, BashInvocationConfig{Name: c.name})
	if err != nil {
		return exitf(inv, 2, "%v", err)
	}
	switch parsed.Action {
	case "help":
		return RenderBashInvocationUsage(inv.Stdout, BashInvocationConfig{Name: c.name})
	case "version":
		return RenderSimpleVersion(inv.Stdout, c.name)
	}

	switch parsed.Source {
	case BashSourceCommandString:
		return c.executeInlineScript(ctx, inv, parsed, parsed.CommandString, inv.Stdin)
	case BashSourceFile:
		scriptData, _, err := readAllFile(ctx, inv, parsed.ScriptPath)
		if err != nil {
			return exitf(inv, 127, "%s: %s: No such file or directory", c.name, parsed.ScriptPath)
		}
		return c.executeInlineScript(ctx, inv, parsed, string(scriptData), inv.Stdin)
	default:
		return c.executeStdinScript(ctx, inv, parsed)
	}
}

func (c *Bash) executeStdinScript(ctx context.Context, inv *Invocation, parsed *BashInvocation) error {
	data, err := io.ReadAll(inv.Stdin)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if len(data) == 0 {
		return nil
	}
	return c.executeInlineScript(ctx, inv, parsed, string(data), nil)
}

func (c *Bash) executeInlineScript(ctx context.Context, inv *Invocation, parsed *BashInvocation, script string, stdin io.Reader) error {
	result, err := inv.Exec(ctx, parsed.BuildExecutionRequest(inv.Env, inv.Cwd, stdin, script))
	if err != nil {
		return err
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

var _ Command = (*Bash)(nil)
