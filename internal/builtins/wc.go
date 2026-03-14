package builtins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

type WC struct{}

type wcTotalWhen int

const (
	wcTotalAuto wcTotalWhen = iota
	wcTotalAlways
	wcTotalOnly
	wcTotalNever
)

type wcOptions struct {
	lines         bool
	words         bool
	bytes         bool
	chars         bool
	maxLineLength bool
	debug         bool
	files0From    string
	totalWhen     wcTotalWhen
}

type wcCounts struct {
	lines         int
	words         int
	bytes         int
	chars         int
	maxLineLength int
}

type wcLineResult struct {
	label  string
	counts wcCounts
}

const wcMinimumWidth = 7

func NewWC() *WC {
	return &WC{}
}

func (c *WC) Name() string {
	return "wc"
}

func (c *WC) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *WC) Spec() CommandSpec {
	return CommandSpec{
		Name:  "wc",
		About: "Print newline, word, and byte counts for each FILE, and a total line if more than one FILE is specified.",
		Usage: "wc [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "bytes", Short: 'c', Long: "bytes", Help: "print the byte counts"},
			{Name: "chars", Short: 'm', Long: "chars", Help: "print the character counts"},
			{Name: "files0-from", Long: "files0-from", Arity: OptionRequiredValue, ValueName: "F", Help: "read input from the files specified by NUL-terminated names in file F; if F is - then read names from standard input"},
			{Name: "lines", Short: 'l', Long: "lines", Help: "print the newline counts"},
			{Name: "max-line-length", Short: 'L', Long: "max-line-length", Help: "print the maximum display width"},
			{Name: "total", Long: "total", Arity: OptionRequiredValue, ValueName: "WHEN", Help: "when to print a line with total counts: auto, always, only, never"},
			{Name: "words", Short: 'w', Long: "words", Help: "print the word counts"},
			{Name: "debug", Long: "debug", Hidden: true, Help: "accepted for compatibility"},
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

func (c *WC) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseWCMatches(inv, matches)
	if err != nil {
		return err
	}

	files, exitCode, err := wcResolveInputs(ctx, inv, &opts, matches.Args("file"))
	if err != nil {
		return err
	}

	if len(files) == 0 {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return err
		}
		return writeWCLine(inv, countWC(data, inv), opts, "", wcOutputWidth(opts, nil, false))
	}

	results := make([]wcLineResult, 0, len(files))
	total := wcCounts{}
	hasStdinInput := false
	for _, file := range files {
		var (
			data []byte
			err  error
		)
		if file == "-" {
			hasStdinInput = true
			data, err = readAllStdin(ctx, inv)
		} else {
			data, _, err = readAllFile(ctx, inv, file)
		}
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "wc: %s: No such file or directory\n", file)
			exitCode = 1
			continue
		}
		counts := countWC(data, inv)
		total.lines += counts.lines
		total.words += counts.words
		total.bytes += counts.bytes
		total.chars += counts.chars
		if counts.maxLineLength > total.maxLineLength {
			total.maxLineLength = counts.maxLineLength
		}
		results = append(results, wcLineResult{label: file, counts: counts})
	}

	width := wcOutputWidth(opts, results, hasStdinInput)
	if opts.totalWhen != wcTotalOnly {
		for _, result := range results {
			if err := writeWCLine(inv, result.counts, opts, result.label, width); err != nil {
				return err
			}
		}
	}
	if wcTotalVisible(opts.totalWhen, len(files)) {
		if err := writeWCLine(inv, total, opts, "total", width); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseWCMatches(inv *Invocation, matches *ParsedCommand) (wcOptions, error) {
	opts := wcOptions{
		lines:         matches.Has("lines"),
		words:         matches.Has("words"),
		bytes:         matches.Has("bytes"),
		chars:         matches.Has("chars"),
		maxLineLength: matches.Has("max-line-length"),
		debug:         matches.Has("debug"),
		files0From:    matches.Value("files0-from"),
		totalWhen:     wcTotalAuto,
	}
	if !opts.lines && !opts.words && !opts.bytes && !opts.chars && !opts.maxLineLength {
		opts.lines = true
		opts.words = true
		opts.bytes = true
	}
	if matches.Has("total") {
		switch matches.Value("total") {
		case "auto":
			opts.totalWhen = wcTotalAuto
		case "always":
			opts.totalWhen = wcTotalAlways
		case "only":
			opts.totalWhen = wcTotalOnly
		case "never":
			opts.totalWhen = wcTotalNever
		default:
			return wcOptions{}, exitf(inv, 1, "wc: invalid argument %q for '--total'", matches.Value("total"))
		}
	}
	return opts, nil
}

func wcResolveInputs(ctx context.Context, inv *Invocation, opts *wcOptions, files []string) (resolved []string, exitCode int, err error) {
	if opts.files0From == "" {
		return files, 0, nil
	}
	if len(files) > 0 {
		return nil, 0, exitf(inv, 1, "wc: extra operand %s\nfile operands cannot be combined with --files0-from\nTry 'wc --help' for more information.", quoteGNUOperand(files[0]))
	}

	var (
		data    []byte
		readErr error
	)
	source := opts.files0From
	if source == "-" {
		data, readErr = readAllStdin(ctx, inv)
		if readErr != nil {
			return nil, 0, readErr
		}
	} else {
		data, _, readErr = readAllFile(ctx, inv, source)
		if readErr != nil {
			return nil, 0, exitf(inv, 1, "wc: cannot open %s for reading: No such file or directory", quoteGNUOperand(source))
		}
	}
	return parseWCFiles0From(inv, source, data), 0, nil
}

func parseWCFiles0From(inv *Invocation, source string, data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	files := make([]string, 0, len(parts))
	for i, part := range parts {
		if len(part) == 0 {
			_, _ = fmt.Fprintf(inv.Stderr, "wc: %s:%d: invalid zero-length file name\n", source, i+1)
			continue
		}
		name := string(part)
		if source == "-" && name == "-" {
			_, _ = fmt.Fprintln(inv.Stderr, "wc: when reading file names from standard input, no file name of '-' allowed")
			continue
		}
		files = append(files, name)
	}
	return files
}

func countWC(data []byte, inv *Invocation) wcCounts {
	return wcCounts{
		lines:         bytes.Count(data, []byte{'\n'}),
		words:         wcCountWords(data, inv),
		bytes:         len(data),
		chars:         utf8.RuneCount(data),
		maxLineLength: wcMaxLineLength(data),
	}
}

func wcCountWords(data []byte, inv *Invocation) int {
	posix := inv != nil && inv.Env["POSIXLY_CORRECT"] != ""
	count := 0
	inWord := false
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if !inWord {
				count++
				inWord = true
			}
			data = data[1:]
			continue
		}
		isSpace := false
		if posix {
			isSpace = r == ' ' || (r >= '\t' && r <= '\r')
		} else {
			isSpace = unicode.IsSpace(r)
		}
		if isSpace {
			inWord = false
		} else if !inWord {
			count++
			inWord = true
		}
		data = data[size:]
	}
	return count
}

