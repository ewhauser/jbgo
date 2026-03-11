package commands

import (
	"context"
	"fmt"
	"strings"
)

type Cut struct{}

type cutOptions struct {
	delimiter       string
	fields          []cutRange
	characters      []cutRange
	suppressNoDelim bool
}

type cutRange struct {
	start int
	end   int
}

func NewCut() *Cut {
	return &Cut{}
}

func (c *Cut) Name() string {
	return "cut"
}

func (c *Cut) Run(ctx context.Context, inv *Invocation) error {
	opts, files, err := parseCutArgs(inv)
	if err != nil {
		return err
	}

	exitCode := 0
	if len(files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		if err := writeCutOutput(inv, textLines(data), opts); err != nil {
			return err
		}
		return nil
	}

	for _, file := range files {
		data, _, err := readAllFile(ctx, inv, file)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "cut: %s: No such file or directory\n", file)
			exitCode = 1
			continue
		}
		if err := writeCutOutput(inv, textLines(data), opts); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseCutArgs(inv *Invocation) (cutOptions, []string, error) {
	args := inv.Args
	opts := cutOptions{delimiter: "\t"}

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}

		switch {
		case arg == "-d":
			if len(args) < 2 {
				return cutOptions{}, nil, exitf(inv, 1, "cut: option requires an argument -- 'd'")
			}
			opts.delimiter = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-d") && len(arg) > 2:
			opts.delimiter = arg[2:]
		case arg == "-f":
			if len(args) < 2 {
				return cutOptions{}, nil, exitf(inv, 1, "cut: option requires an argument -- 'f'")
			}
			parsed, err := parseCutList(args[1])
			if err != nil {
				return cutOptions{}, nil, exitf(inv, 1, "cut: invalid field list: %s", args[1])
			}
			opts.fields = parsed
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-f") && len(arg) > 2:
			parsed, err := parseCutList(arg[2:])
			if err != nil {
				return cutOptions{}, nil, exitf(inv, 1, "cut: invalid field list: %s", arg[2:])
			}
			opts.fields = parsed
		case arg == "-c":
			if len(args) < 2 {
				return cutOptions{}, nil, exitf(inv, 1, "cut: option requires an argument -- 'c'")
			}
			parsed, err := parseCutList(args[1])
			if err != nil {
				return cutOptions{}, nil, exitf(inv, 1, "cut: invalid character list: %s", args[1])
			}
			opts.characters = parsed
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			parsed, err := parseCutList(arg[2:])
			if err != nil {
				return cutOptions{}, nil, exitf(inv, 1, "cut: invalid character list: %s", arg[2:])
			}
			opts.characters = parsed
		case arg == "-s":
			opts.suppressNoDelim = true
		case arg == "--only-delimited":
			opts.suppressNoDelim = true
		default:
			return cutOptions{}, nil, exitf(inv, 1, "cut: unsupported flag %s", arg)
		}
		args = args[1:]
	}

	switch {
	case len(opts.fields) == 0 && len(opts.characters) == 0:
		return cutOptions{}, nil, exitf(inv, 1, "cut: you must specify a list of bytes, characters, or fields")
	case len(opts.fields) > 0 && len(opts.characters) > 0:
		return cutOptions{}, nil, exitf(inv, 1, "cut: only one of -c or -f may be specified")
	}

	return opts, args, nil
}

func parseCutList(value string) ([]cutRange, error) {
	parts := strings.Split(value, ",")
	ranges := make([]cutRange, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty list element")
		}
		if !strings.Contains(part, "-") {
			index, err := parsePositiveInt(part)
			if err != nil {
				return nil, err
			}
			ranges = append(ranges, cutRange{start: index, end: index})
			continue
		}

		startText, endText, _ := strings.Cut(part, "-")
		var current cutRange
		if startText != "" {
			index, err := parsePositiveInt(startText)
			if err != nil {
				return nil, err
			}
			current.start = index
		}
		if endText != "" {
			index, err := parsePositiveInt(endText)
			if err != nil {
				return nil, err
			}
			current.end = index
		}
		if current.start == 0 && current.end == 0 {
			return nil, fmt.Errorf("empty range")
		}
		if current.start != 0 && current.end != 0 && current.end < current.start {
			return nil, fmt.Errorf("descending range")
		}
		ranges = append(ranges, current)
	}
	return ranges, nil
}

func parsePositiveInt(value string) (int, error) {
	index := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid number")
		}
		index = (index * 10) + int(ch-'0')
	}
	if index <= 0 {
		return 0, fmt.Errorf("invalid number")
	}
	return index, nil
}

func writeCutOutput(inv *Invocation, lines []string, opts cutOptions) error {
	for _, line := range lines {
		value, ok := cutLine(line, opts)
		if !ok {
			continue
		}
		if _, err := fmt.Fprintln(inv.Stdout, value); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func cutLine(line string, opts cutOptions) (string, bool) {
	if len(opts.characters) > 0 {
		runes := []rune(line)
		out := make([]rune, 0, len(runes))
		for index, r := range runes {
			if cutIndexSelected(index+1, opts.characters) {
				out = append(out, r)
			}
		}
		return string(out), true
	}

	if !strings.Contains(line, opts.delimiter) {
		if opts.suppressNoDelim {
			return "", false
		}
		return line, true
	}

	fields := strings.Split(line, opts.delimiter)
	selected := make([]string, 0, len(fields))
	for index, field := range fields {
		if cutIndexSelected(index+1, opts.fields) {
			selected = append(selected, field)
		}
	}
	return strings.Join(selected, opts.delimiter), true
}

func cutIndexSelected(index int, ranges []cutRange) bool {
	for _, current := range ranges {
		start := current.start
		end := current.end
		if start == 0 {
			start = 1
		}
		if index < start {
			continue
		}
		if end == 0 || index <= end {
			return true
		}
	}
	return false
}

var _ Command = (*Cut)(nil)
