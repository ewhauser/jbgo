package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"runtime"
	"slices"
	"strconv"
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
		if warning := bashInteractiveJobControlWarning(ctx, c.name, inv); warning != "" {
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
		if err := ValidateShellScriptFileData(parsed.ScriptPath, scriptData); err != nil {
			return exitf(inv, 126, "%s", err)
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
	req := parsed.BuildExecutionRequest(inv.Env, inv.Cwd, stdin, script)
	// When stdout and stderr point to the same writer (e.g. 2>&1),
	// pass them through so the child session writes directly and
	// output interleaving between trace and command output is preserved.
	passthrough := inv.Stdout != nil && inv.Stdout == inv.Stderr
	if passthrough {
		req.Stdout = inv.Stdout
		req.Stderr = inv.Stderr
	}
	result, err := inv.Exec(ctx, req)
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
	if passthrough {
		// Output was already written via the passthrough writers,
		// so skip writeExecutionOutputs to avoid duplicated output.
		// Note: stderr normalization above (bash: prefix, etc.) runs on
		// the captured result but cannot retroactively fix what was
		// already streamed. This means error prefixes are lost when
		// stderr is merged with stdout; the proper fix is for the child
		// session to emit the prefix itself.
		return exitForExecutionResult(result)
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func normalizeNestedShellArithStderr(script, stderr string) (string, bool) {
	failureLine, failureToken, ok := nestedShellArithFailure(stderr)
	if !ok {
		return "", false
	}
	replacement := ""
	for search := 0; search < len(script); {
		startRel := strings.Index(script[search:], "$((")
		if startRel < 0 {
			break
		}
		exprStart := search + startRel + len("$((")
		endRel := strings.Index(script[exprStart:], "))")
		if endRel < 0 {
			return "", false
		}
		exprEnd := exprStart + endRel
		exprRaw := script[exprStart:exprEnd]
		tokenText, tokenStart, ok := nestedShellArithCRToken(exprRaw)
		if ok {
			exprLine := nestedShellArithLine(script, exprStart)
			tokenLine := nestedShellArithTokenLine(script, exprStart+tokenStart, tokenText)
			if nestedShellArithFailureToken(tokenText) != failureToken {
				search = exprEnd + len("))")
				continue
			}
			if failureLine > 0 && exprLine != failureLine && tokenLine != failureLine {
				search = exprEnd + len("))")
				continue
			}
			if replacement != "" {
				return "", false
			}
			exprText := strings.Trim(exprRaw, " \t\n")
			if exprText == "" {
				return "", false
			}
			replacement = fmt.Sprintf("%s: syntax error: operand expected (error token is %s)\n", exprText, bashQuoteArithToken(tokenText))
		}
		search = exprEnd + len("))")
	}
	if replacement == "" {
		return "", false
	}
	normalized := stderr
	replaced := false
	if strings.Contains(stderr, "not a valid arithmetic operator") {
		normalized, replaced = replaceNestedShellArithDiagnostic(stderr, replacement)
	}
	withoutContinuation, stripped := stripNestedShellArithContinuation(normalized)
	if replaced || stripped {
		return withoutContinuation, true
	}
	return "", false
}

func nestedShellArithFailure(stderr string) (lineNum int, token string, ok bool) {
	for line := range strings.SplitSeq(stderr, "\n") {
		prefix, remainder, found := strings.Cut(line, "not a valid arithmetic operator: `")
		if !found {
			continue
		}
		token, _, found = strings.Cut(remainder, "`")
		if !found {
			continue
		}
		linePrefix := strings.TrimSuffix(strings.TrimSpace(prefix), ":")
		if linePrefix != "" {
			if trimmed, found := strings.CutPrefix(linePrefix, "bash: "); found {
				linePrefix = trimmed
			}
			if !strings.HasPrefix(linePrefix, "line ") {
				continue
			}
			parsedLine, err := strconv.Atoi(strings.TrimPrefix(linePrefix, "line "))
			if err != nil {
				return 0, "", false
			}
			lineNum = parsedLine
		}
		return lineNum, token, true
	}
	return 0, "", false
}

func replaceNestedShellArithDiagnostic(stderr, replacement string) (string, bool) {
	lines := strings.SplitAfter(stderr, "\n")
	out := make([]string, 0, len(lines))
	replaced := false
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimRight(lines[i], "\n")
		if !strings.Contains(trimmed, "not a valid arithmetic operator") {
			out = append(out, lines[i])
			continue
		}
		out = append(out, replacement)
		replaced = true
		for i+1 < len(lines) {
			next := strings.TrimLeft(strings.TrimRight(lines[i+1], "\n"), " \t")
			if strings.HasPrefix(next, "`") || (strings.HasPrefix(next, "line ") && strings.Contains(next, ": `")) {
				i++
				continue
			}
			break
		}
	}
	if !replaced {
		return "", false
	}
	return strings.Join(out, ""), true
}

func stripNestedShellArithContinuation(stderr string) (string, bool) {
	lines := strings.SplitAfter(stderr, "\n")
	out := make([]string, 0, len(lines))
	stripped := false
	sawOperandExpected := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		switch {
		case strings.Contains(trimmed, "syntax error: operand expected"):
			sawOperandExpected = true
			out = append(out, line)
		case sawOperandExpected && isNestedShellArithContinuationLine(trimmed):
			stripped = true
		default:
			sawOperandExpected = false
			out = append(out, line)
		}
	}
	if !stripped {
		return stderr, false
	}
	return strings.Join(out, ""), true
}

func isNestedShellArithContinuationLine(line string) bool {
	line = strings.TrimLeft(line, " \t")
	if strings.HasPrefix(line, "`") {
		return true
	}
	return strings.HasPrefix(line, "bash: line ") && strings.Contains(line, ": `")
}

func nestedShellArithCRToken(exprRaw string) (string, int, bool) {
	tokenStart := strings.IndexByte(exprRaw, '\r')
	if tokenStart < 0 {
		return "", 0, false
	}
	tokenEnd := tokenStart + 1
	if tokenEnd < len(exprRaw) && exprRaw[tokenEnd] == '\n' {
		tokenEnd++
	}
	for tokenEnd < len(exprRaw) && !isNestedShellArithTokenDelimiter(exprRaw[tokenEnd]) {
		tokenEnd++
	}
	if tokenEnd <= tokenStart {
		return "", 0, false
	}
	return exprRaw[tokenStart:tokenEnd], tokenStart, true
}

func nestedShellArithTokenLine(script string, tokenPos int, tokenText string) int {
	offset := 0
	for offset < len(tokenText) && (tokenText[offset] == '\r' || tokenText[offset] == '\n') {
		offset++
	}
	return nestedShellArithLine(script, tokenPos+offset)
}

func nestedShellArithLine(script string, pos int) int {
	return 1 + strings.Count(script[:pos], "\n")
}

func nestedShellArithFailureToken(tokenText string) string {
	return strings.Trim(tokenText, "\r\n")
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
			if shouldUpgradePrefixedNestedShellDiagnostic(rest) {
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

func shouldUpgradePrefixedNestedShellDiagnostic(line string) bool {
	if line == "" || strings.HasPrefix(line, "line ") {
		return false
	}
	if !strings.Contains(line, "(error token is ") {
		return false
	}
	return isNestedShellDiagnosticText(line)
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
