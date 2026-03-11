package commands

import (
	"context"
	"fmt"
	"regexp"
	gosort "sort"
	"strconv"
	"strings"
	"unicode"

	jbfs "github.com/ewhauser/jbgo/fs"
)

type Sort struct{}

type sortOptions struct {
	reverse             bool
	numeric             bool
	unique              bool
	ignoreCase          bool
	humanNumeric        bool
	versionSort         bool
	dictionaryOrder     bool
	monthSort           bool
	ignoreLeadingBlanks bool
	stable              bool
	checkOnly           bool
	help                bool
	outputFile          string
	fieldDelimiter      *string
	keys                []sortKey
}

type sortKey struct {
	startField      int
	startChar       int
	hasStartChar    bool
	endField        int
	hasEndField     bool
	endChar         int
	hasEndChar      bool
	numeric         bool
	reverse         bool
	ignoreCase      bool
	ignoreLeading   bool
	humanNumeric    bool
	versionSort     bool
	dictionaryOrder bool
	monthSort       bool
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
	if opts.help {
		_, _ = fmt.Fprint(inv.Stdout, sortHelpText)
		return nil
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

	if opts.checkOnly {
		checkFile := "-"
		if len(files) > 0 {
			checkFile = files[0]
		}
		for i := 1; i < len(lines); i++ {
			if compareSortLines(lines[i-1], lines[i], opts) > 0 {
				return exitf(inv, 1, "sort: %s:%d: disorder: %s", checkFile, i+1, lines[i])
			}
		}
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
	}

	gosort.SliceStable(lines, func(i, j int) bool {
		return compareSortLines(lines[i], lines[j], opts) < 0
	})

	if opts.unique {
		lines = uniqueSortedLines(lines, opts)
	}

	output := ""
	if len(lines) > 0 {
		output = strings.Join(lines, "\n") + "\n"
	}

	if opts.outputFile != "" {
		targetAbs := jbfs.Resolve(inv.Dir, opts.outputFile)
		if err := writeFileContents(ctx, inv, targetAbs, []byte(output), 0o644); err != nil {
			return err
		}
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
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

		switch {
		case arg == "--help":
			opts.help = true
			return opts, nil, nil
		case arg == "-r" || arg == "--reverse":
			opts.reverse = true
		case arg == "-n" || arg == "--numeric-sort":
			opts.numeric = true
		case arg == "-u" || arg == "--unique":
			opts.unique = true
		case arg == "-f" || arg == "--ignore-case":
			opts.ignoreCase = true
		case arg == "-h" || arg == "--human-numeric-sort":
			opts.humanNumeric = true
		case arg == "-V" || arg == "--version-sort":
			opts.versionSort = true
		case arg == "-d" || arg == "--dictionary-order":
			opts.dictionaryOrder = true
		case arg == "-M" || arg == "--month-sort":
			opts.monthSort = true
		case arg == "-b" || arg == "--ignore-leading-blanks":
			opts.ignoreLeadingBlanks = true
		case arg == "-s" || arg == "--stable":
			opts.stable = true
		case arg == "-c" || arg == "--check":
			opts.checkOnly = true
		case arg == "-o" || arg == "--output":
			if len(args) < 2 {
				return sortOptions{}, nil, exitf(inv, 1, "sort: option requires an argument -- 'o'")
			}
			opts.outputFile = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-o") && len(arg) > 2:
			opts.outputFile = arg[2:]
		case strings.HasPrefix(arg, "--output="):
			opts.outputFile = strings.TrimPrefix(arg, "--output=")
		case arg == "-t" || arg == "--field-separator":
			if len(args) < 2 {
				return sortOptions{}, nil, exitf(inv, 1, "sort: option requires an argument -- 't'")
			}
			delim := args[1]
			opts.fieldDelimiter = &delim
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-t") && len(arg) > 2:
			delim := arg[2:]
			opts.fieldDelimiter = &delim
		case strings.HasPrefix(arg, "--field-separator="):
			delim := strings.TrimPrefix(arg, "--field-separator=")
			opts.fieldDelimiter = &delim
		case arg == "-k" || arg == "--key":
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
		case strings.HasPrefix(arg, "-k") && len(arg) > 2:
			key, err := parseSortKey(arg[2:])
			if err != nil {
				return sortOptions{}, nil, exitf(inv, 1, "sort: invalid key spec %q", arg[2:])
			}
			opts.keys = append(opts.keys, key)
		case strings.HasPrefix(arg, "--key="):
			key, err := parseSortKey(strings.TrimPrefix(arg, "--key="))
			if err != nil {
				return sortOptions{}, nil, exitf(inv, 1, "sort: invalid key spec %q", strings.TrimPrefix(arg, "--key="))
			}
			opts.keys = append(opts.keys, key)
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
				case 'h':
					opts.humanNumeric = true
				case 'V':
					opts.versionSort = true
				case 'd':
					opts.dictionaryOrder = true
				case 'M':
					opts.monthSort = true
				case 'b':
					opts.ignoreLeadingBlanks = true
				case 's':
					opts.stable = true
				case 'c':
					opts.checkOnly = true
				default:
					return sortOptions{}, nil, exitf(inv, 1, "sort: unsupported flag -%c", flag)
				}
			}
		default:
			return sortOptions{}, nil, exitf(inv, 1, "sort: unsupported flag %s", arg)
		}
		args = args[1:]
	}

	return opts, args, nil
}

