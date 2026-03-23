package builtins

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

const unexpandHelpText = `Usage: unexpand [OPTION]... [FILE]...
Convert blanks in each FILE to tabs, writing to standard output.

With no FILE, or when FILE is -, read standard input.

Mandatory arguments to long options are mandatory for short options too.
  -a, --all         convert all blanks, instead of just initial blanks
      --first-only  convert only leading sequences of blanks (overrides -a)
  -t, --tabs=N      have tabs N characters apart instead of 8 (enables -a)
  -t, --tabs=LIST   use comma separated list of tab positions.
                      The last specified position can be prefixed with '/'
                      to specify a tab size to use after the last
                      explicitly specified tab stop.  Also a prefix of '+'
                      can be used to align remaining tab stops relative to
                      the last specified tab stop instead of the first column
      --help        display this help and exit
      --version     output version information and exit
`

type unexpandCharType int

const (
	unexpandCharOther unexpandCharType = iota
	unexpandCharBackspace
	unexpandCharSpace
	unexpandCharTab
)

type unexpandOptions struct {
	all           bool
	files         []string
	tabstops      []int
	remainingMode expandRemainingMode
	lastcol       int
}

type unexpandPrintState struct {
	col     int
	scol    int
	leading bool
	prev    unexpandCharType
}

type Unexpand struct{}

func NewUnexpand() *Unexpand {
	return &Unexpand{}
}

func (c *Unexpand) Name() string {
	return "unexpand"
}

func (c *Unexpand) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Unexpand) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}

	normalized := make([]string, 0, len(inv.Args)+1)
	changed := false
	hasShortcut := false
	hasAll := false

	for i, arg := range inv.Args {
		if arg == "--" {
			if hasShortcut && !hasAll {
				normalized = append(normalized, "--first-only")
				changed = true
				hasShortcut = false
			}
			normalized = append(normalized, arg)
			normalized = append(normalized, inv.Args[i+1:]...)
			break
		}
		switch arg {
		case "-a", "--all":
			hasAll = true
		}
		if values, ok := expandTabsShortcut(arg); ok {
			normalized = append(normalized, values...)
			changed = true
			hasShortcut = true
			continue
		}
		normalized = append(normalized, arg)
	}

	if hasShortcut && !hasAll {
		normalized = append(normalized, "--first-only")
		changed = true
	}
	if !changed {
		return inv
	}

	clone := *inv
	clone.Args = normalized
	return &clone
}

func (c *Unexpand) Spec() CommandSpec {
	return CommandSpec{
		Name:  "unexpand",
		About: "Convert blanks to tabs.",
		Usage: "unexpand [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "all", Short: 'a', Long: "all", Help: "convert all blanks, instead of just initial blanks"},
			{Name: "first-only", Long: "first-only", Help: "convert only leading sequences of blanks (overrides -a)"},
			{Name: "tabs", Short: 't', Long: "tabs", Arity: OptionRequiredValue, ValueName: "N", Repeatable: true, Help: "have tabs N characters apart instead of 8 (enables -a)"},
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
		HelpRenderer: renderStaticHelp(unexpandHelpText),
	}
}

func (c *Unexpand) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	spec := c.Spec()
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	if matches.Has("version") {
		return RenderCommandVersion(inv.Stdout, &spec)
	}

	opts, err := parseUnexpandMatches(inv, matches)
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
			if _, writeErr := fmt.Fprintf(inv.Stderr, "unexpand: %s: %s\n", name, readAllErrorText(err)); writeErr != nil {
				return &ExitError{Code: 1, Err: writeErr}
			}
			continue
		}

		if _, err := inv.Stdout.Write(unexpandBytes(data, &opts)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if hadErrors {
		return &ExitError{Code: 1}
	}
	return nil
}

func parseUnexpandMatches(inv *Invocation, matches *ParsedCommand) (unexpandOptions, error) {
	mode, tabstops, err := parseUnexpandTabList(strings.Join(matches.Values("tabs"), ","))
	if err != nil {
		return unexpandOptions{}, exitf(inv, 1, "unexpand: %s", err)
	}
	return unexpandOptions{
		all:           (matches.Has("all") || matches.Count("tabs") > 0) && !matches.Has("first-only"),
		files:         matches.Args("file"),
		tabstops:      tabstops,
		remainingMode: mode,
		lastcol:       unexpandLastCol(tabstops, mode),
	}, nil
}

func unexpandLastCol(tabstops []int, mode expandRemainingMode) int {
	if mode == expandRemainingNone && len(tabstops) > 1 {
		return tabstops[len(tabstops)-1]
	}
	return 0
}

