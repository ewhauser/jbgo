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
	spec := BashInvocationSpec(BashInvocationConfig{
		Name:             c.name,
		AllowInteractive: true,
	})
	spec.HelpRenderer = func(w io.Writer, spec CommandSpec) error {
		_, err := fmt.Fprintf(w, "usage: %s\n", spec.Usage)
		return err
	}
	spec.VersionRenderer = func(w io.Writer, _ CommandSpec) error {
		return RenderSimpleVersion(w, c.name)
	}
	return spec
}

func (c *Bash) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if inv.Exec == nil {
		return fmt.Errorf("%s: subexec callback missing", c.name)
	}

	parsed, err := bashInvocationFromParsed(BashInvocationConfig{
		Name:             c.name,
		AllowInteractive: true,
	}, matches, inv.Args)
	if err != nil {
		return exitf(inv, 2, "%v", err)
	}
	switch parsed.Action {
	case "help":
		return RenderBashInvocationUsage(inv.Stdout, BashInvocationConfig{
			Name:             c.name,
			AllowInteractive: true,
		})
	case "version":
		return RenderSimpleVersion(inv.Stdout, c.name)
	}
	if parsed.Interactive && parsed.Source == BashSourceStdin {
		if inv.Interact == nil {
			return fmt.Errorf("%s: interactive callback missing", c.name)
		}
		result, err := inv.Interact(ctx, &InteractiveRequest{
			Name:           parsed.ExecutionName,
			Args:           append([]string(nil), parsed.Args...),
			StartupOptions: append([]string(nil), parsed.StartupOptions...),
			Env:            inv.Env,
			WorkDir:        inv.Cwd,
			ReplaceEnv:     true,
			Stdin:          inv.Stdin,
			Stdout:         inv.Stdout,
			Stderr:         inv.Stderr,
		})
		if err != nil {
			return err
		}
		if result == nil {
			return nil
		}
		return exitForExecutionResult(&ExecutionResult{ExitCode: result.ExitCode})
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
var _ SpecProvider = (*Bash)(nil)
var _ ParsedRunner = (*Bash)(nil)
