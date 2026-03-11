package commands

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

type TR struct{}

type trOptions struct {
	complement bool
	delete     bool
	squeeze    bool
}

type trSet struct {
	ordered []rune
	index   map[rune]int
	present map[rune]bool
}

func NewTR() *TR {
	return &TR{}
}

func (c *TR) Name() string {
	return "tr"
}

func (c *TR) Run(_ context.Context, inv *Invocation) error {
	opts, set1Text, set2Text, err := parseTRArgs(inv)
	if err != nil {
		return err
	}

	set1, err := parseTRSet(set1Text)
	if err != nil {
		return exitf(inv, 1, "tr: %v", err)
	}
	set2, err := parseTRSet(set2Text)
	if err != nil {
		return exitf(inv, 1, "tr: %v", err)
	}

	data, err := readAllStdin(inv)
	if err != nil {
		return err
	}
	var out []rune
	var (
		lastOut rune
		hasLast bool
	)
	squeezeMembership := chooseTRSqueezeMembership(opts, set1, set2)
	replacementFallback := rune(0)
	if len(set2.ordered) > 0 {
		replacementFallback = set2.ordered[len(set2.ordered)-1]
	}

	for _, r := range string(data) {
		matched := set1.contains(r)
		if opts.complement {
			matched = !matched
		}

		emit := true
		current := r
		switch {
		case opts.delete && matched:
			emit = false
		case len(set2.ordered) > 0 && matched:
			if opts.complement {
				current = replacementFallback
			} else {
				current = set2.translate(set1, r)
			}
		}

		if !emit {
			continue
		}
		if opts.squeeze && hasLast && lastOut == current && squeezeMembership(current) {
			continue
		}
		out = append(out, current)
		lastOut = current
		hasLast = true
	}

	if _, err := fmt.Fprint(inv.Stdout, string(out)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseTRArgs(inv *Invocation) (opts trOptions, set1, set2 string, err error) {
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
		switch arg {
		case "-c", "--complement":
			opts.complement = true
			args = args[1:]
			continue
		case "-C":
			opts.complement = true
			args = args[1:]
			continue
		case "-d", "--delete":
			opts.delete = true
			args = args[1:]
			continue
		case "-s", "--squeeze-repeats":
			opts.squeeze = true
			args = args[1:]
			continue
		}
		for _, flag := range arg[1:] {
			switch flag {
			case 'c', 'C':
				opts.complement = true
			case 'd':
				opts.delete = true
			case 's':
				opts.squeeze = true
			default:
				return trOptions{}, "", "", exitf(inv, 1, "tr: unsupported flag -%c", flag)
			}
		}
		args = args[1:]
	}

	if len(args) == 0 {
		return trOptions{}, "", "", exitf(inv, 1, "tr: missing operand")
	}
	set1 = args[0]
	args = args[1:]
	if len(args) > 0 {
		set2 = args[0]
		args = args[1:]
	}
	if len(args) > 0 {
		return trOptions{}, "", "", exitf(inv, 1, "tr: extra operand %q", args[0])
	}
	if !opts.delete && !opts.squeeze && set2 == "" {
		return trOptions{}, "", "", exitf(inv, 1, "tr: missing second operand")
	}
	if opts.delete && !opts.squeeze && set2 != "" {
		return trOptions{}, "", "", exitf(inv, 1, "tr: extra operand %q", set2)
	}
	return opts, set1, set2, nil
}

func parseTRSet(value string) (trSet, error) {
	set := trSet{
		index:   make(map[rune]int),
		present: make(map[rune]bool),
	}
	if value == "" {
		return set, nil
	}
	runes := []rune(value)
	for i := 0; i < len(runes); {
		if runes[i] == '[' && i+1 < len(runes) && runes[i+1] == ':' {
			end := i + 2
			for end+1 < len(runes) && (runes[end] != ':' || runes[end+1] != ']') {
				end++
			}
			if end+1 >= len(runes) {
				return trSet{}, fmt.Errorf("invalid character class %q", value[i:])
			}
			classRunes, err := trClassRunes(string(runes[i+2 : end]))
			if err != nil {
				return trSet{}, err
			}
			for _, r := range classRunes {
				set.append(r)
			}
			i = end + 2
			continue
		}

		left, advance, err := parseTRRune(runes, i)
		if err != nil {
			return trSet{}, err
		}
		next := i + advance
		if next+1 < len(runes) && runes[next] == '-' {
			right, rightAdvance, err := parseTRRune(runes, next+1)
			if err == nil && left <= right {
				for current := left; current <= right; current++ {
					set.append(current)
				}
				i = next + 1 + rightAdvance
				continue
			}
		}
		set.append(left)
		i = next
	}
	return set, nil
}

func parseTRRune(runes []rune, index int) (value rune, advance int, err error) {
	if runes[index] != '\\' {
		return runes[index], 1, nil
	}
	if index+1 >= len(runes) {
		return '\\', 1, nil
	}
	switch runes[index+1] {
	case 'n':
		return '\n', 2, nil
	case 'r':
		return '\r', 2, nil
	case 't':
		return '\t', 2, nil
	case '\\':
		return '\\', 2, nil
	default:
		return runes[index+1], 2, nil
	}
}

func trClassRunes(name string) ([]rune, error) {
	var out []rune
	for r := rune(0); r <= unicode.MaxASCII; r++ {
		switch name {
		case "alnum":
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, r)
			}
		case "alpha":
			if unicode.IsLetter(r) {
				out = append(out, r)
			}
		case "digit":
			if unicode.IsDigit(r) {
				out = append(out, r)
			}
		case "lower":
			if unicode.IsLower(r) {
				out = append(out, r)
			}
		case "space":
			if unicode.IsSpace(r) {
				out = append(out, r)
			}
		case "upper":
			if unicode.IsUpper(r) {
				out = append(out, r)
			}
		default:
			return nil, fmt.Errorf("unsupported character class %q", name)
		}
	}
	return out, nil
}

func (s *trSet) append(r rune) {
	if _, exists := s.index[r]; !exists {
		s.index[r] = len(s.ordered)
	}
	s.ordered = append(s.ordered, r)
	s.present[r] = true
}

func (s trSet) contains(r rune) bool {
	return s.present[r]
}

func (s trSet) translate(source trSet, r rune) rune {
	if len(s.ordered) == 0 {
		return r
	}
	position, ok := source.index[r]
	if !ok {
		return s.ordered[len(s.ordered)-1]
	}
	if position >= len(s.ordered) {
		position = len(s.ordered) - 1
	}
	return s.ordered[position]
}

func chooseTRSqueezeMembership(opts trOptions, set1, set2 trSet) func(rune) bool {
	switch {
	case opts.squeeze && len(set2.ordered) > 0 && !opts.delete:
		return set2.contains
	case opts.squeeze && len(set2.ordered) > 0 && opts.delete:
		return set2.contains
	default:
		return set1.contains
	}
}

var _ Command = (*TR)(nil)
