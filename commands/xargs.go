package commands

import (
	"context"
	"fmt"
	"strings"
)

type XArgs struct{}

type xargsOptions struct {
	maxArgs      int
	replace      string
	delimiter    *string
	nullInput    bool
	verbose      bool
	noRunIfEmpty bool
}

func NewXArgs() *XArgs {
	return &XArgs{}
}

func (c *XArgs) Name() string {
	return "xargs"
}

func (c *XArgs) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: xargs [-0rt] [-n MAX-ARGS] [-I REPL] [-d DELIM] [COMMAND [ARG...]]")
		return nil
	}
	opts, cmdArgs, err := parseXArgs(inv)
	if err != nil {
		return err
	}
	data, err := readAllStdin(inv)
	if err != nil {
		return err
	}
	items, err := parseXArgsItems(data, opts)
	if err != nil {
		return exitf(inv, 1, "xargs: %v", err)
	}
	if len(items) == 0 {
		if opts.noRunIfEmpty {
			return nil
		}
		if len(cmdArgs) == 0 {
			cmdArgs = []string{"echo"}
		}
		return runXArgsCommand(ctx, inv, cmdArgs, opts.verbose)
	}
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"echo"}
	}

	if opts.replace != "" {
		for _, item := range items {
			argv := replaceXArgsPlaceholders(cmdArgs, opts.replace, item)
			if err := runXArgsCommand(ctx, inv, argv, opts.verbose); err != nil {
				return err
			}
		}
		return nil
	}

	batchSize := len(items)
	if opts.maxArgs > 0 {
		batchSize = opts.maxArgs
	}
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		argv := append(append([]string(nil), cmdArgs...), items[start:end]...)
		if err := runXArgsCommand(ctx, inv, argv, opts.verbose); err != nil {
			return err
		}
	}
	return nil
}

func parseXArgs(inv *Invocation) (opts xargsOptions, cmdArgs []string, err error) {
	args := inv.Args
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
		case arg == "-n":
			if len(args) < 2 {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option requires an argument -- 'n'")
			}
			value, err := parsePositiveInt(args[1])
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid max-args %q", args[1])
			}
			opts.maxArgs = value
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-n") && len(arg) > 2:
			value, err := parsePositiveInt(arg[2:])
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid max-args %q", arg[2:])
			}
			opts.maxArgs = value
		case arg == "--max-args":
			if len(args) < 2 {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option requires an argument -- max-args")
			}
			value, err := parsePositiveInt(args[1])
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid max-args %q", args[1])
			}
			opts.maxArgs = value
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--max-args="):
			value, err := parsePositiveInt(strings.TrimPrefix(arg, "--max-args="))
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid max-args %q", strings.TrimPrefix(arg, "--max-args="))
			}
			opts.maxArgs = value
		case arg == "-I":
			if len(args) < 2 {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option requires an argument -- 'I'")
			}
			opts.replace = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-I") && len(arg) > 2:
			opts.replace = arg[2:]
		case arg == "--replace":
			if len(args) < 2 {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option requires an argument -- replace")
			}
			opts.replace = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--replace="):
			opts.replace = strings.TrimPrefix(arg, "--replace=")
		case arg == "-0":
			opts.nullInput = true
		case arg == "--null":
			opts.nullInput = true
		case arg == "-d":
			if len(args) < 2 {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option requires an argument -- 'd'")
			}
			decoded, err := decodeDelimiterValue(args[1])
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid delimiter %q", args[1])
			}
			opts.delimiter = &decoded
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-d") && len(arg) > 2:
			decoded, err := decodeDelimiterValue(arg[2:])
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid delimiter %q", arg[2:])
			}
			opts.delimiter = &decoded
		case arg == "--delimiter":
			if len(args) < 2 {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option requires an argument -- delimiter")
			}
			decoded, err := decodeDelimiterValue(args[1])
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid delimiter %q", args[1])
			}
			opts.delimiter = &decoded
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--delimiter="):
			decoded, err := decodeDelimiterValue(strings.TrimPrefix(arg, "--delimiter="))
			if err != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: invalid delimiter %q", strings.TrimPrefix(arg, "--delimiter="))
			}
			opts.delimiter = &decoded
		case arg == "-t":
			opts.verbose = true
		case arg == "--verbose":
			opts.verbose = true
		case arg == "-r":
			opts.noRunIfEmpty = true
		case arg == "--no-run-if-empty":
			opts.noRunIfEmpty = true
		case arg == "-P":
			if len(args) < 2 {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option requires an argument -- 'P'")
			}
			args = args[2:]
			continue
		default:
			return xargsOptions{}, nil, exitf(inv, 1, "xargs: unsupported flag %s", arg)
		}
		args = args[1:]
	}
	return opts, args, nil
}

func parseXArgsItems(data []byte, opts xargsOptions) ([]string, error) {
	switch {
	case opts.nullInput:
		return filterEmpty(strings.Split(string(data), string([]byte{0}))), nil
	case opts.delimiter != nil:
		return filterEmpty(strings.Split(string(data), *opts.delimiter)), nil
	case opts.replace != "":
		return filterEmpty(textLines(data)), nil
	default:
		return strings.Fields(string(data)), nil
	}
}

func replaceXArgsPlaceholders(args []string, placeholder, item string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = strings.ReplaceAll(arg, placeholder, item)
	}
	return out
}

func runXArgsCommand(ctx context.Context, inv *Invocation, argv []string, verbose bool) error {
	if len(argv) == 0 {
		return nil
	}
	if verbose {
		if _, err := fmt.Fprintln(inv.Stderr, shellJoinArgs(argv)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	result, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:    argv,
		Env:     inv.Env,
		WorkDir: inv.Dir,
	})
	if err != nil {
		return err
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func filterEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

var _ Command = (*XArgs)(nil)
