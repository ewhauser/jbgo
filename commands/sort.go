package commands

import (
	"bytes"
	"context"
	"fmt"
	"math"
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
	generalNumeric      bool
	randomSort          bool
	unique              bool
	ignoreCase          bool
	ignoreNonprinting   bool
	humanNumeric        bool
	versionSort         bool
	dictionaryOrder     bool
	monthSort           bool
	ignoreLeadingBlanks bool
	stable              bool
	merge               bool
	checkOnly           bool
	checkQuiet          bool
	help                bool
	showVersion         bool
	debug               bool
	zeroTerminated      bool
	outputFile          string
	files0From          string
	fieldDelimiter      *string
	compressProgram     string
	randomSource        string
	parallel            int
	batchSize           int
	batchSizeSet        bool
	bufferSize          string
	tempDirs            []string
	keys                []sortKey
}

type sortKey struct {
	startField        int
	startChar         int
	hasStartChar      bool
	endField          int
	hasEndField       bool
	endChar           int
	hasEndChar        bool
	numeric           bool
	generalNumeric    bool
	reverse           bool
	ignoreCase        bool
	ignoreNonprinting bool
	ignoreLeading     bool
	humanNumeric      bool
	versionSort       bool
	dictionaryOrder   bool
	monthSort         bool
}

type sortGeneralNumber struct {
	kind  int
	value float64
}

const (
	sortNumberInvalid = iota
	sortNumberFinite
	sortNumberNaN
)

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
	if opts.showVersion {
		_, _ = fmt.Fprint(inv.Stdout, sortVersionText)
		return nil
	}
	if err := validateSortOptions(inv, &opts); err != nil {
		return err
	}

	lines, checkFile, exitCode, err := collectSortInput(ctx, inv, &opts, files)
	if err != nil {
		return err
	}
	if opts.compressProgram != "" {
		if err := sortRunCompressProgram(ctx, inv, opts.compressProgram); err != nil {
			return err
		}
	}

	if opts.debug {
		_, _ = fmt.Fprintln(inv.Stderr, "sort: text ordering performed using simple byte comparison")
	}

	if opts.checkOnly {
		for i := 1; i < len(lines); i++ {
			cmp := compareSortLines(lines[i-1], lines[i], &opts)
			if cmp > 0 || (opts.unique && sortLinesEquivalent(lines[i-1], lines[i], &opts)) {
				if opts.checkQuiet {
					return &ExitError{Code: 1}
				}
				return exitf(inv, 1, "sort: %s:%d: disorder: %s", checkFile, i+1, lines[i])
			}
		}
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
	}

	if !opts.randomSort {
		gosort.SliceStable(lines, func(i, j int) bool {
			return compareSortLines(lines[i], lines[j], &opts) < 0
		})
	}

	if opts.unique {
		lines = uniqueSortedLines(lines, &opts)
	}

	output := encodeSortRecords(lines, opts.zeroTerminated)
	if opts.outputFile != "" {
		targetAbs := jbfs.Resolve(inv.Cwd, opts.outputFile)
		if err := writeFileContents(ctx, inv, targetAbs, output, 0o644); err != nil {
			return err
		}
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
	}

	if _, err := inv.Stdout.Write(output); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseSortArgs(inv *Invocation) (sortOptions, []string, error) {
	args := append([]string(nil), inv.Args...)
	var opts sortOptions

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if consumed, handled, err := maybeParseLegacySortKey(args, &opts, inv); handled {
			if err != nil {
				return sortOptions{}, nil, err
			}
			args = args[consumed:]
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		if strings.HasPrefix(arg, "--") {
			consumed, err := parseSortLongOption(inv, &opts, args)
			if err != nil {
				return sortOptions{}, nil, err
			}
			args = args[consumed:]
			continue
		}
		consumed, err := parseSortShortOption(inv, &opts, args)
		if err != nil {
			return sortOptions{}, nil, err
		}
		args = args[consumed:]
	}

	return opts, args, nil
}

