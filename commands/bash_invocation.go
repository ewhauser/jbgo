package commands

import (
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
	Source         BashSourceMode
	CommandString  string
	ScriptPath     string
	Args           []string
	StartupOptions []string
	RawArgs        []string

	stdinRequested bool
}

type bashParseError struct {
	message string
}

func (e *bashParseError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func ParseBashInvocation(args []string, cfg BashInvocationConfig) (*BashInvocation, error) {
	cfg = normalizeBashInvocationConfig(cfg)
	out := &BashInvocation{
		Name:    cfg.Name,
		RawArgs: append([]string(nil), args...),
	}

	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); {
		arg := args[i]
		switch {
		case arg == "--":
			positionals = append(positionals, args[i+1:]...)
			i = len(args)
			continue
		case strings.HasPrefix(arg, "--"):
			action, handled, err := parseBashLongOption(out, arg, cfg)
			if err != nil {
				return nil, err
			}
			if handled {
				if action != "" {
					out.Action = action
					return out, nil
				}
				i++
				continue
			}
			return nil, &bashParseError{message: fmt.Sprintf("unrecognized option '%s'", arg)}
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			positionals = append(positionals, args[i:]...)
			i = len(args)
			continue
		}

		pending, err := parseBashShortOptions(out, arg, cfg)
		if err != nil {
			return nil, err
		}
		i++
		for _, option := range pending {
			if i >= len(args) {
				return nil, &bashParseError{message: fmt.Sprintf("option requires an argument -- '%c'", option)}
			}
			value := args[i]
			i++
			switch option {
			case 'c':
				out.CommandString = value
			case 'o':
				name, err := normalizeBashStartupOption(value)
				if err != nil {
					return nil, err
				}
				out.StartupOptions = append(out.StartupOptions, name)
			default:
				return nil, &bashParseError{message: fmt.Sprintf("unsupported option parser state for -%c", option)}
			}
		}
		if out.Action != "" {
			return out, nil
		}
	}

	switch {
	case out.CommandString != "":
		out.Source = BashSourceCommandString
		if len(positionals) > 0 {
			out.Args = append(out.Args, positionals[1:]...)
		}
	case out.stdinRequested || len(positionals) == 0:
		out.Source = BashSourceStdin
		out.Args = append(out.Args, positionals...)
	default:
		out.Source = BashSourceFile
		out.ScriptPath = positionals[0]
		out.Args = append(out.Args, positionals[1:]...)
	}
	return out, nil
}

func (inv *BashInvocation) Prelude() string {
	if inv == nil || len(inv.StartupOptions) == 0 {
		return ""
	}
	var b strings.Builder
	for _, name := range inv.StartupOptions {
		if name == "" {
			continue
		}
		_, _ = fmt.Fprintf(&b, "set -o %s\n", name)
	}
	return b.String()
}

func (inv *BashInvocation) ApplyPrelude(script string) string {
	if inv == nil || len(inv.StartupOptions) == 0 {
		return script
	}
	return inv.Prelude() + script
}

func (inv *BashInvocation) ExecutionName() string {
	if inv == nil {
		return "stdin"
	}
	switch inv.Source {
	case BashSourceCommandString:
		return inv.Name
	case BashSourceFile:
		if strings.TrimSpace(inv.ScriptPath) != "" {
			return inv.ScriptPath
		}
		return inv.Name
	default:
		return "stdin"
	}
}

func (inv *BashInvocation) BuildExecutionRequest(env map[string]string, cwd string, stdin io.Reader, script string) *ExecutionRequest {
	if inv == nil {
		return &ExecutionRequest{Script: script, Stdin: stdin}
	}
	req := &ExecutionRequest{
		Name:            inv.ExecutionName(),
		Interpreter:     inv.Name,
		PassthroughArgs: append([]string(nil), inv.RawArgs...),
		Script:          inv.ApplyPrelude(script),
		Args:            append([]string(nil), inv.Args...),
		Env:             env,
		WorkDir:         cwd,
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

func RenderBashInvocationHelp(w io.Writer, cfg BashInvocationConfig) error {
	cfg = normalizeBashInvocationConfig(cfg)
	lines := []string{
		fmt.Sprintf("Usage: %s", bashInvocationUsage(cfg)),
		"",
		"Options:",
		"  -c command_string  read commands from command_string",
		"  -s                 read commands from standard input",
	}
	if cfg.AllowInteractive {
		lines = append(lines,
			"  -i                 run an interactive shell session",
		)
		if cfg.LongInteractive {
			lines = append(lines, "      --interactive  run an interactive shell session")
		}
	}
	lines = append(lines,
		"  -a                 export all assigned variables",
		"  -e                 exit immediately if a command exits non-zero",
		"  -f                 disable pathname expansion",
		"  -n                 read commands but do not execute them",
		"  -u                 treat unset variables as an error",
		"  -x                 print commands and their arguments as they are executed",
		"  -o option          set shell option (allexport, errexit, noglob, noexec, nounset, xtrace, pipefail)",
		"      --help         display this help and exit",
		"      --version      output version information and exit",
	)
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
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
	parts = append(parts, "[-aefnux]", "[-o option]", "[-c command_string [name [arg ...]]]", "[-s]", "[script [arg ...]]")
	return strings.Join(parts, " ")
}

func parseBashLongOption(out *BashInvocation, arg string, cfg BashInvocationConfig) (action string, handled bool, err error) {
	name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
	switch name {
	case "help":
		if hasValue {
			return "", false, &bashParseError{message: "option '--help' doesn't allow an argument"}
		}
		return "help", true, nil
	case "version":
		if hasValue {
			return "", false, &bashParseError{message: "option '--version' doesn't allow an argument"}
		}
		return "version", true, nil
	case "interactive":
		if !cfg.AllowInteractive {
			return "", false, nil
		}
		if hasValue {
			return "", false, &bashParseError{message: "option '--interactive' doesn't allow an argument"}
		}
		out.Interactive = true
		return "", true, nil
	default:
		if hasValue {
			return "", false, &bashParseError{message: fmt.Sprintf("unrecognized option '--%s=%s'", name, value)}
		}
		return "", false, nil
	}
}

func parseBashShortOptions(out *BashInvocation, arg string, cfg BashInvocationConfig) ([]rune, error) {
	shorts := strings.TrimPrefix(arg, "-")
	pending := make([]rune, 0, 2)
	for _, ch := range shorts {
		switch ch {
		case 'a':
			out.StartupOptions = append(out.StartupOptions, "allexport")
		case 'c':
			pending = append(pending, 'c')
		case 'e':
			out.StartupOptions = append(out.StartupOptions, "errexit")
		case 'f':
			out.StartupOptions = append(out.StartupOptions, "noglob")
		case 'i':
			if !cfg.AllowInteractive {
				return nil, &bashParseError{message: fmt.Sprintf("invalid option -- '%c'", ch)}
			}
			out.Interactive = true
		case 'n':
			out.StartupOptions = append(out.StartupOptions, "noexec")
		case 'o':
			pending = append(pending, 'o')
		case 's':
			out.stdinRequested = true
		case 'u':
			out.StartupOptions = append(out.StartupOptions, "nounset")
		case 'x':
			out.StartupOptions = append(out.StartupOptions, "xtrace")
		default:
			return nil, &bashParseError{message: fmt.Sprintf("invalid option -- '%c'", ch)}
		}
	}
	return pending, nil
}

func normalizeBashStartupOption(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "allexport":
		return "allexport", nil
	case "errexit":
		return "errexit", nil
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
		return "", &bashParseError{message: fmt.Sprintf("invalid option name %q", value)}
	}
}
