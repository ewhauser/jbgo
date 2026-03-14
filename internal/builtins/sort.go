package builtins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	gosort "sort"
	"strconv"
	"strings"
	"unicode"

	gbfs "github.com/ewhauser/gbash/fs"
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
	return RunCommand(ctx, c, inv)
}

func (c *Sort) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}
	args := normalizeSortLegacyArgs(inv.Args)
	if slicesEqual(args, inv.Args) {
		return inv
	}
	clone := *inv
	clone.Args = args
	return &clone
}

func (c *Sort) Spec() CommandSpec {
	return CommandSpec{
		Name:  "sort",
		About: "sort lines of text files",
		Usage: "sort [OPTION]... [FILE]...",
		HelpRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, sortHelpText)
			return err
		},
		VersionRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, sortVersionText)
			return err
		},
		Options: []OptionSpec{
			{Name: "help", Long: "help", Help: "show this help text"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
			{Name: "debug", Long: "debug", Help: "annotate the part of the line used to sort, and warn"},
			{Name: "reverse", Short: 'r', Long: "reverse", Help: "reverse the result of comparisons"},
			{Name: "numeric-sort", Short: 'n', Long: "numeric-sort", Help: "compare according to numeric value"},
			{Name: "general-numeric-sort", Short: 'g', Long: "general-numeric-sort", Help: "compare according to general numeric value"},
			{Name: "random-sort", Short: 'R', Long: "random-sort", Help: "sort by random hash of keys"},
			{Name: "unique", Short: 'u', Long: "unique", Help: "output only the first of equal lines"},
			{Name: "ignore-case", Short: 'f', Long: "ignore-case", Help: "fold lower case to upper case characters"},
			{Name: "ignore-nonprinting", Short: 'i', Long: "ignore-nonprinting", Help: "consider only printable characters"},
			{Name: "human-numeric-sort", Short: 'h', Long: "human-numeric-sort", Help: "compare human-readable numbers"},
			{Name: "version-sort", Short: 'V', Long: "version-sort", Help: "natural sort of version numbers"},
			{Name: "dictionary-order", Short: 'd', Long: "dictionary-order", Help: "consider only blanks and alphanumeric characters"},
			{Name: "month-sort", Short: 'M', Long: "month-sort", Help: "compare month names"},
			{Name: "ignore-leading-blanks", Short: 'b', Long: "ignore-leading-blanks", Help: "ignore leading blanks"},
			{Name: "stable", Short: 's', Long: "stable", Help: "disable last-resort whole-line comparison"},
			{Name: "merge", Short: 'm', Long: "merge", Help: "merge already sorted files"},
			{Name: "check", Short: 'c', Long: "check", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "MODE", Help: "check whether input is sorted"},
			{Name: "check-silent", Short: 'C', Long: "check-silent", Help: "like -c, but do not diagnose first disorder"},
			{Name: "zero-terminated", Short: 'z', Long: "zero-terminated", Help: "line delimiter is NUL, not newline"},
			{Name: "output", Short: 'o', Long: "output", Arity: OptionRequiredValue, ValueName: "FILE", Help: "write result to FILE instead of stdout"},
			{Name: "field-separator", Short: 't', Long: "field-separator", Arity: OptionRequiredValue, ValueName: "SEP", Help: "use SEP instead of whitespace for field separation"},
			{Name: "key", Short: 'k', Long: "key", Arity: OptionRequiredValue, ValueName: "KEYDEF", Repeatable: true, Help: "sort via a key definition"},
			{Name: "sort", Long: "sort", Arity: OptionRequiredValue, ValueName: "WORD", Help: "sort according to WORD"},
			{Name: "parallel", Long: "parallel", Arity: OptionRequiredValue, ValueName: "N", Help: "change the number of sorts run concurrently"},
			{Name: "batch-size", Long: "batch-size", Arity: OptionRequiredValue, ValueName: "NMERGE", Help: "merge at most NMERGE inputs at once"},
			{Name: "buffer-size", Short: 'S', Long: "buffer-size", Arity: OptionRequiredValue, ValueName: "SIZE", Help: "use SIZE for main memory buffer"},
			{Name: "temporary-directory", Short: 'T', Long: "temporary-directory", Arity: OptionRequiredValue, ValueName: "DIR", Repeatable: true, Help: "use DIR for temporaries, not $TMPDIR or /tmp"},
			{Name: "compress-program", Long: "compress-program", Arity: OptionRequiredValue, ValueName: "PROG", Help: "compress temporaries with PROG; decompress them with PROG -d"},
			{Name: "files0-from", Long: "files0-from", Arity: OptionRequiredValue, ValueName: "F", Help: "read input file names from NUL-terminated file F"},
			{Name: "random-source", Long: "random-source", Arity: OptionRequiredValue, ValueName: "FILE", Help: "get random bytes from FILE"},
			{Name: "legacy-key", Long: "legacy-key", Arity: OptionRequiredValue, ValueName: "SPEC", Repeatable: true, Hidden: true},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
		},
	}
}