func parseSortKey(spec string) (sortKey, error) {
	var key sortKey
	mainSpec := spec
	modifiers := ""

	if match := sortKeyModifierRe.FindStringSubmatch(mainSpec); match != nil {
		modifiers = match[1]
		mainSpec = strings.TrimSuffix(mainSpec, modifiers)
	}

	parts := strings.Split(mainSpec, ",")
	if len(parts) == 0 || parts[0] == "" {
		return sortKey{}, fmt.Errorf("missing start field")
	}

	startField, startChar, hasStartChar, err := parseSortFieldPart(parts[0])
	if err != nil {
		return sortKey{}, err
	}
	key.startField = startField
	key.startChar = startChar
	key.hasStartChar = hasStartChar

	if len(parts) > 1 && parts[1] != "" {
		endPart := parts[1]
		if match := sortKeyModifierRe.FindStringSubmatch(endPart); match != nil {
			modifiers += match[1]
			endPart = strings.TrimSuffix(endPart, match[1])
		}
		endField, endChar, hasEndChar, err := parseSortFieldPart(endPart)
		if err != nil {
			return sortKey{}, err
		}
		key.endField = endField
		key.hasEndField = true
		key.endChar = endChar
		key.hasEndChar = hasEndChar
	}

	for _, flag := range modifiers {
		switch flag {
		case 'n':
			key.numeric = true
		case 'r':
			key.reverse = true
		case 'f':
			key.ignoreCase = true
		case 'b':
			key.ignoreLeading = true
		case 'h':
			key.humanNumeric = true
		case 'V':
			key.versionSort = true
		case 'd':
			key.dictionaryOrder = true
		case 'M':
			key.monthSort = true
		default:
			return sortKey{}, fmt.Errorf("unsupported key modifier %q", string(flag))
		}
	}

	return key, nil
}

