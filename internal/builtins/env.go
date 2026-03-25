package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
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
	spec := c.Spec()
	normalizedArgs := normalizeEnvDashAlias(inv.Args)
	if action, ok := envAutoAction(inv, &spec, normalizedArgs); ok {
		switch action {
		case "help":
			return RenderCommandHelp(inv.Stdout, &spec)
		case "version":
			return RenderCommandVersion(inv.Stdout, &spec)
		}
	}

	parseInv := *inv
	args, err := expandEnvArgs(inv, &spec, normalizedArgs)
	if err != nil {
		return err
	}
	parseInv.Args = args
	parseInv.Stderr = io.Discard

	matches, action, err := ParseCommandSpec(&parseInv, &spec)
	if err != nil {
		return rewriteEnvParseError(inv, err)
	}
	switch action {
	case "help":
		return RenderCommandHelp(inv.Stdout, &spec)
	case "version":
		return RenderCommandVersion(inv.Stdout, &spec)
	default:
		return c.runParsed(ctx, inv, matches)
	}
}

func (c *Env) Spec() CommandSpec {
	return CommandSpec{
		Name:  "env",
		Usage: "env [-0iv] [-a ARG] [-C DIR] [-S STRING] [-u NAME] [NAME=VALUE ...] [COMMAND [ARG...]]",
		Options: []OptionSpec{
			{Name: "ignore-environment", Short: 'i', Long: "ignore-environment", Help: "start with an empty environment"},
			{Name: "null", Short: '0', Long: "null", Help: "end each output line with NUL, not newline"},
			{Name: "chdir", Short: 'C', Long: "chdir", ValueName: "DIR", Arity: OptionRequiredValue, Help: "change working directory to DIR"},
			{Name: "argv0", Short: 'a', Long: "argv0", ValueName: "ARG", Arity: OptionRequiredValue, Help: "pass ARG as argv[0] of the command to execute"},
			{Name: "debug", Short: 'v', Long: "debug", Help: "print verbose information for each processing step"},
			{Name: "split-string", Short: 'S', Long: "split-string", ValueName: "STRING", Arity: OptionRequiredValue, Help: "process and split STRING into separate arguments"},
			{Name: "unset", Short: 'u', Long: "unset", ValueName: "NAME", Arity: OptionRequiredValue, Repeatable: true, Help: "remove variable from the environment"},
		},
		Args: []ArgSpec{
			{Name: "item", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			StopAtFirstPositional:    true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := fmt.Fprintf(w, "usage: %s\n", spec.Usage)
			return err
		},
	}
}

func (c *Env) runParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	replaceEnv := matches.Has("ignore-environment")
	unset := matches.Values("unset")
	for _, name := range unset {
		if err := validateEnvUnsetName(inv, name); err != nil {
			return err
		}
	}

	setPairs, argv := splitEnvAssignments(matches.Args("item"))

	env := buildEnv(inv.Env, replaceEnv, unset, setPairs)
	workDir := inv.Cwd
	argv0, argv0Set := envOptionValue(matches, "argv0")
	nullOutput := matches.Has("null")
	if matches.Has("chdir") {
		if len(argv) == 0 {
			return exitf(inv, 125, "env: must specify command with --chdir (-C)\nTry 'env --help' for more information.")
		}
		resolved, err := resolveEnvWorkingDir(ctx, inv, matches.Value("chdir"))
		if err != nil {
			return err
		}
		workDir = resolved
	}
	if len(argv) == 0 {
		if argv0Set {
			return exitf(inv, 125, "env: must specify command with --argv0 (-a)\nTry 'env --help' for more information.")
		}
		separator := "\n"
		if nullOutput {
			separator = "\x00"
		}
		for _, line := range sortedEnvPairs(env) {
			if _, err := io.WriteString(inv.Stdout, line+separator); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}
	if nullOutput {
		return exitf(inv, 125, "env: cannot specify --null (-0) with command\nTry 'env --help' for more information.")
	}

	searchEnv := env
	if _, ok := searchEnv["PATH"]; !ok {
		searchEnv = mergeStringMap(env)
		if pathValue, ok := inv.Env["PATH"]; ok {
			searchEnv["PATH"] = pathValue
		}
	}
	if matches.Has("debug") && len(argv) > 0 {
		if err := writeEnvDebug(inv, argv, argv0, argv0Set); err != nil {
			return err
		}
	}

	result, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:       argv,
		Env:        env,
		SearchEnv:  searchEnv,
		WorkDir:    workDir,
		ReplaceEnv: true,
		Stdin:      inv.Stdin,
	})
	if err != nil {
		return err
	}
	if result != nil && result.CommandNotFound {
		return envCommandNotFound(inv, argv[0])
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func (c *PrintEnv) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *PrintEnv) NormalizeParseError(inv *Invocation, err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	if inv != nil && inv.Stderr != nil {
		message := strings.TrimSpace(err.Error())
		if message != "" {
			_, _ = io.WriteString(inv.Stderr, message+"\n")
		}
	}
	return &ExitError{Code: 2}
}

func (c *PrintEnv) Spec() CommandSpec {
	return CommandSpec{
		Name:  "printenv",
		Usage: "printenv [NAME...]",
		Options: []OptionSpec{
			{Name: "null", Short: '0', Long: "null", Help: "end each output line with NUL, not newline"},
		},
		Args: []ArgSpec{
			{Name: "name", ValueName: "NAME", Repeatable: true},
		},
		Parse: ParseConfig{
			AutoHelp:    true,
			AutoVersion: true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := fmt.Fprintf(w, "usage: %s\n", spec.Usage)
			return err
		},
	}
}

