package builtins

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
)

type Tac struct{}

type tacOptions struct {
	before    bool
	regex     bool
	separator []byte
}

func NewTac() *Tac {
	return &Tac{}
}

func (c *Tac) Name() string {
	return "tac"
}

func (c *Tac) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Tac) Spec() CommandSpec {
	return CommandSpec{
		Name:  "tac",
		About: "Write each FILE to standard output, last line first.",
		Usage: "tac [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "before", Short: 'b', Long: "before", Help: "attach the separator before instead of after"},
			{Name: "regex", Short: 'r', Long: "regex", Help: "interpret the separator as a regular expression"},
			{Name: "separator", Short: 's', Long: "separator", Arity: OptionRequiredValue, ValueName: "STRING", Help: "use STRING as the separator instead of newline"},
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

func (c *Tac) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts := parseTacMatches(matches)

	inputs, err := readNamedInputs(ctx, inv, matches.Args("file"))
	if err != nil {
		return err
	}

	var compiled *regexp.Regexp
	if opts.regex {
		compiled, err = regexp.Compile(translateTacRegexFlavor(opts.separator))
		if err != nil {
			return exitf(inv, 1, "tac: invalid regular expression")
		}
	}

	for _, input := range inputs {
		var output []byte
		if compiled != nil {
			output = tacReverseRegex(input.Data, compiled, opts.before)
		} else {
			output = tacReverseFixed(input.Data, opts.separator, opts.before)
		}
		if _, err := inv.Stdout.Write(output); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func parseTacMatches(matches *ParsedCommand) tacOptions {
	opts := tacOptions{
		before:    matches.Has("before"),
		regex:     matches.Has("regex"),
		separator: []byte("\n"),
	}
	if matches.Has("separator") {
		value := matches.Value("separator")
		if value == "" {
			opts.separator = []byte{0}
		} else {
			opts.separator = []byte(value)
		}
	}
	return opts
}

func tacReverseFixed(data, separator []byte, before bool) []byte {
	if len(separator) == 0 {
		separator = []byte{0}
	}
	var out bytes.Buffer
	slen := len(separator)
	followingLineStart := len(data)
	for i := bytes.LastIndex(data, separator); i >= 0; i = bytes.LastIndex(data[:i], separator) {
		if before {
			_, _ = out.Write(data[i:followingLineStart])
			followingLineStart = i
		} else {
			_, _ = out.Write(data[i+slen : followingLineStart])
			followingLineStart = i + slen
		}
	}
	_, _ = out.Write(data[:followingLineStart])
	return out.Bytes()
}

func tacReverseRegex(data []byte, pattern *regexp.Regexp, before bool) []byte {
	matches := pattern.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return append([]byte(nil), data...)
	}
	var out bytes.Buffer
	thisLineEnd := len(data)
	followingLineStart := len(data)
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		if match[1] > thisLineEnd {
			continue
		}
		thisLineEnd = match[0]
		if before {
			_, _ = out.Write(data[match[0]:followingLineStart])
			followingLineStart = match[0]
		} else {
			_, _ = out.Write(data[match[1]:followingLineStart])
			followingLineStart = match[1]
		}
	}
	_, _ = out.Write(data[:followingLineStart])
	return out.Bytes()
}

func translateTacRegexFlavor(bytesValue []byte) string {
	var result []byte
	insideBrackets := false
	prevWasBackslash := false
	var lastByte byte
	var hasLast bool

	for i := 0; i < len(bytesValue); i++ {
		b := bytesValue[i]
		isEscaped := prevWasBackslash
		prevWasBackslash = false

		switch {
		case insideBrackets && b > 0x7f:
			continue
		case b == '\\' && !insideBrackets && !isEscaped:
			if i+1 < len(bytesValue) {
				next := bytesValue[i+1]
				if next == '(' || next == ')' || next == '|' || next == '{' || next == '}' {
					result = append(result, next)
					lastByte, hasLast = next, true
					i++
					continue
				}
			}
			result = append(result, '\\')
			lastByte, hasLast = '\\', true
			prevWasBackslash = true
		case b == '[':
			insideBrackets = true
			result = append(result, b)
			lastByte, hasLast = b, true
		case b == ']':
			insideBrackets = false
			result = append(result, b)
			lastByte, hasLast = b, true
		case (b == '(' || b == ')' || b == '|' || b == '{' || b == '}') && !insideBrackets && !isEscaped:
			result = append(result, '\\', b)
			lastByte, hasLast = b, true
		case b == '^' && !insideBrackets && !isEscaped:
			if len(result) != 0 && (!hasLast || (lastByte != '(' && lastByte != '|')) {
				result = append(result, '\\')
			}
			result = append(result, b)
			lastByte, hasLast = b, true
		case b == '$' && !insideBrackets && !isEscaped:
			nextIsAnchor := i+1 >= len(bytesValue)
			if !nextIsAnchor {
				next := bytesValue[i+1]
				nextIsAnchor = next == ')' || next == '|'
				if next == '\\' && i+2 < len(bytesValue) {
					peek := bytesValue[i+2]
					nextIsAnchor = peek == ')' || peek == '|'
				}
			}
			if !nextIsAnchor {
				result = append(result, '\\')
			}
			result = append(result, b)
			lastByte, hasLast = b, true
		case b > 0x7f:
			result = fmt.Appendf(result, "(?-u:\\x%02x)", b)
			hasLast = false
		default:
			result = append(result, b)
			lastByte, hasLast = b, true
		}
	}

	return string(result)
}

var _ Command = (*Tac)(nil)
var _ SpecProvider = (*Tac)(nil)
var _ ParsedRunner = (*Tac)(nil)