func parseSortLongOption(inv *Invocation, opts *sortOptions, args []string) (int, error) {
	arg := args[0]
	switch {
	case arg == "--help":
		opts.help = true
		return 1, nil
	case arg == "--version":
		opts.showVersion = true
		return 1, nil
	case arg == "--debug":
		opts.debug = true
		return 1, nil
	case arg == "--reverse":
		opts.reverse = true
		return 1, nil
	case arg == "--numeric-sort":
		opts.numeric = true
		return 1, nil
	case arg == "--general-numeric-sort":
		opts.generalNumeric = true
		return 1, nil
	case arg == "--random-sort":
		opts.randomSort = true
		return 1, nil
	case arg == "--unique":
		opts.unique = true
		return 1, nil
	case arg == "--ignore-case":
		opts.ignoreCase = true
		return 1, nil
	case arg == "--ignore-nonprinting":
		opts.ignoreNonprinting = true
		return 1, nil
	case arg == "--human-numeric-sort":
		opts.humanNumeric = true
		return 1, nil
	case arg == "--version-sort":
		opts.versionSort = true
		return 1, nil
	case arg == "--dictionary-order":
		opts.dictionaryOrder = true
		return 1, nil
	case arg == "--month-sort":
		opts.monthSort = true
		return 1, nil
	case arg == "--ignore-leading-blanks":
		opts.ignoreLeadingBlanks = true
		return 1, nil
	case arg == "--stable":
		opts.stable = true
		return 1, nil
	case arg == "--merge":
		opts.merge = true
		return 1, nil
	case arg == "--zero-terminated":
		opts.zeroTerminated = true
		return 1, nil
	case arg == "--check":
		opts.checkOnly = true
		return 1, nil
	case arg == "--check=quiet" || arg == "--check=silent":
		opts.checkOnly = true
		opts.checkQuiet = true
		return 1, nil
	case arg == "--check=diagnose-first":
		opts.checkOnly = true
		opts.checkQuiet = false
		return 1, nil
	case arg == "--output":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- 'o'")
		}
		opts.outputFile = args[1]
		return 2, nil
	case strings.HasPrefix(arg, "--output="):
		opts.outputFile = strings.TrimPrefix(arg, "--output=")
		return 1, nil
	case arg == "--field-separator":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- 't'")
		}
		delim := args[1]
		opts.fieldDelimiter = &delim
		return 2, nil
	case strings.HasPrefix(arg, "--field-separator="):
		delim := strings.TrimPrefix(arg, "--field-separator=")
		opts.fieldDelimiter = &delim
		return 1, nil
	case arg == "--key":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- 'k'")
		}
		if err := appendSortKey(&opts.keys, args[1]); err != nil {
			return 0, sortOptionf(inv, "sort: invalid field specification %q", args[1])
		}
		return 2, nil
	case strings.HasPrefix(arg, "--key="):
		value := strings.TrimPrefix(arg, "--key=")
		if err := appendSortKey(&opts.keys, value); err != nil {
			return 0, sortOptionf(inv, "sort: invalid field specification %q", value)
		}
		return 1, nil
	case arg == "--sort":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- sort")
		}
		if err := applySortMode(opts, args[1], inv); err != nil {
			return 0, err
		}
		return 2, nil
	case strings.HasPrefix(arg, "--sort="):
		if err := applySortMode(opts, strings.TrimPrefix(arg, "--sort="), inv); err != nil {
			return 0, err
		}
		return 1, nil
	case arg == "--parallel":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- parallel")
		}
		value, err := parseSortPositiveInt(inv, "parallel", args[1], 1)
		if err != nil {
			return 0, err
		}
		opts.parallel = value
		return 2, nil
	case strings.HasPrefix(arg, "--parallel="):
		value, err := parseSortPositiveInt(inv, "parallel", strings.TrimPrefix(arg, "--parallel="), 1)
		if err != nil {
			return 0, err
		}
		opts.parallel = value
		return 1, nil
	case arg == "--batch-size":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- batch-size")
		}
		value, err := parseSortPositiveInt(inv, "batch-size", args[1], 2)
		if err != nil {
			return 0, err
		}
		opts.batchSize = value
		opts.batchSizeSet = true
		return 2, nil
	case strings.HasPrefix(arg, "--batch-size="):
		value, err := parseSortPositiveInt(inv, "batch-size", strings.TrimPrefix(arg, "--batch-size="), 2)
		if err != nil {
			return 0, err
		}
		opts.batchSize = value
		opts.batchSizeSet = true
		return 1, nil
	case arg == "--buffer-size":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- S")
		}
		opts.bufferSize = args[1]
		return 2, nil
	case strings.HasPrefix(arg, "--buffer-size="):
		opts.bufferSize = strings.TrimPrefix(arg, "--buffer-size=")
		return 1, nil
	case arg == "--temporary-directory":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- T")
		}
		opts.tempDirs = append(opts.tempDirs, args[1])
		return 2, nil
	case strings.HasPrefix(arg, "--temporary-directory="):
		opts.tempDirs = append(opts.tempDirs, strings.TrimPrefix(arg, "--temporary-directory="))
		return 1, nil
	case arg == "--compress-program":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- compress-program")
		}
		opts.compressProgram = args[1]
		return 2, nil
	case strings.HasPrefix(arg, "--compress-program="):
		opts.compressProgram = strings.TrimPrefix(arg, "--compress-program=")
		return 1, nil
	case arg == "--files0-from":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- files0-from")
		}
		opts.files0From = args[1]
		return 2, nil
	case strings.HasPrefix(arg, "--files0-from="):
		opts.files0From = strings.TrimPrefix(arg, "--files0-from=")
		return 1, nil
	case arg == "--random-source":
		if len(args) < 2 {
			return 0, sortOptionf(inv, "sort: option requires an argument -- random-source")
		}
		opts.randomSource = args[1]
		return 2, nil
	case strings.HasPrefix(arg, "--random-source="):
		opts.randomSource = strings.TrimPrefix(arg, "--random-source=")
		return 1, nil
	default:
		return 0, sortOptionf(inv, "sort: unsupported flag %s", arg)
	}
}

