package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Uniq struct{}

type uniqOptions struct {
	countOnly      bool
	checkChars     int
	checkCharsSet  bool
	duplicatesOnly bool
	ignoreCase     bool
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

	streamer := &uniqStreamer{
		inv:  inv,
		opts: opts,
	}
	exitCode := 0
	if len(files) == 0 {
		if err := streamer.consumeReader(stdinReader(inv)); err != nil {
			return err
		}
	} else {
		for _, file := range files {
			reader, _, err := openRead(ctx, inv, file)
			if err != nil {
				_, _ = fmt.Fprintf(inv.Stderr, "uniq: %s: No such file or directory\n", file)
				exitCode = 1
				continue
			}
			if err := streamer.consumeReader(reader); err != nil {
				_ = reader.Close()
				return err
			}
			_ = reader.Close()
		}
	}

	if err := streamer.flush(); err != nil {
		return err
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

type uniqStreamer struct {
	inv   *Invocation
	opts  uniqOptions
	line  string
	count int
	have  bool
}

func (s *uniqStreamer) consumeReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	var buf [4 * 1024]byte
	scanner.Buffer(buf[:], ScannerTokenLimit(s.inv))
	for scanner.Scan() {
		line := scanner.Text()
		if !s.have {
			s.line = line
			s.count = 1
			s.have = true
			continue
		}
		if equalUniqLine(line, s.line, s.opts) {
			s.count++
			continue
		}
		if err := s.flush(); err != nil {
			return err
		}
		s.line = line
		s.count = 1
		s.have = true
	}
	if err := scanner.Err(); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func (s *uniqStreamer) flush() error {
	if !s.have {
		return nil
	}
	group := uniqGroup{line: s.line, count: s.count}
	s.have = false
	s.line = ""
	s.count = 0

	if !shouldPrintUniqGroup(group, s.opts) {
		return nil
	}
	if s.opts.countOnly {
		if _, err := fmt.Fprintf(s.inv.Stdout, "%4d %s\n", group.count, group.line); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}
	if _, err := fmt.Fprintln(s.inv.Stdout, group.line); err != nil {
		return &ExitError{Code: 1, Err: err}
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
		switch {
		case arg == "-c" || arg == "--count":
			opts.countOnly = true
		case arg == "-w" || arg == "--check-chars":
			if len(args) < 2 {
				return uniqOptions{}, nil, exitf(inv, 1, "uniq: option requires an argument -- 'w'")
			}
			count, err := parseUniqCheckChars(inv, args[1])
			if err != nil {
				return uniqOptions{}, nil, err
			}
			opts.checkChars = count
			opts.checkCharsSet = true
			args = args[1:]
		case arg == "-d" || arg == "--repeated":
			opts.duplicatesOnly = true
		case arg == "-i" || arg == "--ignore-case":
			opts.ignoreCase = true
		case arg == "-u" || arg == "--unique":
			opts.uniqueOnly = true
		case strings.HasPrefix(arg, "--check-chars="):
			count, err := parseUniqCheckChars(inv, strings.TrimPrefix(arg, "--check-chars="))
			if err != nil {
				return uniqOptions{}, nil, err
			}
			opts.checkChars = count
			opts.checkCharsSet = true
		default:
			if len(arg) > 2 && strings.HasPrefix(arg, "-w") {
				count, err := parseUniqCheckChars(inv, arg[2:])
				if err != nil {
					return uniqOptions{}, nil, err
				}
				opts.checkChars = count
				opts.checkCharsSet = true
				args = args[1:]
				continue
			}
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				for _, flag := range arg[1:] {
					switch flag {
					case 'c':
						opts.countOnly = true
					case 'd':
						opts.duplicatesOnly = true
					case 'i':
						opts.ignoreCase = true
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

func parseUniqCheckChars(inv *Invocation, raw string) (int, error) {
	count, err := strconv.Atoi(raw)
	if err != nil || count < 0 {
		return 0, exitf(inv, 1, "uniq: invalid number of characters to compare")
	}
	return count, nil
}

func uniqGroups(lines []string, opts uniqOptions) []uniqGroup {
	if len(lines) == 0 {
		return nil
	}
	groups := make([]uniqGroup, 0, len(lines))
	current := uniqGroup{line: lines[0], count: 1}
	for _, line := range lines[1:] {
		if equalUniqLine(line, current.line, opts) {
			current.count++
			continue
		}
		groups = append(groups, current)
		current = uniqGroup{line: line, count: 1}
	}
	groups = append(groups, current)
	return groups
}

func equalUniqLine(left, right string, opts uniqOptions) bool {
	if opts.checkCharsSet {
		left = uniqPrefixChars(left, opts.checkChars)
		right = uniqPrefixChars(right, opts.checkChars)
	}
	if opts.ignoreCase {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func uniqPrefixChars(line string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(line) <= limit {
		return line
	}
	for idx := range line {
		if limit == 0 {
			return line[:idx]
		}
		limit--
	}
	return line
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
