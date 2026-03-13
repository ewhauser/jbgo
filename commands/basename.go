package commands

import (
	"context"
	"fmt"
	"path"
	"strings"
)

type Basename struct{}

func NewBasename() *Basename {
	return &Basename{}
}

func (c *Basename) Name() string {
	return "basename"
}

func (c *Basename) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Basename) Spec() CommandSpec {
	return CommandSpec{
		Name:  "basename",
		About: "Print NAME with any leading directory components removed.",
		Usage: "basename NAME [SUFFIX]\n  or:  basename OPTION... NAME...",
		Options: []OptionSpec{
			{Name: "multiple", Short: 'a', Long: "multiple", Help: "support multiple arguments and treat each as a NAME"},
			{Name: "suffix", Short: 's', Long: "suffix", ValueName: "SUFFIX", Arity: OptionRequiredValue, Help: "remove a trailing SUFFIX"},
			{Name: "zero", Short: 'z', Long: "zero", Help: "end each output line with NUL, not newline"},
			{Name: "help", Short: 'h', Long: "help", Help: "display this help and exit"},
			{Name: "version", Short: 'V', Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "name", ValueName: "NAME", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			StopAtFirstPositional:    true,
		},
		HelpRenderer:    renderStaticHelp(basenameHelpText),
		VersionRenderer: renderStaticVersion(basenameVersionText),
	}
}

func (c *Basename) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return renderStaticHelp(basenameHelpText)(inv.Stdout, c.Spec())
	}
	if matches.Has("version") {
		return renderStaticVersion(basenameVersionText)(inv.Stdout, c.Spec())
	}

	names := matches.Args("name")
	if len(names) == 0 {
		return exitf(inv, 1, "basename: missing operand\nTry 'basename --help' for more information.")
	}

	multiple := matches.Has("multiple") || matches.Has("suffix")
	suffix := matches.Value("suffix")
	if !multiple {
		switch len(names) {
		case 1:
		case 2:
			suffix = names[1]
			names = names[:1]
		default:
			return exitf(inv, 1, "basename: extra operand %s\nTry 'basename --help' for more information.", quoteGNUOperand(names[2]))
		}
	}

	terminator := "\n"
	if matches.Has("zero") {
		terminator = "\x00"
	}

	for _, operand := range names {
		base := basenameValue(operand)
		if shouldStripBasenameSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
		}
		if _, err := fmt.Fprint(inv.Stdout, base, terminator); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func basenameValue(name string) string {
	if name == "" {
		return ""
	}
	cleaned := strings.TrimRight(name, "/")
	if cleaned == "" {
		return "/"
	}
	return path.Base(cleaned)
}

func shouldStripBasenameSuffix(base, suffix string) bool {
	return suffix != "" && base != suffix && strings.HasSuffix(base, suffix)
}

func quoteGNUOperand(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

const basenameHelpText = `Usage: basename NAME [SUFFIX]
  or:  basename OPTION... NAME...
Print NAME with any leading directory components removed.
`

const basenameVersionText = "basename (gbash) dev\n"

var _ Command = (*Basename)(nil)
var _ SpecProvider = (*Basename)(nil)
var _ ParsedRunner = (*Basename)(nil)