func parseSortShortOption(inv *Invocation, opts *sortOptions, args []string) (int, error) {
	arg := args[0]
	consumed := 1
	short := arg[1:]
	for i := 0; i < len(short); i++ {
		flag := short[i]
		switch flag {
		case 'b':
			opts.ignoreLeadingBlanks = true
		case 'c':
			opts.checkOnly = true
		case 'C':
			opts.checkOnly = true
			opts.checkQuiet = true
		case 'd':
			opts.dictionaryOrder = true
		case 'f':
			opts.ignoreCase = true
		case 'g':
			opts.generalNumeric = true
		case 'h':
			opts.humanNumeric = true
		case 'i':
			opts.ignoreNonprinting = true
		case 'm':
			opts.merge = true
		case 'M':
			opts.monthSort = true
		case 'n':
			opts.numeric = true
		case 'r':
			opts.reverse = true
		case 'R':
			opts.randomSort = true
		case 's':
			opts.stable = true
		case 'u':
			opts.unique = true
		case 'V':
			opts.versionSort = true
		case 'z':
			opts.zeroTerminated = true
		case 'o':
			value, nextConsumed, err := sortShortOptionValue(inv, args, short, i+1, 'o')
			if err != nil {
				return 0, err
			}
			opts.outputFile = value
			return consumed + nextConsumed, nil
		case 't':
			value, nextConsumed, err := sortShortOptionValue(inv, args, short, i+1, 't')
			if err != nil {
				return 0, err
			}
			delim := value
			opts.fieldDelimiter = &delim
			return consumed + nextConsumed, nil
		case 'k':
			value, nextConsumed, err := sortShortOptionValue(inv, args, short, i+1, 'k')
			if err != nil {
				return 0, err
			}
			if err := appendSortKey(&opts.keys, value); err != nil {
				return 0, sortOptionf(inv, "sort: invalid field specification %q", value)
			}
			return consumed + nextConsumed, nil
		case 'S':
			value, nextConsumed, err := sortShortOptionValue(inv, args, short, i+1, 'S')
			if err != nil {
				return 0, err
			}
			opts.bufferSize = value
			return consumed + nextConsumed, nil
		case 'T':
			value, nextConsumed, err := sortShortOptionValue(inv, args, short, i+1, 'T')
			if err != nil {
				return 0, err
			}
			opts.tempDirs = append(opts.tempDirs, value)
			return consumed + nextConsumed, nil
		default:
			return 0, sortOptionf(inv, "sort: unsupported flag -%c", flag)
		}
	}
	return consumed, nil
}

