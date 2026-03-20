package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"runtime"
	"slices"
	"strings"
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
			if errors.Is(err, stdfs.ErrNotExist) {
				exitCode := 127
				if slices.Contains(parsed.StartupOptions, "errexit") {
					exitCode = 1
				}
				return exitf(inv, exitCode, "%s: %s: No such file or directory", c.name, parsed.ScriptPath)
			}
			return exitf(inv, 1, "%s: %s: %s", c.name, parsed.ScriptPath, readAllErrorText(err))
		}
		return c.executeInlineScript(ctx, inv, parsed, string(scriptData), inv.Stdin)
	default:
		return c.executeStdinScript(ctx, inv, parsed)
	}
}

func (c *Bash) executeStdinScript(ctx context.Context, inv *Invocation, parsed *BashInvocation) error {
	data, err := readAllReader(ctx, inv, inv.Stdin)
	if err != nil {
		return err
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
	commandString := parsed != nil && parsed.Source == BashSourceCommandString
	if commandString {
		prefixNestedShellDiagnostic(result, parsed.Name)
	}
	if result != nil && result.Stderr != "" {
		result.Stderr = prefixNestedShellCommandNotFound(c.name, result.Stderr, commandString)
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func prefixNestedShellCommandNotFound(name, stderr string, commandString bool) string {
	prefix := strings.TrimSpace(name)
	if prefix == "" || stderr == "" {
		return stderr
	}
	linePrefix := prefix + ": "
	if commandString {
		linePrefix = prefix + ": line 1: "
	}
	lines := strings.SplitAfter(stderr, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		if trimmed == "" || strings.HasPrefix(trimmed, linePrefix) || strings.HasPrefix(trimmed, prefix+": ") {
			continue
		}
		targetLine := trimmed
		if rest, ok := strings.CutPrefix(targetLine, linePrefix); ok {
			targetLine = rest
		} else if rest, ok := strings.CutPrefix(targetLine, prefix+": "); ok {
			targetLine = rest
		}
		target, ok := strings.CutSuffix(targetLine, ": command not found")
		if !ok {
			continue
		}
		suffix := line[len(trimmed):]
		if shouldPrefixNestedCommandNotFound(target) {
			lines[i] = linePrefix + target + ": command not found" + suffix
		} else {
			lines[i] = target + ": command not found" + suffix
		}
	}
	return strings.Join(lines, "")
}

func simpleCommandNotFoundTarget(target string) bool {
	if target == "" {
		return false
	}
	for _, r := range target {
		if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			continue
		}
		switch r {
		case '_', '-', '.', '/':
			continue
		default:
			return false
		}
	}
	return true
}

func shouldPrefixNestedCommandNotFound(target string) bool {
	if target == "" || containsControlRune(target) {
		return false
	}
	if simpleCommandNotFoundTarget(target) {
		return true
	}
	return runtime.GOOS == "darwin"
}

func containsControlRune(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func prefixNestedShellDiagnostic(result *ExecutionResult, shellName string) {
	if result == nil || result.ExitCode == 0 || result.Stderr == "" {
		return
	}
	hadTrailingNewline := strings.HasSuffix(result.Stderr, "\n")
	trimmed := strings.TrimSuffix(result.Stderr, "\n")
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if !shouldPrefixNestedShellDiagnostic(line, shellName) {
			return
		}
		normalized := strings.TrimRight(strings.TrimLeft(lines[i], " \t"), " \t")
		normalized = strings.Replace(normalized, " : ", ": ", 1)
		lines[i] = shellName + ": line 1: " + normalized
		result.Stderr = strings.Join(lines, "\n")
		if hadTrailingNewline {
			result.Stderr += "\n"
		}
		return
	}
}

func shouldPrefixNestedShellDiagnostic(line, shellName string) bool {
	if line == "" {
		return false
	}
	if shellName != "" && strings.HasPrefix(line, shellName+": ") {
		return false
	}
	switch {
	case strings.Contains(line, "unbound variable"):
		return true
	case strings.Contains(line, "bad substitution"):
		return true
	case strings.Contains(line, "value too great for base"):
		return true
	case strings.Contains(line, "invalid number"):
		return true
	case strings.Contains(line, "arithmetic syntax error"):
		return true
	case strings.Contains(line, "division by 0"):
		return true
	default:
		return false
	}
}

var _ Command = (*Bash)(nil)
var _ SpecProvider = (*Bash)(nil)
var _ ParsedRunner = (*Bash)(nil)
