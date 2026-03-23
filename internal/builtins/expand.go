package builtins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	expandDefaultTabStop = 8
)

const expandHelpText = `Usage: expand [OPTION]... [FILE]...
Convert tabs in each FILE to spaces, writing to standard output.

With no FILE, or when FILE is -, read standard input.

Mandatory arguments to long options are mandatory for short options too.
  -i, --initial    do not convert tabs after non blanks
  -t, --tabs=N     have tabs N characters apart, not 8
  -t, --tabs=LIST  use comma separated list of tab positions.
                     The last specified position can be prefixed with '/'
                     to specify a tab size to use after the last
                     explicitly specified tab stop.  Also a prefix of '+'
                     can be used to align remaining tab stops relative to
                     the last specified tab stop instead of the first column
      --help       display this help and exit
      --version    output version information and exit
`

type expandRemainingMode int

const (
	expandRemainingNone expandRemainingMode = iota
	expandRemainingSlash
	expandRemainingPlus
)

type expandOptions struct {
	initial       bool
	files         []string
	tabstops      []int
	remainingMode expandRemainingMode
	spaceCache    []byte
}

type Expand struct{}

func NewExpand() *Expand {
	return &Expand{}
}

func (c *Expand) Name() string {
	return "expand"
}

func (c *Expand) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Expand) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}

	normalized := make([]string, 0, len(inv.Args))
	changed := false
	for i, arg := range inv.Args {
		if arg == "--" {
			normalized = append(normalized, arg)
			normalized = append(normalized, inv.Args[i+1:]...)
			break
		}
		if values, ok := expandTabsShortcut(arg); ok {
			normalized = append(normalized, values...)
			changed = true
			continue
		}
		normalized = append(normalized, arg)
	}
	if !changed {
		return inv
	}

	clone := *inv
	clone.Args = normalized
	return &clone
}

func (c *Expand) Spec() CommandSpec {
	return CommandSpec{
		Name:  "expand",
		About: "Convert tabs to spaces.",
		Usage: "expand [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "initial", Short: 'i', Long: "initial", Help: "do not convert tabs after non blanks"},
			{Name: "tabs", Short: 't', Long: "tabs", Arity: OptionRequiredValue, ValueName: "N", Repeatable: true, Help: "have tabs N characters apart, not 8"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
		},
		HelpRenderer: renderStaticHelp(expandHelpText),
	}
}

func (c *Expand) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return renderStaticHelp(expandHelpText)(inv.Stdout, c.Spec())
	}
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}

	opts, err := parseExpandMatches(inv, matches)
	if err != nil {
		return err
	}

	files := opts.files
	if len(files) == 0 {
		files = []string{"-"}
	}

	var (
		stdinData   []byte
		stdinLoaded bool
		hadErrors   bool
	)

	for _, name := range files {
		data, err := func() ([]byte, error) {
			if name == "-" {
				if !stdinLoaded {
					stdinData, err = readAllStdin(ctx, inv)
					if err != nil {
						return nil, err
					}
					stdinLoaded = true
				}
				return stdinData, nil
			}
			read, _, err := readAllFile(ctx, inv, name)
			if err != nil {
				return nil, err
			}
			return read, nil
		}()
		if err != nil {
			hadErrors = true
			if _, writeErr := fmt.Fprintf(inv.Stderr, "expand: %s: %s\n", name, readAllErrorText(err)); writeErr != nil {
				return &ExitError{Code: 1, Err: writeErr}
			}
			continue
		}

		if _, err := inv.Stdout.Write(expandBytes(data, opts)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if hadErrors {
		return &ExitError{Code: 1}
	}
	return nil
}

func parseExpandMatches(inv *Invocation, matches *ParsedCommand) (expandOptions, error) {
	mode, tabstops, err := parseExpandTabList(strings.Join(matches.Values("tabs"), ","))
	if err != nil {
		return expandOptions{}, exitf(inv, 1, "expand: %s", err)
	}
	return expandOptions{
		initial:       matches.Has("initial"),
		files:         matches.Args("file"),
		tabstops:      tabstops,
		remainingMode: mode,
		spaceCache:    bytes.Repeat([]byte(" "), expandMaxSpaceRun(tabstops, mode)),
	}, nil
}

func expandTabsShortcut(arg string) ([]string, bool) {
	if len(arg) < 2 || arg[0] != '-' || arg == "--" {
		return nil, false
	}
	for _, ch := range arg[1:] {
		if (ch < '0' || ch > '9') && ch != ',' {
			return nil, false
		}
	}

	parts := strings.Split(arg[1:], ",")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		normalized = append(normalized, "--tabs="+part)
	}
	if len(normalized) == 0 {
		return nil, false
	}
	return normalized, true
}

