package builtins

import (
	"context"
	"fmt"
	"strconv"
)

type Strings struct{}

type stringsOptions struct {
	minLength    int
	encoding     string
	offsetFormat string
}

func NewStrings() *Strings {
	return &Strings{}
}

func (c *Strings) Name() string {
	return "strings"
}

func (c *Strings) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Strings) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}
	args := append([]string(nil), inv.Args...)
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if len(arg) > 1 && arg[0] == '-' && isDecimalDigits(arg[1:]) {
			args[i] = "-n" + arg[1:]
		}
	}
	clone := *inv
	clone.Args = args
	return &clone
}

func (c *Strings) Spec() CommandSpec {
	return CommandSpec{
		Name:  "strings",
		About: "Print the sequences of printable characters in files.\nWith no FILE, or when FILE is -, read standard input.",
		Usage: "strings [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "min-length", Short: 'n', Arity: OptionRequiredValue, ValueName: "MIN", Help: "print sequences of at least MIN characters (default: 4)"},
			{Name: "radix", Short: 't', Arity: OptionRequiredValue, ValueName: "FORMAT", Help: "print offset before each string (o=octal, x=hex, d=decimal)"},
			{Name: "all", Short: 'a', Long: "all", Help: "scan the entire file (default behavior)"},
			{Name: "encoding", Short: 'e', Arity: OptionRequiredValue, ValueName: "ENCODING", Help: "select character encoding (s=7-bit, S=8-bit)"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Strings) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseStringsMatches(inv, matches)
	if err != nil {
		return err
	}

	inputs, err := readNamedInputs(ctx, inv, matches.Args("file"))
	if err != nil {
		return err
	}

	for _, input := range inputs {
		lines := extractStrings(input.Data, opts)
		for _, line := range lines {
			if _, err := fmt.Fprintln(inv.Stdout, line); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}
	return nil
}

func parseStringsMatches(inv *Invocation, matches *ParsedCommand) (stringsOptions, error) {
	opts := stringsOptions{
		minLength: 4,
		encoding:  "s",
	}
	if matches.Has("min-length") {
		minLength, err := strconv.Atoi(matches.Value("min-length"))
		if err != nil || minLength < 1 {
			return stringsOptions{}, exitf(inv, 1, "strings: invalid minimum string length: %q", matches.Value("min-length"))
		}
		opts.minLength = minLength
	}
	if matches.Has("radix") {
		switch matches.Value("radix") {
		case "o", "x", "d":
			opts.offsetFormat = matches.Value("radix")
		default:
			return stringsOptions{}, exitf(inv, 1, "strings: invalid radix: %q", matches.Value("radix"))
		}
	}
	if matches.Has("encoding") {
		switch matches.Value("encoding") {
		case "s", "S":
			opts.encoding = matches.Value("encoding")
		default:
			return stringsOptions{}, exitf(inv, 1, "strings: invalid encoding: %q", matches.Value("encoding"))
		}
	}
	return opts, nil
}

func extractStrings(data []byte, opts stringsOptions) []string {
	results := make([]string, 0)
	current := make([]byte, 0, opts.minLength)
	start := 0

	flush := func() {
		if len(current) < opts.minLength {
			current = current[:0]
			return
		}
		results = append(results, formatStringsOffset(start, opts.offsetFormat)+string(current))
		current = current[:0]
	}

	for i, b := range data {
		if isStringsPrintableByte(b, opts.encoding) {
			if len(current) == 0 {
				start = i
			}
			current = append(current, b)
			continue
		}
		flush()
	}
	flush()
	return results
}

func isStringsPrintableByte(b byte, encoding string) bool {
	switch encoding {
	case "S":
		return b == '\t' || (b >= 32 && b != 127)
	default:
		return b == '\t' || (b >= 32 && b <= 126)
	}
}

func formatStringsOffset(offset int, format string) string {
	switch format {
	case "o":
		return fmt.Sprintf("%7o ", offset)
	case "x":
		return fmt.Sprintf("%7x ", offset)
	case "d":
		return fmt.Sprintf("%7d ", offset)
	default:
		return ""
	}
}

var _ Command = (*Strings)(nil)
var _ SpecProvider = (*Strings)(nil)
var _ ParsedRunner = (*Strings)(nil)
var _ ParseInvocationNormalizer = (*Strings)(nil)