func wcMaxLineLength(data []byte) int {
	maxWidth := 0
	current := 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			current++
			data = data[1:]
			if current > maxWidth {
				maxWidth = current
			}
			continue
		}
		switch r {
		case '\n', '\r', '\f':
			if current > maxWidth {
				maxWidth = current
			}
			current = 0
		case '\t':
			current -= current % 8
			current += 8
		default:
			if !unicode.IsControl(r) {
				current++
			}
		}
		if current > maxWidth {
			maxWidth = current
		}
		data = data[size:]
	}
	return maxWidth
}

func wcOutputWidth(opts wcOptions, results []wcLineResult, hasStdinInput bool) int {
	enabled := wcEnabledCount(opts)
	if len(results) == 0 {
		if enabled == 1 {
			return 1
		}
		return wcMinimumWidth
	}
	if enabled == 1 && len(results) == 1 {
		return 1
	}

	width := 1
	if hasStdinInput {
		width = wcMinimumWidth
	}
	for _, result := range results {
		width = max(width, wcCountsWidth(result.counts, opts))
	}
	if wcTotalVisible(opts.totalWhen, len(results)) {
		total := wcCounts{}
		for _, result := range results {
			total.lines += result.counts.lines
			total.words += result.counts.words
			total.bytes += result.counts.bytes
			total.chars += result.counts.chars
			if result.counts.maxLineLength > total.maxLineLength {
				total.maxLineLength = result.counts.maxLineLength
			}
		}
		width = max(width, wcCountsWidth(total, opts))
	}
	return width
}

func wcEnabledCount(opts wcOptions) int {
	count := 0
	if opts.lines {
		count++
	}
	if opts.words {
		count++
	}
	if opts.bytes {
		count++
	}
	if opts.chars {
		count++
	}
	if opts.maxLineLength {
		count++
	}
	return count
}

func wcCountsWidth(counts wcCounts, opts wcOptions) int {
	width := 1
	for _, value := range wcSelectedCounts(counts, opts) {
		width = max(width, len(fmt.Sprintf("%d", value)))
	}
	return width
}

func wcSelectedCounts(counts wcCounts, opts wcOptions) []int {
	values := make([]int, 0, 5)
	if opts.lines {
		values = append(values, counts.lines)
	}
	if opts.words {
		values = append(values, counts.words)
	}
	if opts.bytes {
		values = append(values, counts.bytes)
	}
	if opts.chars {
		values = append(values, counts.chars)
	}
	if opts.maxLineLength {
		values = append(values, counts.maxLineLength)
	}
	return values
}

func wcTotalVisible(totalWhen wcTotalWhen, numInputs int) bool {
	switch totalWhen {
	case wcTotalAlways, wcTotalOnly:
		return true
	case wcTotalNever:
		return false
	default:
		return numInputs > 1
	}
}

func writeWCLine(inv *Invocation, counts wcCounts, opts wcOptions, label string, width int) error {
	parts := make([]string, 0, 5)
	if opts.lines {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.lines))
	}
	if opts.words {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.words))
	}
	if opts.bytes {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.bytes))
	}
	if opts.chars {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.chars))
	}
	if opts.maxLineLength {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.maxLineLength))
	}

	line := strings.Join(parts, " ")
	if label != "" {
		line += " " + label
	}
	if _, err := io.WriteString(inv.Stdout, line+"\n"); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*WC)(nil)
var _ SpecProvider = (*WC)(nil)
var _ ParsedRunner = (*WC)(nil)
