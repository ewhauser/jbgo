package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/ewhauser/gbash/policy"
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
	parseInv := *inv
	parseInv.Args = normalizeEnvDashAlias(inv.Args)

	spec := c.Spec()
	matches, action, err := ParseCommandSpec(&parseInv, &spec)
	if err != nil {
		return rewriteEnvParseError(err)
	}
	switch action {
	case "help":
		return RenderCommandHelp(inv.Stdout, &spec)
	case "version":
		return RenderCommandVersion(inv.Stdout, &spec)
	default:
		return c.RunParsed(ctx, inv, matches)
	}
}

func (c *Env) Spec() CommandSpec {
	return CommandSpec{
		Name:  "env",
		Usage: "env [-i] [-u NAME] [NAME=VALUE ...] [COMMAND [ARG...]]",
		Options: []OptionSpec{
			{Name: "ignore-environment", Short: 'i', Long: "ignore-environment", Help: "start with an empty environment"},
			{Name: "chdir", Short: 'C', Long: "chdir", ValueName: "DIR", Arity: OptionRequiredValue, Help: "change working directory to DIR"},
			{Name: "argv0", Short: 'a', Long: "argv0", ValueName: "ARG", Arity: OptionRequiredValue, Help: "pass ARG as argv[0] of the command to execute"},
			{Name: "debug", Short: 'v', Long: "debug", Help: "print verbose information for each processing step"},
			{Name: "unset", Short: 'u', Long: "unset", ValueName: "NAME", Arity: OptionRequiredValue, Repeatable: true, Help: "remove variable from the environment"},
		},
		Args: []ArgSpec{
			{Name: "item", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
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

func (c *Env) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	replaceEnv := matches.Has("ignore-environment")
	unset := matches.Values("unset")
	for _, name := range unset {
		if err := validateEnvUnsetName(inv, name); err != nil {
			return err
		}
	}

	setPairs, argv := splitEnvAssignments(matches.Args("item"))
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}

	env := buildEnv(inv.Env, replaceEnv, unset, setPairs)
	workDir := inv.Cwd
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
		for _, line := range sortedEnvPairs(env) {
			if _, err := fmt.Fprintln(inv.Stdout, line); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	searchEnv := env
	if _, ok := searchEnv["PATH"]; !ok {
		searchEnv = mergeStringMap(env)
		if pathValue, ok := inv.Env["PATH"]; ok {
			searchEnv["PATH"] = pathValue
		}
	}
	if matches.Has("debug") && len(argv) > 0 {
		if err := writeEnvDebug(inv, argv, matches); err != nil {
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
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func (c *PrintEnv) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
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
	for i, arg := range args {
		if arg == "--" {
			return setPairs, args[i+1:]
		}
		if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "=") {
			name, value, _ := strings.Cut(arg, "=")
			setPairs[name] = value
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

func rewriteEnvParseError(err error) error {
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Code == 1 {
		return &ExitError{Code: 125, Err: exitErr.Err}
	}
	return err
}

func validateEnvUnsetName(inv *Invocation, name string) error {
	if name == "" || strings.Contains(name, "=") {
		return exitf(inv, 125, "env: cannot unset %s: Invalid argument", quoteGNUOperand(name))
	}
	return nil
}

func resolveEnvWorkingDir(ctx context.Context, inv *Invocation, dir string) (string, error) {
	_, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, dir)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", exitf(inv, 125, "env: cannot change directory to %s: No such file or directory", quoteGNUOperand(dir))
	}
	return abs, nil
}

func writeEnvDebug(inv *Invocation, argv []string, matches *ParsedCommand) error {
	argv0 := argv[0]
	if matches.Has("argv0") {
		argv0 = matches.Value("argv0")
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

var _ Command = (*Env)(nil)
var _ Command = (*PrintEnv)(nil)
var _ SpecProvider = (*Env)(nil)
var _ ParsedRunner = (*Env)(nil)
var _ SpecProvider = (*PrintEnv)(nil)
var _ ParsedRunner = (*PrintEnv)(nil)
