package commands

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Sort struct{}

type sortOptions struct {
	reverse    bool
	numeric    bool
	unique     bool
	ignoreCase bool
	delimiter  string
	keys       []sortKey
}

type sortKey struct {
	start      int
	end        int
	numeric    bool
	reverse    bool
	ignoreCase bool
}

func NewSort() *Sort {
	return &Sort{}
}

func (c *Sort) Name() string {
	return "sort"
}

func (c *Sort) Run(ctx context.Context, inv *Invocation) error {
	opts, files, err := parseSortArgs(inv)
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
				_, _ = fmt.Fprintf(inv.Stderr, "sort: %s: No such file or directory\n", file)
				exitCode = 1
				continue
			}
			lines = append(lines, textLines(data)...)
		}
	}

	sort.SliceStable(lines, func(i, j int) bool {
		return compareSortLines(lines[i], lines[j], opts) < 0
	})

	if opts.unique {
		lines = uniqueSortedLines(lines, opts)
	}

	if err := writeTextLines(inv.Stdout, lines); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseSortArgs(inv *Invocation) (sortOptions, []string, error) {
	args := inv.Args
	var opts sortOptions

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
		case "-r", "--reverse":
			opts.reverse = true
		case "-n", "--numeric-sort":
			opts.numeric = true
		case "-u", "--unique":
			opts.unique = true
		case "-f", "--ignore-case":
			opts.ignoreCase = true
		case "-k":
			if len(args) < 2 {
				return sortOptions{}, nil, exitf(inv, 1, "sort: option requires an argument -- 'k'")
			}
			key, err := parseSortKey(args[1])
			if err != nil {
				return sortOptions{}, nil, exitf(inv, 1, "sort: invalid key spec %q", args[1])
			}
			opts.keys = append(opts.keys, key)
			args = args[2:]
			continue
		case "-t":
			if len(args) < 2 {
				return sortOptions{}, nil, exitf(inv, 1, "sort: option requires an argument -- 't'")
			}
			opts.delimiter = args[1]
			args = args[2:]
			continue
		case "--help":
			_, _ = fmt.Fprintln(inv.Stdout, "usage: sort [-nruf] [-t SEP] [-k KEY] [FILE...]")
			_, _ = fmt.Fprintln(inv.Stdout, "  -r, --reverse         reverse the result of comparisons")
			_, _ = fmt.Fprintln(inv.Stdout, "  -n, --numeric-sort    compare according to numeric value")
			_, _ = fmt.Fprintln(inv.Stdout, "  -u, --unique          output only the first of equal lines")
			_, _ = fmt.Fprintln(inv.Stdout, "  -f, --ignore-case     fold lower case to upper case characters")
			_, _ = fmt.Fprintln(inv.Stdout, "  -t SEP                use SEP instead of whitespace for field separation")
			_, _ = fmt.Fprintln(inv.Stdout, "  -k KEY                sort via a field key such as 2 or 2,2nr")
			return sortOptions{}, nil, nil
		default:
			switch {
			case strings.HasPrefix(arg, "-k") && len(arg) > 2:
				key, err := parseSortKey(arg[2:])
				if err != nil {
					return sortOptions{}, nil, exitf(inv, 1, "sort: invalid key spec %q", arg[2:])
				}
				opts.keys = append(opts.keys, key)
			case strings.HasPrefix(arg, "-t") && len(arg) > 2:
				opts.delimiter = arg[2:]
			case len(arg) > 2 && arg[0] == '-' && arg[1] != '-':
				for _, flag := range arg[1:] {
					switch flag {
					case 'r':
						opts.reverse = true
					case 'n':
						opts.numeric = true
					case 'u':
						opts.unique = true
					case 'f':
						opts.ignoreCase = true
					default:
						return sortOptions{}, nil, exitf(inv, 1, "sort: unsupported flag -%c", flag)
					}
				}
			default:
				return sortOptions{}, nil, exitf(inv, 1, "sort: unsupported flag %s", arg)
			}
		}
		args = args[1:]
	}

	return opts, args, nil
}

func parseSortKey(spec string) (sortKey, error) {
	var key sortKey
	remainder := spec

	start, rest, ok := consumeSortKeyNumber(remainder)
	if !ok || start <= 0 {
		return sortKey{}, fmt.Errorf("missing start field")
	}
	key.start = start
	remainder = rest

	if strings.HasPrefix(remainder, ",") {
		remainder = remainder[1:]
		end, rest, ok := consumeSortKeyNumber(remainder)
		if ok {
			key.end = end
			remainder = rest
		}
	}

	for _, flag := range remainder {
		switch flag {
		case 'n':
			key.numeric = true
		case 'r':
			key.reverse = true
		case 'f':
			key.ignoreCase = true
		default:
			return sortKey{}, fmt.Errorf("unsupported key modifier %q", string(flag))
		}
	}

	return key, nil
}

func consumeSortKeyNumber(value string) (number int, remainder string, ok bool) {
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, value, false
	}
	number, err := strconv.Atoi(value[:end])
	if err != nil {
		return 0, value, false
	}
	return number, value[end:], true
}

func uniqueSortedLines(lines []string, opts sortOptions) []string {
	if len(lines) == 0 {
		return nil
	}
	out := []string{lines[0]}
	for _, line := range lines[1:] {
		if compareSortLines(out[len(out)-1], line, opts) == 0 {
			continue
		}
		out = append(out, line)
	}
	return out
}

func compareSortLines(a, b string, opts sortOptions) int {
	if len(opts.keys) > 0 {
		for _, key := range opts.keys {
			cmp := compareSortValue(
				extractSortKey(a, key, opts.delimiter),
				extractSortKey(b, key, opts.delimiter),
				opts.numeric || key.numeric,
				opts.ignoreCase || key.ignoreCase,
			)
			if key.reverse || opts.reverse {
				cmp = -cmp
			}
			if cmp != 0 {
				return cmp
			}
		}
	}

	cmp := compareSortValue(a, b, opts.numeric, opts.ignoreCase)
	if opts.reverse {
		cmp = -cmp
	}
	return cmp
}

func extractSortKey(line string, key sortKey, delimiter string) string {
	fields := sortFields(line, delimiter)
	if key.start <= 0 || key.start > len(fields) {
		return ""
	}
	end := key.end
	if end <= 0 || end > len(fields) {
		end = len(fields)
	}
	if end < key.start {
		end = key.start
	}
	return strings.Join(fields[key.start-1:end], " ")
}

func sortFields(line, delimiter string) []string {
	if delimiter == "" {
		return strings.Fields(line)
	}
	return strings.Split(line, delimiter)
}

func compareSortValue(a, b string, numeric, ignoreCase bool) int {
	if numeric {
		return compareFloat(sortNumericValue(a), sortNumericValue(b))
	}
	if ignoreCase {
		a = strings.ToLower(a)
		b = strings.ToLower(b)
	}
	return strings.Compare(a, b)
}

func sortNumericValue(value string) float64 {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return number
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

var _ Command = (*Sort)(nil)