func sortShortOptionValue(inv *Invocation, args []string, short string, offset int, flag byte) (value string, consumed int, err error) {
	if offset < len(short) {
		return short[offset:], 0, nil
	}
	if len(args) < 2 {
		return "", 0, sortOptionf(inv, "sort: option requires an argument -- '%c'", flag)
	}
	return args[1], 1, nil
}

func maybeParseLegacySortKey(args []string, opts *sortOptions, inv *Invocation) (consumed int, handled bool, err error) {
	if len(args) == 0 {
		return 0, false, nil
	}
	start := args[0]
	if !strings.HasPrefix(start, "+") || len(start) == 1 {
		return 0, false, nil
	}
	end := ""
	consumed = 1
	if len(args) > 1 && isLegacySortEndArg(args[1]) {
		end = args[1]
		consumed = 2
	}
	key, err := parseLegacySortKey(start, end)
	if err != nil {
		return 0, true, sortOptionf(inv, "sort: invalid field specification %q", start)
	}
	opts.keys = append(opts.keys, key)
	return consumed, true, nil
}

func isLegacySortEndArg(arg string) bool {
	return len(arg) > 1 && arg[0] == '-' && arg[1] >= '0' && arg[1] <= '9'
}

func parseLegacySortKey(start, end string) (sortKey, error) {
	key, modifiers, err := parseLegacySortPoint(strings.TrimPrefix(start, "+"))
	if err != nil {
		return sortKey{}, err
	}
	if end != "" {
		endKey, extraModifiers, err := parseLegacySortPoint(strings.TrimPrefix(end, "-"))
		if err != nil {
			return sortKey{}, err
		}
		key.endField = endKey.startField
		key.hasEndField = true
		key.endChar = max(endKey.startChar-1, 0)
		if endKey.hasStartChar {
			key.hasEndChar = true
		}
		modifiers += extraModifiers
	}
	applySortKeyModifiers(&key, modifiers)
	return key, nil
}

func parseLegacySortPoint(spec string) (sortKey, string, error) {
	var key sortKey
	fieldText, rest := consumeDigits(spec)
	if fieldText == "" {
		return sortKey{}, "", fmt.Errorf("missing field")
	}
	fieldValue, err := parseSortCount(fieldText)
	if err != nil {
		return sortKey{}, "", err
	}
	key.startField = fieldValue + 1
	if strings.HasPrefix(rest, ".") {
		charText, more := consumeDigits(rest[1:])
		rest = more
		if charText == "" {
			key.hasStartChar = true
			key.startChar = 1
		} else {
			charValue, err := parseSortCount(charText)
			if err != nil {
				return sortKey{}, "", err
			}
			key.hasStartChar = true
			key.startChar = charValue + 1
		}
	}
	if key.startChar == 0 {
		key.startChar = 1
	}
	return key, rest, nil
}

