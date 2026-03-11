package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type NL struct{}

type nlOptions struct {
	style     byte
	format    string
	width     int
	separator string
	start     int
	increment int
}

func NewNL() *NL {
	return &NL{}
}

func (c *NL) Name() string {
	return "nl"
}

func (c *NL) Run(ctx context.Context, inv *Invocation) error {
	opts, names, err := parseNLArgs(inv)
	if err != nil {
		return err
	}
	inputs, err := readNamedInputs(ctx, inv, names, true)
	if err != nil {
		return err
	}

	number := opts.start
	for _, input := range inputs {
		for _, line := range textLines(input.Data) {
			out := strings.Repeat(" ", opts.width)
			if shouldNumberLine(opts.style, line) {
				out = formatNLNumber(opts, number)
				number += opts.increment
			}
			if _, err := fmt.Fprintf(inv.Stdout, "%s%s%s\n", out, opts.separator, line); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}
	return nil
}

func parseNLArgs(inv *Invocation) (nlOptions, []string, error) {
	args := inv.Args
	opts := nlOptions{
		style:     't',
		format:    "rn",
		width:     6,
		separator: "\t",
		start:     1,
		increment: 1,
	}
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
		case arg == "-b":
			if len(args) < 2 {
				return nlOptions{}, nil, exitf(inv, 1, "nl: option requires an argument -- 'b'")
			}
			if err := setNLStyle(inv, &opts, args[1]); err != nil {
				return nlOptions{}, nil, err
			}
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-b") && len(arg) > 2:
			if err := setNLStyle(inv, &opts, arg[2:]); err != nil {
				return nlOptions{}, nil, err
			}
		case arg == "-n":
			if len(args) < 2 {
				return nlOptions{}, nil, exitf(inv, 1, "nl: option requires an argument -- 'n'")
			}
			if err := setNLFormat(inv, &opts, args[1]); err != nil {
				return nlOptions{}, nil, err
			}
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-n") && len(arg) > 2:
			if err := setNLFormat(inv, &opts, arg[2:]); err != nil {
				return nlOptions{}, nil, err
			}
		case arg == "-w":
			value, rest, err := parseNLInt(inv, "w", args[1:])
			if err != nil {
				return nlOptions{}, nil, err
			}
			opts.width = value
			args = rest
			continue
		case strings.HasPrefix(arg, "-w") && len(arg) > 2:
			value, err := strconv.Atoi(arg[2:])
			if err != nil || value < 0 {
				return nlOptions{}, nil, exitf(inv, 1, "nl: invalid width %q", arg[2:])
			}
			opts.width = value
		case arg == "-s":
			if len(args) < 2 {
				return nlOptions{}, nil, exitf(inv, 1, "nl: option requires an argument -- 's'")
			}
			opts.separator = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-s") && len(arg) > 2:
			opts.separator = arg[2:]
		case arg == "-v":
			value, rest, err := parseNLInt(inv, "v", args[1:])
			if err != nil {
				return nlOptions{}, nil, err
			}
			opts.start = value
			args = rest
			continue
		case strings.HasPrefix(arg, "-v") && len(arg) > 2:
			value, err := strconv.Atoi(arg[2:])
			if err != nil {
				return nlOptions{}, nil, exitf(inv, 1, "nl: invalid start %q", arg[2:])
			}
			opts.start = value
		case arg == "-i":
			value, rest, err := parseNLInt(inv, "i", args[1:])
			if err != nil {
				return nlOptions{}, nil, err
			}
			if value <= 0 {
				return nlOptions{}, nil, exitf(inv, 1, "nl: invalid increment %d", value)
			}
			opts.increment = value
			args = rest
			continue
		case strings.HasPrefix(arg, "-i") && len(arg) > 2:
			value, err := strconv.Atoi(arg[2:])
			if err != nil || value <= 0 {
				return nlOptions{}, nil, exitf(inv, 1, "nl: invalid increment %q", arg[2:])
			}
			opts.increment = value
		default:
			return nlOptions{}, nil, exitf(inv, 1, "nl: unsupported flag %s", arg)
		}
		args = args[1:]
	}
	return opts, args, nil
}

func setNLStyle(inv *Invocation, opts *nlOptions, value string) error {
	if len(value) != 1 || !strings.ContainsRune("atn", rune(value[0])) {
		return exitf(inv, 1, "nl: unsupported body numbering style %q", value)
	}
	opts.style = value[0]
	return nil
}

func setNLFormat(inv *Invocation, opts *nlOptions, value string) error {
	switch value {
	case "ln", "rn", "rz":
		opts.format = value
		return nil
	default:
		return exitf(inv, 1, "nl: unsupported numbering format %q", value)
	}
}

func parseNLInt(inv *Invocation, flag string, args []string) (value int, rest []string, err error) {
	if len(args) == 0 {
		return 0, nil, exitf(inv, 1, "nl: option requires an argument -- '%s'", flag)
	}
	value, err = strconv.Atoi(args[0])
	if err != nil {
		return 0, nil, exitf(inv, 1, "nl: invalid numeric value %q", args[0])
	}
	return value, args[1:], nil
}

func shouldNumberLine(style byte, line string) bool {
	switch style {
	case 'a':
		return true
	case 'n':
		return false
	default:
		return line != ""
	}
}

func formatNLNumber(opts nlOptions, number int) string {
	value := strconv.Itoa(number)
	switch opts.format {
	case "ln":
		return fmt.Sprintf("%-*s", opts.width, value)
	case "rz":
		return fmt.Sprintf("%0*s", opts.width, value)
	default:
		return fmt.Sprintf("%*s", opts.width, value)
	}
}

var _ Command = (*NL)(nil)
