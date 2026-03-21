package builtins

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type BashSourceMode int

const (
	BashSourceStdin BashSourceMode = iota
	BashSourceCommandString
	BashSourceFile
)

type BashInvocationConfig struct {
	Name             string
	AllowInteractive bool
	LongInteractive  bool
}

type BashInvocation struct {
	Name           string
	Action         string
	Interactive    bool
	LoginShell     bool
	NoRc           bool
	NoProfile      bool
	Source         BashSourceMode
	Rcfile         string
	ExecutionName  string
	CommandString  string
	ScriptPath     string
	Args           []string
	StartupOptions []string
	RawArgs        []string
}

func ParseBashInvocation(args []string, cfg BashInvocationConfig) (*BashInvocation, error) {
	cfg = normalizeBashInvocationConfig(cfg)
	spec := BashInvocationSpec(cfg)
	matches, _, err := ParseCommandSpec(&Invocation{Args: append([]string(nil), args...)}, &spec)
	if err != nil {
		return nil, normalizeBashParseError(cfg.Name, err)
	}
	return bashInvocationFromParsed(cfg, matches, args)
}

func BashInvocationSpec(cfg BashInvocationConfig) CommandSpec {
	cfg = normalizeBashInvocationConfig(cfg)
	options := []OptionSpec{
		{Name: "allexport", Short: 'a', Help: "export all assigned variables"},
		{Name: "command", Short: 'c', ValueName: "command_string", Arity: OptionRequiredValue, Help: "read commands from command_string"},
		{Name: "errexit", Short: 'e', Help: "exit immediately if a command exits non-zero"},
		{Name: "noglob", Short: 'f', Help: "disable pathname expansion"},
		{Name: "login", Short: 'l', Long: "login", Help: "act as if invoked as a login shell"},
		{Name: "noexec", Short: 'n', Help: "read commands but do not execute them"},
		{Name: "noprofile", Long: "noprofile", Help: "do not read the system-wide or personal startup files"},
		{Name: "norc", Long: "norc", Help: "do not read ~/.bashrc for interactive shells"},
		{Name: "option", Short: 'o', ValueName: "option", Arity: OptionRequiredValue, Help: "set shell option (allexport, errexit, noglob, noexec, nounset, xtrace, pipefail)"},
		{Name: "rcfile", Long: "rcfile", ValueName: "file", Arity: OptionRequiredValue, Help: "execute commands from file instead of the standard personal initialization file"},
		{Name: "stdin", Short: 's', Help: "read commands from standard input"},
		{Name: "nounset", Short: 'u', Help: "treat unset variables as an error"},
		{Name: "xtrace", Short: 'x', Help: "print commands and their arguments as they are executed"},
		{Name: "help", Long: "help", Help: "display this help and exit"},
		{Name: "version", Long: "version", Help: "output version information and exit"},
	}
	if cfg.AllowInteractive {
		option := OptionSpec{Name: "interactive", Short: 'i', Help: "run an interactive shell session"}
		if cfg.LongInteractive {
			option.Long = "interactive"
		}
		options = append(options[:4], append([]OptionSpec{option}, options[4:]...)...)
	}

	return CommandSpec{
		Name:    cfg.Name,
		Usage:   bashInvocationUsage(cfg),
		Options: options,
		Args: []ArgSpec{
			{Name: "arg", ValueName: "arg", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ContinueShortGroupValues: true,
			LongOptionValueEquals:    true,
			StopAtFirstPositional:    true,
		},
	}
}

func (inv *BashInvocation) BuildExecutionRequest(env map[string]string, cwd string, stdin io.Reader, script string) *ExecutionRequest {
	if inv == nil {
		return &ExecutionRequest{Script: script, Stdin: stdin}
	}
	req := &ExecutionRequest{
		Name:            inv.ExecutionName,
		Interpreter:     inv.Name,
		PassthroughArgs: append([]string(nil), inv.RawArgs...),
		ScriptPath:      inv.ScriptPath,
		Script:          script,
		Args:            append([]string(nil), inv.Args...),
		StartupOptions:  append([]string(nil), inv.StartupOptions...),
		Env:             env,
		WorkDir:         cwd,
		Interactive:     inv.Interactive,
		Stdin:           stdin,
	}
	if len(req.PassthroughArgs) == 0 {
		req.PassthroughArgs = []string{"-s"}
	}
	return req
}

func RenderBashInvocationUsage(w io.Writer, cfg BashInvocationConfig) error {
	_, err := fmt.Fprintf(w, "usage: %s\n", bashInvocationUsage(normalizeBashInvocationConfig(cfg)))
	return err
}

func normalizeBashInvocationConfig(cfg BashInvocationConfig) BashInvocationConfig {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		cfg.Name = "bash"
	}
	return cfg
}