func appendSortKey(keys *[]sortKey, spec string) error {
	key, err := parseSortKey(spec)
	if err != nil {
		return err
	}
	*keys = append(*keys, key)
	return nil
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

	startField, startChar, hasStartChar, err := parseSortFieldPart(parts[0], false)
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
		endField, endChar, hasEndChar, err := parseSortFieldPart(endPart, true)
		if err != nil {
			return sortKey{}, err
		}
		key.endField = endField
		key.hasEndField = true
		key.endChar = endChar
		key.hasEndChar = hasEndChar
	}

	applySortKeyModifiers(&key, modifiers)
	return key, nil
}

func applySortKeyModifiers(key *sortKey, modifiers string) {
	for _, flag := range modifiers {
		switch flag {
		case 'n':
			key.numeric = true
		case 'g':
			key.generalNumeric = true
		case 'r':
			key.reverse = true
		case 'f':
			key.ignoreCase = true
		case 'i':
			key.ignoreNonprinting = true
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
		case 'R':
			// Accepted in legacy forms as a no-op for our in-memory implementation.
		}
	}
}

func parseSortFieldPart(spec string, allowZeroChar bool) (field, char int, hasChar bool, err error) {
	parts := strings.Split(spec, ".")
	field, err = strconv.Atoi(parts[0])
	if err != nil || field < 1 {
		return 0, 0, false, fmt.Errorf("invalid field")
	}
	if len(parts) > 1 && parts[1] != "" {
		char, err = strconv.Atoi(parts[1])
		if err != nil || char < 0 || (!allowZeroChar && char < 1) {
			return 0, 0, false, fmt.Errorf("invalid character position")
		}
		return field, char, true, nil
	}
	return field, 0, false, nil
}

func consumeSortKeyNumber(value string) (number int, remainder string, ok bool) {
	digits, rest := consumeDigits(value)
	if digits == "" {
		return 0, value, false
	}
	parsed, err := strconv.Atoi(digits)
	if err != nil {
		return 0, value, false
	}
	return parsed, rest, true
}

func validateSortOptions(inv *Invocation, opts *sortOptions) error {
	if opts.humanNumeric && opts.numeric {
		return sortOptionf(inv, "sort: options '-hn' are incompatible")
	}
	return nil
}

func applySortMode(opts *sortOptions, value string, inv *Invocation) error {
	switch value {
	case "general-numeric":
		opts.generalNumeric = true
	case "human-numeric":
		opts.humanNumeric = true
	case "month":
		opts.monthSort = true
	case "numeric":
		opts.numeric = true
	case "random":
		opts.randomSort = true
	case "version":
		opts.versionSort = true
	default:
		return sortOptionf(inv, "sort: invalid argument %q for --sort", value)
	}
	return nil
}

func parseSortPositiveInt(inv *Invocation, name, value string, minimum int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, sortOptionf(inv, "sort: invalid --%s argument %q", name, value)
	}
	if parsed < minimum {
		if name == "batch-size" && parsed >= 0 {
			return 0, sortOptionf(inv, "sort: invalid --batch-size argument %q\nsort: minimum --batch-size argument is '2'", value)
		}
		return 0, sortOptionf(inv, "sort: invalid --%s argument %q", name, value)
	}
	return parsed, nil
}