func parseSortFieldPart(spec string) (field, char int, hasChar bool, err error) {
	parts := strings.Split(spec, ".")
	field, err = strconv.Atoi(parts[0])
	if err != nil || field < 1 {
		return 0, 0, false, fmt.Errorf("invalid field")
	}
	if len(parts) > 1 && parts[1] != "" {
		char, err = strconv.Atoi(parts[1])
		if err != nil || char < 1 {
			return 0, 0, false, fmt.Errorf("invalid character position")
		}
		return field, char, true, nil
	}
	return field, 0, false, nil
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

func compareSortLines(a, b string, opts sortOptions) int {
	lineA := a
	lineB := b
	if opts.ignoreLeadingBlanks {
		lineA = strings.TrimLeftFunc(lineA, unicode.IsSpace)
		lineB = strings.TrimLeftFunc(lineB, unicode.IsSpace)
	}

	if len(opts.keys) == 0 {
		cmp := compareSortValues(lineA, lineB, opts.numeric, opts.ignoreCase, opts.humanNumeric, opts.versionSort, opts.dictionaryOrder, opts.monthSort)
		if cmp != 0 {
			if opts.reverse {
				return -cmp
			}
			return cmp
		}
		if !opts.stable {
			tiebreaker := strings.Compare(a, b)
			if opts.reverse {
				return -tiebreaker
			}
			return tiebreaker
		}
		return 0
	}

	for _, key := range opts.keys {
		valA := extractSortKeyValue(lineA, key, opts.fieldDelimiter)
		valB := extractSortKeyValue(lineB, key, opts.fieldDelimiter)
		if key.ignoreLeading {
			valA = strings.TrimLeftFunc(valA, unicode.IsSpace)
			valB = strings.TrimLeftFunc(valB, unicode.IsSpace)
		}

		cmp := compareSortValues(
			valA,
			valB,
			key.numeric || opts.numeric,
			key.ignoreCase || opts.ignoreCase,
			key.humanNumeric || opts.humanNumeric,
			key.versionSort || opts.versionSort,
			key.dictionaryOrder || opts.dictionaryOrder,
			key.monthSort || opts.monthSort,
		)
		useReverse := key.reverse || opts.reverse
		if cmp != 0 {
			if useReverse {
				return -cmp
			}
			return cmp
		}
	}

	if !opts.stable {
		tiebreaker := strings.Compare(a, b)
		if opts.reverse {
			return -tiebreaker
		}
		return tiebreaker
	}
	return 0
}

func extractSortKeyValue(line string, key sortKey, delimiter *string) string {
	fields := sortFields(line, delimiter)
	start := key.startField - 1
	if start < 0 || start >= len(fields) {
		return ""
	}

	if !key.hasEndField {
		field := fields[start]
		if key.hasStartChar {
			field = sliceFromChar(field, key.startChar)
		}
		return field
	}

	end := key.endField - 1
	if end >= len(fields) {
		end = len(fields) - 1
	}
	if end < start {
		end = start
	}

	joiner := " "
	if delimiter != nil {
		joiner = *delimiter
	}

	parts := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		field := fields[i]
		if i == start && key.hasStartChar {
			field = sliceFromChar(field, key.startChar)
		}
		if i == end && key.hasEndChar {
			field = sliceToChar(field, key.endChar)
		}
		parts = append(parts, field)
	}
	return strings.Join(parts, joiner)
}

func sortFields(line string, delimiter *string) []string {
	if delimiter == nil {
		return strings.Fields(line)
	}
	return strings.Split(line, *delimiter)
}