func bashInvocationUsage(cfg BashInvocationConfig) string {
	parts := []string{cfg.Name}
	if cfg.AllowInteractive {
		parts = append(parts, "[-i]")
	}
	parts = append(parts, "[-aeflnux]", "[-o option]", "[--login]", "[--noprofile]", "[--norc]", "[--rcfile file]", "[-c command_string [name [arg ...]]]", "[-s]", "[script [arg ...]]")
	return strings.Join(parts, " ")
}

func bashInvocationFromParsed(cfg BashInvocationConfig, matches *ParsedCommand, rawArgs []string) (*BashInvocation, error) {
	cfg = normalizeBashInvocationConfig(cfg)
	out := &BashInvocation{
		Name:    cfg.Name,
		RawArgs: append([]string(nil), rawArgs...),
	}
	if matches == nil {
		return out, nil
	}
	switch {
	case matches.Has("help"):
		out.Action = "help"
	case matches.Has("version"):
		out.Action = "version"
	}
	out.Interactive = matches.Has("interactive")
	out.LoginShell = matches.Has("login")
	out.NoProfile = matches.Has("noprofile")
	out.NoRc = matches.Has("norc")
	out.Rcfile = matches.Value("rcfile")

	appendStartup := func(name string) {
		if matches.Has(name) {
			out.StartupOptions = append(out.StartupOptions, name)
		}
	}
	appendStartup("allexport")
	appendStartup("errexit")
	appendStartup("noglob")
	appendStartup("noexec")
	appendStartup("nounset")
	appendStartup("xtrace")
	for _, value := range matches.Values("option") {
		name, err := normalizeBashStartupOption(value)
		if err != nil {
			return nil, err
		}
		out.StartupOptions = append(out.StartupOptions, name)
	}

	positionals := matches.Args("arg")
	switch {
	case matches.Has("command"):
		out.Source = BashSourceCommandString
		out.CommandString = matches.Value("command")
		out.ExecutionName = out.Name
		if len(positionals) > 0 {
			out.ExecutionName = positionals[0]
			out.Args = append(out.Args, positionals[1:]...)
		}
	case matches.Has("stdin") || len(positionals) == 0:
		out.Source = BashSourceStdin
		out.ExecutionName = out.Name
		out.Args = append(out.Args, positionals...)
	default:
		out.Source = BashSourceFile
		out.ScriptPath = positionals[0]
		out.ExecutionName = out.ScriptPath
		out.Args = append(out.Args, positionals[1:]...)
	}
	return out, nil
}

func normalizeBashParseError(name string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	prefix := strings.TrimSpace(name)
	if prefix != "" {
		prefix += ": "
		msg = strings.TrimPrefix(msg, prefix)
	}
	return errors.New(msg)
}

func normalizeBashStartupOption(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "allexport":
		return "allexport", nil
	case "errtrace":
		return "errtrace", nil
	case "errexit":
		return "errexit", nil
	case "functrace":
		return "functrace", nil
	case "noglob":
		return "noglob", nil
	case "noexec":
		return "noexec", nil
	case "nounset":
		return "nounset", nil
	case "pipefail":
		return "pipefail", nil
	case "xtrace":
		return "xtrace", nil
	default:
		return "", fmt.Errorf("invalid option name %q", value)
	}
}
