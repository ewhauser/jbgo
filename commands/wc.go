package commands

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

type WC struct{}

type wcOptions struct {
	lines bool
	words bool
	bytes bool
	chars bool
}

type wcCounts struct {
	lines int
	words int
	bytes int
	chars int
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
	opts, files, err := parseWCArgs(inv)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		return writeWCLine(inv, countWC(data), opts, "", wcOutputWidth(opts, nil, false))
	}

	results := make([]wcLineResult, 0, len(files))
	total := wcCounts{}
	exitCode := 0
	hasStdinInput := false
	for _, file := range files {
		var (
			data []byte
			err  error
		)
		if file == "-" {
			hasStdinInput = true
			data, err = readAllStdin(inv)
		} else {
			data, _, err = readAllFile(ctx, inv, file)
		}
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "wc: %s: No such file or directory\n", file)
			exitCode = 1
			continue
		}
		counts := countWC(data)
		total.lines += counts.lines
		total.words += counts.words
		total.bytes += counts.bytes
		total.chars += counts.chars
		results = append(results, wcLineResult{label: file, counts: counts})
	}

	width := wcOutputWidth(opts, results, hasStdinInput)
	for _, result := range results {
		if err := writeWCLine(inv, result.counts, opts, result.label, width); err != nil {
			return err
		}
	}
	if len(files) > 1 {
		if err := writeWCLine(inv, total, opts, "total", width); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseWCArgs(inv *Invocation) (wcOptions, []string, error) {
	args := inv.Args
	var opts wcOptions

	for len(args) > 0 {
		arg := args[0]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch arg {
		case "-l", "--lines":
			opts.lines = true
		case "-w", "--words":
			opts.words = true
		case "-c", "--bytes":
			opts.bytes = true
		case "-m", "--chars":
			opts.chars = true
		default:
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				for _, flag := range arg[1:] {
					switch flag {
					case 'l':
						opts.lines = true
					case 'w':
						opts.words = true
					case 'c':
						opts.bytes = true
					case 'm':
						opts.chars = true
					default:
						return wcOptions{}, nil, exitf(inv, 1, "wc: unsupported flag -%c", flag)
					}
				}
			} else {
				return wcOptions{}, nil, exitf(inv, 1, "wc: unsupported flag %s", arg)
			}
		}
		args = args[1:]
	}

	if !opts.lines && !opts.words && !opts.bytes && !opts.chars {
		opts.lines = true
		opts.words = true
		opts.bytes = true
	}

	return opts, args, nil
}

func countWC(data []byte) wcCounts {
	return wcCounts{
		lines: bytes.Count(data, []byte{'\n'}),
		words: len(bytes.Fields(data)),
		bytes: len(data),
		chars: utf8.RuneCount(data),
	}
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
	if len(results) > 1 {
		total := wcCounts{}
		for _, result := range results {
			total.lines += result.counts.lines
			total.words += result.counts.words
			total.bytes += result.counts.bytes
			total.chars += result.counts.chars
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
	values := make([]int, 0, 4)
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
	return values
}

func writeWCLine(inv *Invocation, counts wcCounts, opts wcOptions, label string, width int) error {
	parts := make([]string, 0, 4)
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

	line := strings.Join(parts, " ")
	if label != "" {
		line += " " + label
	}
	if _, err := fmt.Fprintln(inv.Stdout, line); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*WC)(nil)