func parseExpandTabList(raw string) (expandRemainingMode, []int, error) {
	trimmed := strings.TrimLeftFunc(raw, expandTabSeparator)
	if trimmed == "" {
		return expandRemainingNone, []int{expandDefaultTabStop}, nil
	}

	var (
		tabstops      []int
		remainingMode expandRemainingMode
		specifierUsed bool
	)
	for _, word := range strings.FieldsFunc(trimmed, expandTabSeparator) {
		bytesWord := []byte(word)
		for i := 0; i < len(bytesWord); i++ {
			switch bytesWord[i] {
			case '+':
				remainingMode = expandRemainingPlus
			case '/':
				remainingMode = expandRemainingSlash
			default:
				value := word[i:]
				num, err := parseExpandTabNumber(value)
				if err != nil {
					return expandRemainingNone, nil, err
				}
				if num == 0 {
					return expandRemainingNone, nil, fmt.Errorf("tab size cannot be 0")
				}
				if len(tabstops) > 0 && tabstops[len(tabstops)-1] >= num {
					return expandRemainingNone, nil, fmt.Errorf("tab sizes must be ascending")
				}
				if specifierUsed {
					return expandRemainingNone, nil, fmt.Errorf("%s specifier only allowed with the last value", expandSpecifierQuote(remainingMode))
				}
				if remainingMode != expandRemainingNone {
					specifierUsed = true
				}
				tabstops = append(tabstops, num)
				i = len(bytesWord)
			}
		}
	}

	if len(tabstops) == 0 {
		return expandRemainingNone, []int{expandDefaultTabStop}, nil
	}
	if len(tabstops) < 2 {
		remainingMode = expandRemainingNone
	}
	return remainingMode, tabstops, nil
}

func parseExpandTabNumber(raw string) (int, error) {
	value, err := strconv.ParseUint(raw, 10, strconv.IntSize)
	if err == nil {
		return int(value), nil
	}

	digitsTrimmed := strings.TrimLeft(raw, "0123456789")
	if digitsTrimmed != "" && (digitsTrimmed[0] == '/' || digitsTrimmed[0] == '+') {
		specifier := digitsTrimmed[:1]
		return 0, fmt.Errorf("%s specifier not at start of number: %s", quoteGNUOperand(specifier), quoteGNUOperand(digitsTrimmed))
	}

	var rangeErr *strconv.NumError
	if strings.Contains(err.Error(), "value out of range") || (errors.As(err, &rangeErr) && rangeErr.Err == strconv.ErrRange) {
		return 0, fmt.Errorf("tab stop is too large %s", quoteGNUOperand(raw))
	}
	return 0, fmt.Errorf("tab size contains invalid character(s): %s", quoteGNUOperand(digitsTrimmed))
}

func expandTabSeparator(r rune) bool {
	return r == ' ' || r == ','
}

func expandSpecifierQuote(mode expandRemainingMode) string {
	switch mode {
	case expandRemainingPlus:
		return quoteGNUOperand("+")
	case expandRemainingSlash:
		return quoteGNUOperand("/")
	default:
		return quoteGNUOperand("?")
	}
}

func expandMaxSpaceRun(tabstops []int, mode expandRemainingMode) int {
	maxGap := 1
	prev := 0
	for _, stop := range tabstops {
		if gap := stop - prev; gap > maxGap {
			maxGap = gap
		}
		prev = stop
	}

	switch mode {
	case expandRemainingPlus, expandRemainingSlash:
		if n := tabstops[len(tabstops)-1]; n > maxGap {
			maxGap = n
		}
	case expandRemainingNone:
		if len(tabstops) == 1 && tabstops[0] > maxGap {
			maxGap = tabstops[0]
		}
	}
	return maxGap
}

func expandBytes(data []byte, opts expandOptions) []byte {
	if len(data) == 0 {
		return nil
	}

	var out bytes.Buffer
	col := 0
	initial := true
	for _, b := range data {
		switch b {
		case '\t':
			spaces := expandNextTabstop(opts.tabstops, col, opts.remainingMode)
			col += spaces
			if initial || !opts.initial {
				_, _ = out.Write(expandSpaces(opts.spaceCache, spaces))
			} else {
				_ = out.WriteByte('\t')
			}
		case '\b':
			if col > 0 {
				col--
			}
			initial = false
			_ = out.WriteByte(b)
		default:
			col++
			if b != ' ' {
				initial = false
			}
			if b == '\n' {
				col = 0
				initial = true
			}
			_ = out.WriteByte(b)
		}
	}
	return out.Bytes()
}

func expandSpaces(cache []byte, n int) []byte {
	if n <= len(cache) {
		return cache[:n]
	}
	return bytes.Repeat([]byte(" "), n)
}

func expandNextTabstop(tabstops []int, col int, mode expandRemainingMode) int {
	switch mode {
	case expandRemainingPlus:
		for _, stop := range tabstops[:len(tabstops)-1] {
			if stop > col {
				return stop - col
			}
		}
		step := tabstops[len(tabstops)-1]
		lastFixed := tabstops[len(tabstops)-2]
		sinceLast := col - lastFixed
		stepsRequired := 1 + sinceLast/step
		return stepsRequired*step - sinceLast
	case expandRemainingSlash:
		for _, stop := range tabstops[:len(tabstops)-1] {
			if stop > col {
				return stop - col
			}
		}
		step := tabstops[len(tabstops)-1]
		return step - col%step
	default:
		if len(tabstops) == 1 {
			step := tabstops[0]
			return step - col%step
		}
		for _, stop := range tabstops {
			if stop > col {
				return stop - col
			}
		}
		return 1
	}
}

var _ Command = (*Expand)(nil)
var _ SpecProvider = (*Expand)(nil)
var _ ParsedRunner = (*Expand)(nil)
var _ ParseInvocationNormalizer = (*Expand)(nil)