func uniqueSortedLines(lines []string, opts sortOptions) []string {
	if len(lines) == 0 {
		return nil
	}
	out := []string{lines[0]}
	for _, line := range lines[1:] {
		if sortLinesEquivalent(out[len(out)-1], line, opts) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func sortLinesEquivalent(a, b string, opts sortOptions) bool {
	lineA := a
	lineB := b
	if opts.ignoreLeadingBlanks {
		lineA = strings.TrimLeftFunc(lineA, unicode.IsSpace)
		lineB = strings.TrimLeftFunc(lineB, unicode.IsSpace)
	}

	if len(opts.keys) == 0 {
		return compareSortValues(lineA, lineB, opts.numeric, opts.ignoreCase, opts.humanNumeric, opts.versionSort, opts.dictionaryOrder, opts.monthSort) == 0
	}

	for _, key := range opts.keys {
		valA := extractSortKeyValue(lineA, key, opts.fieldDelimiter)
		valB := extractSortKeyValue(lineB, key, opts.fieldDelimiter)
		if key.ignoreLeading {
			valA = strings.TrimLeftFunc(valA, unicode.IsSpace)
			valB = strings.TrimLeftFunc(valB, unicode.IsSpace)
		}
		if compareSortValues(
			valA,
			valB,
			key.numeric || opts.numeric,
			key.ignoreCase || opts.ignoreCase,
			key.humanNumeric || opts.humanNumeric,
			key.versionSort || opts.versionSort,
			key.dictionaryOrder || opts.dictionaryOrder,
			key.monthSort || opts.monthSort,
		) != 0 {
			return false
		}
	}
	return true
}

func compareSortValues(a, b string, numeric, ignoreCase, humanNumeric, versionSort, dictionaryOrder, monthSort bool) int {
	valA := a
	valB := b

	if dictionaryOrder {
		valA = toDictionaryOrder(valA)
		valB = toDictionaryOrder(valB)
	}
	if ignoreCase {
		valA = strings.ToLower(valA)
		valB = strings.ToLower(valB)
	}
	if monthSort {
		return compareFloat(float64(parseMonth(valA)), float64(parseMonth(valB)))
	}
	if humanNumeric {
		return compareFloat(parseHumanSize(valA), parseHumanSize(valB))
	}
	if versionSort {
		return compareVersions(valA, valB)
	}
	if numeric {
		return compareFloat(sortNumericValue(valA), sortNumericValue(valB))
	}
	return strings.Compare(valA, valB)
}

func toDictionaryOrder(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseMonth(value string) int {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if len(trimmed) > 3 {
		trimmed = trimmed[:3]
	}
	switch trimmed {
	case "jan":
		return 1
	case "feb":
		return 2
	case "mar":
		return 3
	case "apr":
		return 4
	case "may":
		return 5
	case "jun":
		return 6
	case "jul":
		return 7
	case "aug":
		return 8
	case "sep":
		return 9
	case "oct":
		return 10
	case "nov":
		return 11
	case "dec":
		return 12
	default:
		return 0
	}
}

var humanSizeRe = regexp.MustCompile(`^\s*([+-]?\d*\.?\d+)\s*([kmgtpeKMGTPE])?[iI]?[bB]?\s*$`)

func parseHumanSize(value string) float64 {
	trimmed := strings.TrimSpace(value)
	match := humanSizeRe.FindStringSubmatch(trimmed)
	if match == nil {
		number, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0
		}
		return number
	}

	number, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}

	switch strings.ToLower(match[2]) {
	case "k":
		return number * 1024
	case "m":
		return number * 1024 * 1024
	case "g":
		return number * 1024 * 1024 * 1024
	case "t":
		return number * 1024 * 1024 * 1024 * 1024
	case "p":
		return number * 1024 * 1024 * 1024 * 1024 * 1024
	case "e":
		return number * 1024 * 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return number
	}
}

var versionChunkRe = regexp.MustCompile(`\d+|\D+`)

func compareVersions(a, b string) int {
	partsA := versionChunkRe.FindAllString(a, -1)
	partsB := versionChunkRe.FindAllString(b, -1)
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		partA := ""
		partB := ""
		if i < len(partsA) {
			partA = partsA[i]
		}
		if i < len(partsB) {
			partB = partsB[i]
		}

		numA, errA := strconv.Atoi(partA)
		numB, errB := strconv.Atoi(partB)
		if errA == nil && errB == nil {
			if numA < numB {
				return -1
			}
			if numA > numB {
				return 1
			}
			continue
		}

		if cmp := strings.Compare(partA, partB); cmp != 0 {
			return cmp
		}
	}
	return 0
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

func sliceFromChar(value string, start int) string {
	if start <= 1 {
		return value
	}
	runes := []rune(value)
	idx := start - 1
	if idx >= len(runes) {
		return ""
	}
	return string(runes[idx:])
}

func sliceToChar(value string, end int) string {
	if end <= 0 {
		return ""
	}
	runes := []rune(value)
	if end >= len(runes) {
		return value
	}
	return string(runes[:end])
}

var sortKeyModifierRe = regexp.MustCompile(`([bdfhMnrV]+)$`)

const sortHelpText = `sort - sort lines of text files

Usage:
  sort [OPTION]... [FILE]...

Supported options:
  -b, --ignore-leading-blanks
                         ignore leading blanks
  -c, --check           check whether input is sorted
  -d, --dictionary-order
                         consider only blanks and alphanumeric characters
  -f, --ignore-case     fold lower case to upper case characters
  -h, --human-numeric-sort
                         compare human-readable numbers
  -k, --key=KEYDEF      sort via a key definition
  -M, --month-sort      compare month names
  -n, --numeric-sort    compare according to numeric value
  -o, --output=FILE     write result to FILE instead of stdout
  -r, --reverse         reverse the result of comparisons
  -s, --stable          disable last-resort whole-line comparison
  -t, --field-separator=SEP
                         use SEP instead of whitespace for field separation
  -u, --unique          output only the first of equal lines
  -V, --version-sort    natural sort of version numbers
  --help                show this help text
`

var _ Command = (*Sort)(nil)
