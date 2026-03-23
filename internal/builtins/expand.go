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
	expandMaxCachedSpace = 4096
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
	spec := c.Spec()
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	if matches.Has("version") {
		return RenderCommandVersion(inv.Stdout, &spec)
	}

	opts, err := parseExpandMatches(inv, matches)
	if err != nil {
		return err
	}

	files := opts.files
	if len(files) == 0 {
		files = []string{"-"}
	}

	var hadErrors bool

	for _, name := range files {
		data, err := func() ([]byte, error) {
			if name == "-" {
				return readAllStdin(ctx, inv)
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

		if _, err := inv.Stdout.Write(expandBytes(data, &opts)); err != nil {
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
		spaceCache:    expandSpaceCache(tabstops, mode),
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
	)
	words := strings.FieldsFunc(trimmed, expandTabSeparator)
	for idx, word := range words {
		wordMode := expandRemainingNone
		if word != "" {
			switch word[0] {
			case '+':
				wordMode = expandRemainingPlus
			case '/':
				wordMode = expandRemainingSlash
			}
		}
		if wordMode != expandRemainingNone {
			if idx != len(words)-1 {
				return expandRemainingNone, nil, fmt.Errorf("%s specifier only allowed with the last value", expandSpecifierQuote(wordMode))
			}
			remainingMode = wordMode
			word = word[1:]
			if word == "" {
				continue
			}
		}

		num, err := parseExpandTabNumber(word)
		if err != nil {
			return expandRemainingNone, nil, err
		}
		if num == 0 {
			return expandRemainingNone, nil, fmt.Errorf("tab size cannot be 0")
		}
		if wordMode == expandRemainingNone && len(tabstops) > 0 && tabstops[len(tabstops)-1] >= num {
			return expandRemainingNone, nil, fmt.Errorf("tab sizes must be ascending")
		}
		tabstops = append(tabstops, num)
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
	if strings.Contains(err.Error(), "value out of range") || (errors.As(err, &rangeErr) && errors.Is(err, strconv.ErrRange)) {
		return 0, fmt.Errorf("tab stop is too large %s", quoteGNUOperand(raw))
	}
	return 0, fmt.Errorf("tab size contains invalid character(s): %s", quoteGNUOperand(digitsTrimmed))
}

func expandTabSeparator(r rune) bool {
	return r == ' ' || r == '\t' || r == ','
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

func expandSpaceCache(tabstops []int, mode expandRemainingMode) []byte {
	return bytes.Repeat([]byte(" "), min(expandMaxSpaceRun(tabstops, mode), expandMaxCachedSpace))
}

func expandBytes(data []byte, opts *expandOptions) []byte {
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
				expandWriteSpaces(&out, opts.spaceCache, spaces)
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

func expandWriteSpaces(out *bytes.Buffer, cache []byte, n int) {
	for n > 0 {
		chunk := min(len(cache), n)
		_, _ = out.Write(cache[:chunk])
		n -= chunk
	}
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