func collectSortInput(ctx context.Context, inv *Invocation, opts *sortOptions, files []string) (lines []string, checkFile string, exitCode int, err error) {
	inputFiles, err := sortInputFiles(ctx, inv, opts, files)
	if err != nil {
		return nil, "", 0, err
	}

	stdinData := []byte(nil)
	stdinRead := false
	lines = make([]string, 0)
	exitCode = 0
	checkFile = "-"

	if len(inputFiles) == 0 && opts.files0From == "" {
		data, err := readAllStdin(inv)
		if err != nil {
			return nil, "", 0, err
		}
		return decodeSortRecords(data, opts.zeroTerminated), "-", 0, nil
	}

	for idx, file := range inputFiles {
		var data []byte
		switch file {
		case "-":
			if !stdinRead {
				read, err := readAllStdin(inv)
				if err != nil {
					return nil, "", 0, err
				}
				stdinData = read
				stdinRead = true
			}
			data = stdinData
		default:
			read, _, err := readAllFile(ctx, inv, file)
			if err != nil {
				_, _ = fmt.Fprintf(inv.Stderr, "sort: %s: No such file or directory\n", file)
				exitCode = 1
				continue
			}
			data = read
		}
		if idx == 0 {
			checkFile = file
		}
		lines = append(lines, decodeSortRecords(data, opts.zeroTerminated)...)
	}

	return lines, checkFile, exitCode, nil
}

func sortInputFiles(ctx context.Context, inv *Invocation, opts *sortOptions, files []string) ([]string, error) {
	if opts.files0From == "" {
		return files, nil
	}
	if len(files) > 0 {
		return nil, sortOptionf(inv, "sort: extra operand %s\nfile operands cannot be combined with --files0-from\nTry 'sort --help' for more information.", quoteGNUOperand(files[0]))
	}

	var data []byte
	var err error
	source := opts.files0From
	if source == "-" {
		data, err = readAllStdin(inv)
		if err != nil {
			return nil, err
		}
	} else {
		data, _, err = readAllFile(ctx, inv, source)
		if err != nil {
			return nil, sortOptionf(inv, "sort: open failed: %s: No such file or directory", source)
		}
	}
	return parseSortFiles0From(inv, source, data)
}

func parseSortFiles0From(inv *Invocation, source string, data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, sortOptionf(inv, "sort: no input from %s", quoteGNUOperand(source))
	}

	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	files := make([]string, 0, len(parts))
	for i, part := range parts {
		if len(part) == 0 {
			return nil, sortOptionf(inv, "sort: %s:%d: invalid zero-length file name", source, i+1)
		}
		name := string(part)
		if source == "-" && name == "-" {
			return nil, sortOptionf(inv, "sort: when reading file names from standard input, no file name of '-' allowed")
		}
		files = append(files, name)
	}
	if len(files) == 0 {
		return nil, sortOptionf(inv, "sort: no input from %s", quoteGNUOperand(source))
	}
	return files, nil
}

func sortRunCompressProgram(ctx context.Context, inv *Invocation, command string) error {
	result, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:    []string{command},
		WorkDir: inv.Cwd,
		Stdin:   strings.NewReader(""),
	})
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if result == nil || result.ExitCode == 0 {
		return nil
	}
	if err := writeExecutionOutputs(inv, result); err != nil {
		return err
	}
	return exitForExecutionResult(result)
}