func parseUnexpandTabList(raw string) (expandRemainingMode, []int, error) {
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
			remainingMode = wordMode
			word = word[1:]
			if word == "" {
				continue
			}
			if idx != len(words)-1 {
				return expandRemainingNone, nil, fmt.Errorf("%s specifier only allowed with the last value", expandSpecifierQuote(wordMode))
			}
		}

		num, err := parseExpandTabNumber(word)
		if err != nil {
			return expandRemainingNone, nil, err
		}
		if num == 0 {
			switch {
			case wordMode == expandRemainingNone:
				return expandRemainingNone, nil, fmt.Errorf("tab size cannot be 0")
			case len(tabstops) == 0:
				continue
			default:
				remainingMode = expandRemainingNone
				continue
			}
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

func unexpandBytes(data []byte, opts *unexpandOptions) []byte {
	if len(data) == 0 {
		return nil
	}

	var out bytes.Buffer
	lastcol := opts.lastcol
	if lastcol == 0 {
		lastcol = unexpandLastCol(opts.tabstops, opts.remainingMode)
	}
	state := unexpandPrintState{
		leading: true,
		prev:    unexpandCharOther,
	}

	for _, b := range data {
		if lastcol > 0 && state.col >= lastcol {
			unexpandWriteTabs(&out, opts, &state)
			switch b {
			case ' ':
				state.col++
				_ = out.WriteByte(b)
				state.scol = state.col
				state.prev = unexpandCharSpace
			case '\t':
				advance, ok := unexpandNextTabstop(opts.tabstops, state.col, opts.remainingMode)
				if !ok {
					advance = 1
				}
				state.col += advance
				_ = out.WriteByte(b)
				state.scol = state.col
				state.prev = unexpandCharTab
			case '\b':
				if state.col > 0 {
					state.col--
				}
				_ = out.WriteByte(b)
				state.scol = state.col
				state.leading = false
				state.prev = unexpandCharBackspace
			case '\n':
				_ = out.WriteByte(b)
				state = unexpandPrintState{
					leading: true,
					prev:    unexpandCharOther,
				}
			default:
				state.leading = false
				state.col++
				_ = out.WriteByte(b)
				state.scol = state.col
				state.prev = unexpandCharOther
			}
			continue
		}

		switch b {
		case ' ':
			state.col++
			if !state.leading && !opts.all {
				_ = out.WriteByte(b)
				state.scol = state.col
			}
			state.prev = unexpandCharSpace
		case '\t':
			advance, ok := unexpandNextTabstop(opts.tabstops, state.col, opts.remainingMode)
			if !ok {
				advance = 1
			}
			state.col += advance
			if !state.leading && !opts.all {
				_ = out.WriteByte(b)
				state.scol = state.col
			}
			state.prev = unexpandCharTab
		case '\b':
			unexpandWriteTabs(&out, opts, &state)
			state.leading = false
			if state.col > 0 {
				state.col--
			}
			_ = out.WriteByte(b)
			state.scol = state.col
			state.prev = unexpandCharBackspace
		case '\n':
			unexpandWriteTabs(&out, opts, &state)
			_ = out.WriteByte(b)
			state = unexpandPrintState{
				leading: true,
				prev:    unexpandCharOther,
			}
		default:
			unexpandWriteTabs(&out, opts, &state)
			state.leading = false
			state.col++
			_ = out.WriteByte(b)
			state.scol = state.col
			state.prev = unexpandCharOther
		}
	}

	unexpandWriteTabs(&out, opts, &state)
	return out.Bytes()
}

func unexpandWriteTabs(out *bytes.Buffer, opts *unexpandOptions, state *unexpandPrintState) {
	ai := state.leading || opts.all
	if (ai && state.prev != unexpandCharTab && state.col > state.scol+1) ||
		(state.col > state.scol && (state.leading || (ai && state.prev == unexpandCharTab))) {
		for {
			next, ok := unexpandNextTabstop(opts.tabstops, state.scol, opts.remainingMode)
			if !ok || state.col < state.scol+next {
				break
			}
			_ = out.WriteByte('\t')
			state.scol += next
		}
	}
	for state.col > state.scol {
		_ = out.WriteByte(' ')
		state.scol++
	}
}

func unexpandNextTabstop(tabstops []int, col int, mode expandRemainingMode) (int, bool) {
	if len(tabstops) == 0 {
		return 0, false
	}
	if len(tabstops) == 1 {
		step := tabstops[0]
		if step <= 0 {
			return 0, false
		}
		return step - col%step, true
	}

	switch mode {
	case expandRemainingPlus:
		for _, stop := range tabstops[:len(tabstops)-1] {
			if stop > col {
				return stop - col, true
			}
		}
		step := tabstops[len(tabstops)-1]
		lastFixed := tabstops[len(tabstops)-2]
		if step <= 0 || col < lastFixed {
			return 0, false
		}
		sinceLast := col - lastFixed
		remainder := sinceLast % step
		if remainder == 0 {
			return step, true
		}
		return step - remainder, true
	case expandRemainingSlash:
		for _, stop := range tabstops[:len(tabstops)-1] {
			if stop > col {
				return stop - col, true
			}
		}
		step := tabstops[len(tabstops)-1]
		if step <= 0 {
			return 0, false
		}
		return step - col%step, true
	default:
		for _, stop := range tabstops {
			if stop > col {
				return stop - col, true
			}
		}
		return 0, false
	}
}

var _ Command = (*Unexpand)(nil)
var _ SpecProvider = (*Unexpand)(nil)
var _ ParsedRunner = (*Unexpand)(nil)
var _ ParseInvocationNormalizer = (*Unexpand)(nil)