func (c *PrintEnv) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	names := matches.Args("name")
	separator := "\n"
	if matches.Has("null") {
		separator = "\x00"
	}
	if len(names) == 0 {
		for _, line := range sortedEnvPairs(inv.Env) {
			if _, err := io.WriteString(inv.Stdout, line+separator); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	exitCode := 0
	for _, name := range names {
		value, ok := inv.Env[name]
		if !ok {
			exitCode = 1
			continue
		}
		if _, err := io.WriteString(inv.Stdout, value+separator); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func splitEnvAssignments(args []string) (setPairs map[string]string, argv []string) {
	setPairs = make(map[string]string)
	sawAssignment := false
	for i, arg := range args {
		if arg == "--" && !sawAssignment {
			return setPairs, args[i+1:]
		}
		if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "=") {
			name, value, _ := strings.Cut(arg, "=")
			setPairs[name] = value
			sawAssignment = true
			continue
		}
		return setPairs, args[i:]
	}
	return setPairs, nil
}

func normalizeEnvDashAlias(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	normalized := append([]string(nil), args...)
	for i, arg := range normalized {
		if arg == "--" {
			break
		}
		if arg == "-" {
			normalized[i] = "-i"
			break
		}
		if !strings.HasPrefix(arg, "-") || (strings.Contains(arg, "=") && !strings.HasPrefix(arg, "=")) {
			break
		}
	}
	return normalized
}

func envAutoAction(inv *Invocation, spec *CommandSpec, args []string) (string, bool) {
	if spec == nil {
		return "", false
	}
	parseInv := *inv
	parseInv.Args = args
	parseInv.Stderr = io.Discard
	_, action, err := ParseCommandSpec(&parseInv, spec)
	if err != nil || action == "" {
		return "", false
	}
	return action, true
}

func rewriteEnvParseError(inv *Invocation, err error) error {
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Code == 1 {
		message := err.Error()
		message = envNormalizeShebangParseError(message)
		if inv != nil && inv.Stderr != nil && strings.TrimSpace(message) != "" {
			_, _ = io.WriteString(inv.Stderr, strings.TrimSpace(message)+"\n")
		}
		return &ExitError{Code: 125, Err: errors.New(strings.TrimSpace(message))}
	}
	return err
}

func envNormalizeShebangParseError(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return trimmed
	}
	switch {
	case strings.Contains(trimmed, "invalid option -- ' '"),
		strings.Contains(trimmed, "invalid option -- '\t'"),
		strings.Contains(trimmed, "invalid option -- '\n'"),
		strings.Contains(trimmed, "invalid option -- '\v'"),
		strings.Contains(trimmed, "invalid option -- '\f'"),
		strings.Contains(trimmed, "invalid option -- '\r'"):
		tryHelp := "Try 'env --help' for more information."
		if prefix, ok := strings.CutSuffix(trimmed, tryHelp); ok {
			prefix = strings.TrimRight(prefix, "\n")
			return prefix + "\nenv: use -[v]S to pass options in shebang lines\n" + tryHelp
		}
	}
	return trimmed
}

func validateEnvUnsetName(inv *Invocation, name string) error {
	if name == "" || strings.Contains(name, "=") {
		return exitf(inv, 125, "env: cannot unset %s: Invalid argument", quoteGNUOperand(name))
	}
	return nil
}

func resolveEnvWorkingDir(ctx context.Context, inv *Invocation, dir string) (string, error) {
	_, abs, exists, err := statMaybe(ctx, inv, dir)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", exitf(inv, 125, "env: cannot change directory to %s: No such file or directory", quoteGNUOperand(dir))
	}
	return abs, nil
}

func writeEnvDebug(inv *Invocation, argv []string, argv0Value string, argv0Set bool) error {
	argv0 := argv[0]
	if argv0Set {
		argv0 = argv0Value
	}
	lines := []string{
		fmt.Sprintf("argv0:     %s", quoteGNUOperand(argv0)),
		fmt.Sprintf("executing: %s", argv[0]),
		fmt.Sprintf("   arg[0]= %s", quoteGNUOperand(argv0)),
	}
	_, err := io.WriteString(inv.Stderr, strings.Join(lines, "\n")+"\n")
	return err
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
	maps.Copy(env, pairs)
	return env
}

func mergeStringMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	maps.Copy(out, src)
	return out
}

func envOptionValue(matches *ParsedCommand, name string) (string, bool) {
	if matches == nil {
		return "", false
	}
	occurrences := matches.OptionOccurrences()
	if len(occurrences) == 0 {
		return "", false
	}
	for i := len(occurrences); i > 0; i-- {
		occurrence := occurrences[i-1]
		if occurrence.Name != name {
			continue
		}
		if occurrence.HasValue {
			return occurrence.Value, true
		}
		return "", true
	}
	return "", false
}

func envCommandNotFound(inv *Invocation, name string) error {
	if strings.ContainsAny(name, " \t\n\v\f\r") {
		return exitf(inv, 127, "env: %s: No such file or directory\nenv: use -[v]S to pass options in shebang lines", quoteGNUOperand(name))
	}
	return exitf(inv, 127, "env: %s: No such file or directory", quoteGNUOperand(name))
}

var _ Command = (*Env)(nil)
var _ Command = (*PrintEnv)(nil)
var _ SpecProvider = (*Env)(nil)
var _ SpecProvider = (*PrintEnv)(nil)
var _ ParsedRunner = (*PrintEnv)(nil)
var _ ParseErrorNormalizer = (*PrintEnv)(nil)