func (c *Sort) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, files, err := parseSortMatches(inv, matches)
	if err != nil {
		return err
	}
	if matches.Has("help") {
		spec := c.Spec()
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	if matches.Has("version") {
		spec := c.Spec()
		return RenderCommandVersion(inv.Stdout, &spec)
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
		targetAbs := gbfs.Resolve(inv.Cwd, opts.outputFile)
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

func parseSortMatches(inv *Invocation, matches *ParsedCommand) (sortOptions, []string, error) {
	var opts sortOptions
	if matches == nil {
		return opts, nil, nil
	}

	opts.help = matches.Has("help")
	opts.showVersion = matches.Has("version")
	opts.debug = matches.Has("debug")
	opts.reverse = matches.Has("reverse")
	opts.numeric = matches.Has("numeric-sort")
	opts.generalNumeric = matches.Has("general-numeric-sort")
	opts.randomSort = matches.Has("random-sort")
	opts.unique = matches.Has("unique")
	opts.ignoreCase = matches.Has("ignore-case")
	opts.ignoreNonprinting = matches.Has("ignore-nonprinting")
	opts.humanNumeric = matches.Has("human-numeric-sort")
	opts.versionSort = matches.Has("version-sort")
	opts.dictionaryOrder = matches.Has("dictionary-order")
	opts.monthSort = matches.Has("month-sort")
	opts.ignoreLeadingBlanks = matches.Has("ignore-leading-blanks")
	opts.stable = matches.Has("stable")
	opts.merge = matches.Has("merge")
	opts.zeroTerminated = matches.Has("zero-terminated")
	opts.checkOnly = matches.Has("check") || matches.Has("check-silent")
	opts.checkQuiet = matches.Has("check-silent")

	if matches.Has("output") {
		opts.outputFile = matches.Value("output")
	}
	if matches.Has("field-separator") {
		delim := matches.Value("field-separator")
		opts.fieldDelimiter = &delim
	}
	if matches.Has("buffer-size") {
		opts.bufferSize = matches.Value("buffer-size")
	}
	if matches.Has("compress-program") {
		opts.compressProgram = matches.Value("compress-program")
	}
	if matches.Has("files0-from") {
		opts.files0From = matches.Value("files0-from")
	}
	if matches.Has("random-source") {
		opts.randomSource = matches.Value("random-source")
	}
	if matches.Has("parallel") {
		value, err := parseSortPositiveInt(inv, "parallel", matches.Value("parallel"), 1)
		if err != nil {
			return sortOptions{}, nil, err
		}
		opts.parallel = value
	}
	if matches.Has("batch-size") {
		value, err := parseSortPositiveInt(inv, "batch-size", matches.Value("batch-size"), 2)
		if err != nil {
			return sortOptions{}, nil, err
		}
		opts.batchSize = value
		opts.batchSizeSet = true
	}
	opts.tempDirs = append(opts.tempDirs, matches.Values("temporary-directory")...)
	for _, value := range matches.Values("key") {
		if err := appendSortKey(&opts.keys, value); err != nil {
			return sortOptions{}, nil, sortOptionf(inv, "sort: invalid field specification %q", value)
		}
	}
	for _, value := range matches.Values("legacy-key") {
		start, end, _ := strings.Cut(value, "\x00")
		key, err := parseLegacySortKey(start, end)
		if err != nil {
			return sortOptions{}, nil, sortOptionf(inv, "sort: invalid field specification %q", start)
		}
		opts.keys = append(opts.keys, key)
	}
	if matches.Has("sort") {
		if err := applySortMode(&opts, matches.Value("sort"), inv); err != nil {
			return sortOptions{}, nil, err
		}
	}
	for _, mode := range matches.Values("check") {
		switch mode {
		case "", "diagnose-first":
			opts.checkQuiet = false
		case "quiet", "silent":
			opts.checkQuiet = true
		default:
			return sortOptions{}, nil, sortOptionf(inv, "sort: invalid argument %q for --check", mode)
		}
	}

	return opts, matches.Args("file"), nil
}

func normalizeSortLegacyArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	parsingOptions := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !parsingOptions {
			out = append(out, arg)
			continue
		}
		if arg == "--" {
			parsingOptions = false
			out = append(out, arg)
			continue
		}
		if strings.HasPrefix(arg, "+") && len(arg) > 1 {
			end := ""
			if i+1 < len(args) && isLegacySortEndArg(args[i+1]) {
				end = args[i+1]
				i++
			}
			out = append(out, "--legacy-key="+arg+"\x00"+end)
			continue
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			out = append(out, arg)
			continue
		}
		out = append(out, arg)
	}
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
		data, err := readAllStdin(ctx, inv)
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
				read, err := readAllStdin(ctx, inv)
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
		data, err = readAllStdin(ctx, inv)
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

const sortVersionText = "sort (gbash) dev\n"

var _ Command = (*Sort)(nil)