func compareSortLines(a, b string, opts *sortOptions) int {
	if opts.randomSort {
		return 0
	}

	lineA := a
	lineB := b
	if opts.ignoreLeadingBlanks {
		lineA = strings.TrimLeftFunc(lineA, unicode.IsSpace)
		lineB = strings.TrimLeftFunc(lineB, unicode.IsSpace)
	}

	if len(opts.keys) == 0 {
		cmp := compareSortValues(lineA, lineB, opts)
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

		keyOpts := *opts
		keyOpts.numeric = key.numeric || opts.numeric
		keyOpts.generalNumeric = key.generalNumeric || opts.generalNumeric
		keyOpts.ignoreCase = key.ignoreCase || opts.ignoreCase
		keyOpts.ignoreNonprinting = key.ignoreNonprinting || opts.ignoreNonprinting
		keyOpts.humanNumeric = key.humanNumeric || opts.humanNumeric
		keyOpts.versionSort = key.versionSort || opts.versionSort
		keyOpts.dictionaryOrder = key.dictionaryOrder || opts.dictionaryOrder
		keyOpts.monthSort = key.monthSort || opts.monthSort

		cmp := compareSortValues(valA, valB, &keyOpts)
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
		return sliceFieldRange(fields[start], key.startChar, key.hasStartChar, 0, false)
	}

	end := key.endField - 1
	if end >= len(fields) {
		end = len(fields) - 1
	}
	if end < start {
		end = start
	}

	if start == end {
		return sliceFieldRange(fields[start], key.startChar, key.hasStartChar, key.endChar, key.hasEndChar)
	}

	joiner := " "
	if delimiter != nil {
		joiner = *delimiter
	}

	parts := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		field := fields[i]
		switch i {
		case start:
			field = sliceFieldRange(field, key.startChar, key.hasStartChar, 0, false)
		case end:
			field = sliceFieldRange(field, 0, false, key.endChar, key.hasEndChar)
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

func uniqueSortedLines(lines []string, opts *sortOptions) []string {
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

func sortLinesEquivalent(a, b string, opts *sortOptions) bool {
	lineA := a
	lineB := b
	if opts.ignoreLeadingBlanks {
		lineA = strings.TrimLeftFunc(lineA, unicode.IsSpace)
		lineB = strings.TrimLeftFunc(lineB, unicode.IsSpace)
	}

	if len(opts.keys) == 0 {
		return compareSortValues(lineA, lineB, opts) == 0
	}

	for _, key := range opts.keys {
		valA := extractSortKeyValue(lineA, key, opts.fieldDelimiter)
		valB := extractSortKeyValue(lineB, key, opts.fieldDelimiter)
		if key.ignoreLeading {
			valA = strings.TrimLeftFunc(valA, unicode.IsSpace)
			valB = strings.TrimLeftFunc(valB, unicode.IsSpace)
		}
		keyOpts := *opts
		keyOpts.numeric = key.numeric || opts.numeric
		keyOpts.generalNumeric = key.generalNumeric || opts.generalNumeric
		keyOpts.ignoreCase = key.ignoreCase || opts.ignoreCase
		keyOpts.ignoreNonprinting = key.ignoreNonprinting || opts.ignoreNonprinting
		keyOpts.humanNumeric = key.humanNumeric || opts.humanNumeric
		keyOpts.versionSort = key.versionSort || opts.versionSort
		keyOpts.dictionaryOrder = key.dictionaryOrder || opts.dictionaryOrder
		keyOpts.monthSort = key.monthSort || opts.monthSort
		if compareSortValues(valA, valB, &keyOpts) != 0 {
			return false
		}
	}
	return true
}

func compareSortValues(a, b string, opts *sortOptions) int {
	valA := a
	valB := b

	if opts.ignoreNonprinting {
		valA = toPrintableOnly(valA)
		valB = toPrintableOnly(valB)
	}
	if opts.dictionaryOrder {
		valA = toDictionaryOrder(valA)
		valB = toDictionaryOrder(valB)
	}
	if opts.ignoreCase {
		valA = strings.ToLower(valA)
		valB = strings.ToLower(valB)
	}
	if opts.monthSort {
		return compareFloat(float64(parseMonth(valA)), float64(parseMonth(valB)))
	}
	if opts.humanNumeric {
		return compareFloat(parseHumanSize(valA), parseHumanSize(valB))
	}
	if opts.versionSort {
		return compareVersions(valA, valB)
	}
	if opts.generalNumeric {
		return compareGeneralNumericValues(valA, valB)
	}
	if opts.numeric {
		return compareNumericValues(valA, valB)
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

func toPrintableOnly(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsPrint(r) {
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
	maxLen := max(len(partsB), len(partsA))

	for i := range maxLen {
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

func compareNumericValues(a, b string) int {
	valA, okA := parseNumericPrefix(a)
	valB, okB := parseNumericPrefix(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	default:
		return compareFloat(valA, valB)
	}
}

func parseNumericPrefix(value string) (float64, bool) {
	match := numericPrefixRe.FindString(strings.TrimLeftFunc(value, unicode.IsSpace))
	if match == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, false
	}
	return number, true
}

func compareGeneralNumericValues(a, b string) int {
	valA := parseGeneralNumericValue(a)
	valB := parseGeneralNumericValue(b)
	if valA.kind != valB.kind {
		if valA.kind < valB.kind {
			return -1
		}
		return 1
	}
	if valA.kind != sortNumberFinite {
		return 0
	}
	return compareFloat(valA.value, valB.value)
}

func parseGeneralNumericValue(value string) sortGeneralNumber {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return sortGeneralNumber{kind: sortNumberInvalid}
	}
	if math.IsNaN(number) {
		return sortGeneralNumber{kind: sortNumberNaN}
	}
	return sortGeneralNumber{kind: sortNumberFinite, value: number}
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

func sliceFieldRange(value string, startChar int, hasStart bool, endChar int, hasEnd bool) string {
	runes := []rune(value)
	start := 0
	if hasStart {
		start = max(startChar-1, 0)
	}
	if start >= len(runes) {
		return ""
	}
	end := len(runes)
	if hasEnd {
		end = min(max(endChar, 0), len(runes))
	}
	if end < start {
		return ""
	}
	return string(runes[start:end])
}

func decodeSortRecords(data []byte, zeroTerminated bool) []string {
	if !zeroTerminated {
		return textLines(data)
	}
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		lines = append(lines, string(part))
	}
	return lines
}

func encodeSortRecords(lines []string, zeroTerminated bool) []byte {
	if len(lines) == 0 {
		return nil
	}
	terminator := "\n"
	if zeroTerminated {
		terminator = "\x00"
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString(terminator)
	}
	return []byte(b.String())
}

func consumeDigits(value string) (digits, remainder string) {
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	return value[:end], value[end:]
}

func parseSortCount(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("empty count")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}
	if parsed > uint64(^uint(0)>>1) {
		return int(^uint(0) >> 1), nil
	}
	return int(parsed), nil
}

func sortOptionf(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 2, format, args...)
}

var numericPrefixRe = regexp.MustCompile(`^[+-]?(?:\d+(?:\.\d*)?|\.\d+)`)
var sortKeyModifierRe = regexp.MustCompile(`([bdfghiMnrRV]+)$`)

const sortHelpText = `sort - sort lines of text files

Usage:
  sort [OPTION]... [FILE]...

Supported options:
  -b, --ignore-leading-blanks
                         ignore leading blanks
  -c, --check           check whether input is sorted
  -C                    like -c, but do not diagnose first disorder
  -d, --dictionary-order
                         consider only blanks and alphanumeric characters
  --debug               annotate the part of the line used to sort, and warn
  -f, --ignore-case     fold lower case to upper case characters
  --files0-from=F       read input file names from NUL-terminated file F
  -g, --general-numeric-sort
                         compare according to general numeric value
  -h, --human-numeric-sort
                         compare human-readable numbers
  -i, --ignore-nonprinting
                         consider only printable characters
  -k, --key=KEYDEF      sort via a key definition
  -m, --merge           merge already sorted files
  -M, --month-sort      compare month names
  -n, --numeric-sort    compare according to numeric value
  -o, --output=FILE     write result to FILE instead of stdout
  --parallel=N          change the number of sorts run concurrently
  -R, --random-sort     sort by random hash of keys
  -r, --reverse         reverse the result of comparisons
  -s, --stable          disable last-resort whole-line comparison
  --sort=WORD           sort according to WORD: general-numeric, human-numeric,
                         month, numeric, random, or version
  -t, --field-separator=SEP
                         use SEP instead of whitespace for field separation
  -u, --unique          output only the first of equal lines
  -V, --version-sort    natural sort of version numbers
  --version             output version information and exit
  -z, --zero-terminated line delimiter is NUL, not newline
  --help                show this help text
`

const sortVersionText = "sort (jbgo) dev\n"

var _ Command = (*Sort)(nil)
