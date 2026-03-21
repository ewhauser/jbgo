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

	"github.com/ewhauser/gbash/internal/completionutil"
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
	if parsed.Interactive && inv.Stderr != nil {
		if warning := bashInteractiveJobControlWarning(c.name, inv); warning != "" {
			_, _ = io.WriteString(inv.Stderr, warning)
		}
		_, _ = fmt.Fprintf(inv.Stderr, "%s: no job control in this shell\n", c.name)
	}
	if parsed.Interactive && parsed.Source == BashSourceStdin && parsed.Rcfile == "" {
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
	script, err := c.applyRcfile(ctx, inv, parsed, string(data))
	if err != nil {
		return err
	}
	if script == "" {
		return nil
	}
	return c.executeInlineScript(ctx, inv, parsed, script, nil)
}

func (c *Bash) executeInlineScript(ctx context.Context, inv *Invocation, parsed *BashInvocation, script string, stdin io.Reader) error {
	script, err := c.applyRcfile(ctx, inv, parsed, script)
	if err != nil {
		return err
	}
	result, err := inv.Exec(ctx, parsed.BuildExecutionRequest(inv.Env, inv.Cwd, stdin, script))
	if err != nil {
		return err
	}
	if result != nil && result.Stderr != "" {
		if normalized, ok := normalizeNestedShellArithStderr(script, result.Stderr); ok {
			result.Stderr = normalized
		}
	}
	commandString := parsed != nil && parsed.Source == BashSourceCommandString
	if commandString {
		prefixNestedShellDiagnostic(result, parsed.ExecutionName)
	}
	if result != nil && result.Stderr != "" {
		result.Stderr = prefixNestedShellCommandNotFound(c.name, result.Stderr)
		result.Stderr = prefixNestedShellBuiltinWarnings(c.name, result.Stderr)
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func normalizeNestedShellArithStderr(script, stderr string) (string, bool) {
	if !strings.Contains(stderr, "not a valid arithmetic operator") {
		return "", false
	}
	for search := 0; search < len(script); {
		startRel := strings.Index(script[search:], "$((")
		if startRel < 0 {
			return "", false
		}
		exprStart := search + startRel + len("$((")
		endRel := strings.Index(script[exprStart:], "))")
		if endRel < 0 {
			return "", false
		}
		exprEnd := exprStart + endRel
		exprRaw := script[exprStart:exprEnd]
		tokenText, ok := nestedShellArithCRToken(exprRaw)
		if ok {
			exprText := strings.Trim(exprRaw, " \t\n")
			if exprText == "" {
				return "", false
			}
			return fmt.Sprintf("%s: syntax error: operand expected (error token is %s)\n", exprText, bashQuoteArithToken(tokenText)), true
		}
		search = exprEnd + len("))")
	}
	return "", false
}

func nestedShellArithCRToken(exprRaw string) (string, bool) {
	tokenStart := strings.IndexByte(exprRaw, '\r')
	if tokenStart < 0 {
		return "", false
	}
	tokenEnd := tokenStart + 1
	if tokenEnd < len(exprRaw) && exprRaw[tokenEnd] == '\n' {
		tokenEnd++
	}
	for tokenEnd < len(exprRaw) && !isNestedShellArithTokenDelimiter(exprRaw[tokenEnd]) {
		tokenEnd++
	}
	if tokenEnd <= tokenStart {
		return "", false
	}
	return exprRaw[tokenStart:tokenEnd], true
}

func isNestedShellArithTokenDelimiter(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '+', '-', '*', '/', '%', '(', ')', '<', '>', '=', '&', '|', '^', '?', ':', ',', ';':
		return true
	default:
		return false
	}
}

func bashQuoteArithToken(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`
}

func (c *Bash) applyRcfile(ctx context.Context, inv *Invocation, parsed *BashInvocation, script string) (string, error) {
	if parsed == nil || !parsed.Interactive || parsed.NoRc || strings.TrimSpace(parsed.Rcfile) == "" {
		return script, nil
	}
	data, _, err := readAllFile(ctx, inv, parsed.Rcfile)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return "", exitf(inv, 1, "%s: %s: No such file or directory", c.name, parsed.Rcfile)
		}
		return "", exitf(inv, 1, "%s: %s: %s", c.name, parsed.Rcfile, readAllErrorText(err))
	}
	rc := string(data)
	switch {
	case rc == "":
		return script, nil
	case script == "":
		return rc, nil
	case strings.HasSuffix(rc, "\n"):
		return rc + script, nil
	default:
		return rc + "\n" + script, nil
	}
}

func prefixNestedShellCommandNotFound(name, stderr string) string {
	prefix := strings.TrimSpace(name)
	if prefix == "" || stderr == "" {
		return stderr
	}
	lines := strings.SplitAfter(stderr, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		if trimmed == "" || strings.HasPrefix(trimmed, prefix+": ") {
			continue
		}
		targetLine := trimmed
		if rest, ok := strings.CutPrefix(targetLine, prefix+": "); ok {
			targetLine = rest
		}
		target, ok := strings.CutSuffix(targetLine, ": command not found")
		if !ok {
			continue
		}
		suffix := line[len(trimmed):]
		if shouldPrefixNestedCommandNotFound(target) {
			lines[i] = prefix + ": " + target + ": command not found" + suffix
		} else {
			lines[i] = target + ": command not found" + suffix
		}
	}
	return strings.Join(lines, "")
}

func prefixNestedShellBuiltinWarnings(name, stderr string) string {
	prefix := strings.TrimSpace(name)
	if prefix == "" || stderr == "" {
		return stderr
	}
	lines := strings.SplitAfter(stderr, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		if trimmed == "" || strings.HasPrefix(trimmed, prefix+": ") {
			continue
		}
		cmdName, rest, ok := strings.Cut(trimmed, ": ")
		if !ok || !completionutil.IsBuiltinName(cmdName) || !strings.HasPrefix(rest, "warning:") {
			continue
		}
		suffix := line[len(trimmed):]
		lines[i] = prefix + ": " + trimmed + suffix
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
		normalized := strings.TrimRight(strings.TrimLeft(lines[i], " \t"), " \t")
		prefixed, ok := nestedShellDiagnosticLine(normalized, shellName)
		if !ok {
			return
		}
		lines[i] = prefixed
		result.Stderr = strings.Join(lines, "\n")
		if hadTrailingNewline {
			result.Stderr += "\n"
		}
		return
	}
}

func nestedShellDiagnosticLine(line, shellName string) (string, bool) {
	if line == "" {
		return "", false
	}
	if shellName != "" {
		if rest, ok := strings.CutPrefix(line, shellName+": "); ok {
			if isNestedShellDiagnosticText(rest) {
				return shellName + ": line 1: " + rest, true
			}
			return "", false
		}
	}
	if !isNestedShellDiagnosticText(line) {
		return "", false
	}
	return shellName + ": line 1: " + line, true
}

func isNestedShellDiagnosticText(line string) bool {
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
	case strings.Contains(line, "syntax error: operand expected"):
		return true
	case strings.Contains(line, "syntax error in expression"):
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
