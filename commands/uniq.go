package commands

import (
	"context"
	"fmt"
	"strings"
)

type Uniq struct{}

type uniqOptions struct {
	countOnly      bool
	duplicatesOnly bool
	uniqueOnly     bool
}

type uniqGroup struct {
	line  string
	count int
}

func NewUniq() *Uniq {
	return &Uniq{}
}

func (c *Uniq) Name() string {
	return "uniq"
}

func (c *Uniq) Run(ctx context.Context, inv *Invocation) error {
	opts, files, err := parseUniqArgs(inv)
	if err != nil {
		return err
	}

	lines := make([]string, 0)
	exitCode := 0
	if len(files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		lines = append(lines, textLines(data)...)
	} else {
		for _, file := range files {
			data, _, err := readAllFile(ctx, inv, file)
			if err != nil {
				_, _ = fmt.Fprintf(inv.Stderr, "uniq: %s: No such file or directory\n", file)
				exitCode = 1
				continue
			}
			lines = append(lines, textLines(data)...)
		}
	}

	for _, group := range uniqGroups(lines) {
		if !shouldPrintUniqGroup(group, opts) {
			continue
		}
		if opts.countOnly {
			if _, err := fmt.Fprintf(inv.Stdout, "%4d %s\n", group.count, group.line); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			continue
		}
		if _, err := fmt.Fprintln(inv.Stdout, group.line); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseUniqArgs(inv *Invocation) (uniqOptions, []string, error) {
	args := inv.Args
	var opts uniqOptions

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch arg {
		case "-c", "--count":
			opts.countOnly = true
		case "-d", "--repeated":
			opts.duplicatesOnly = true
		case "-u", "--unique":
			opts.uniqueOnly = true
		default:
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				for _, flag := range arg[1:] {
					switch flag {
					case 'c':
						opts.countOnly = true
					case 'd':
						opts.duplicatesOnly = true
					case 'u':
						opts.uniqueOnly = true
					default:
						return uniqOptions{}, nil, exitf(inv, 1, "uniq: unsupported flag -%c", flag)
					}
				}
			} else {
				return uniqOptions{}, nil, exitf(inv, 1, "uniq: unsupported flag %s", arg)
			}
		}
		args = args[1:]
	}

	return opts, args, nil
}

func uniqGroups(lines []string) []uniqGroup {
	if len(lines) == 0 {
		return nil
	}
	groups := make([]uniqGroup, 0, len(lines))
	current := uniqGroup{line: lines[0], count: 1}
	for _, line := range lines[1:] {
		if line == current.line {
			current.count++
			continue
		}
		groups = append(groups, current)
		current = uniqGroup{line: line, count: 1}
	}
	groups = append(groups, current)
	return groups
}

func shouldPrintUniqGroup(group uniqGroup, opts uniqOptions) bool {
	switch {
	case opts.duplicatesOnly && opts.uniqueOnly:
		return false
	case opts.duplicatesOnly:
		return group.count > 1
	case opts.uniqueOnly:
		return group.count == 1
	default:
		return true
	}
}

var _ Command = (*Uniq)(nil)
