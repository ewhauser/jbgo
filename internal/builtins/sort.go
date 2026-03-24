package builtins

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	gosort "sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Sort struct{}

type sortOptions struct {
	reverse             bool
	numeric             bool
	generalNumeric      bool
	randomSort          bool
	randomSalt          []byte
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

type sortInputRun struct {
	name  string
	lines []string
}

type sortFieldInfo struct {
	text  string
	start int
	end   int
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
			InferLongOptions:         true,
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
	if opts.checkOnly && len(files) > 1 {
		return sortOptionf(inv, "sort: extra operand %s not allowed with -c", quoteGNUOperand(files[1]))
	}

	runs, checkFile, exitCode, err := collectSortInputs(ctx, inv, &opts, files)
	if err != nil {
		return err
	}

	if opts.debug {
		sortWriteDebugWarnings(inv.Stderr, &opts)
	}

	lines := flattenSortRuns(runs)
	if opts.randomSort && len(lines) > 0 {
		if _, err := sortRandomSalt(ctx, inv, &opts); err != nil {
			return err
		}
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

	switch {
	case opts.randomSort:
		lines, err = randomizeSortLines(ctx, inv, lines, &opts)
		if err != nil {
			return err
		}
	case opts.merge:
		lines = mergeSortRuns(runs, &opts)
	default:
		gosort.SliceStable(lines, func(i, j int) bool {
			return compareSortLines(lines[i], lines[j], &opts) < 0
		})
	}

	if opts.unique {
		lines = uniqueSortedLines(lines, &opts)
	}

	output := encodeSortRecords(lines, opts.zeroTerminated)
	if opts.debug {
		output = renderSortDebugOutput(lines, &opts)
	}
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
	if outputs := matches.Values("output"); len(outputs) > 1 {
		first := outputs[0]
		for _, value := range outputs[1:] {
			if value != first {
				return sortOptions{}, nil, sortOptionf(inv, "sort: multiple output files specified")
			}
		}
	}

	if matches.Has("output") {
		opts.outputFile = matches.Value("output")
	}
	if matches.Has("field-separator") {
		delim := matches.Value("field-separator")
		if delim == "\\0" {
			delim = "\x00"
		}
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
	if matches.Has("check") && sortHasExplicitEmptyOptionalValue(inv.Args, "check", 'c') {
		return sortOptions{}, nil, sortOptionf(inv, "sort: invalid argument %q for --check", "")
	}
	checkDiagnose := matches.Count("check") > len(matches.Values("check"))
	checkQuiet := matches.Has("check-silent")
	for _, mode := range matches.Values("check") {
		quiet, err := parseSortCheckMode(inv, mode)
		if err != nil {
			return sortOptions{}, nil, err
		}
		if quiet {
			checkQuiet = true
			continue
		}
		checkDiagnose = true
	}
	if checkDiagnose && checkQuiet {
		return sortOptions{}, nil, sortOptionf(inv, "sort: options '-cC' are incompatible")
	}
	opts.checkOnly = checkDiagnose || checkQuiet
	opts.checkQuiet = checkQuiet

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
	if opts.checkOnly && opts.outputFile != "" {
		if opts.checkQuiet {
			return sortOptionf(inv, "sort: options '-Co' are incompatible")
		}
		return sortOptionf(inv, "sort: options '-co' are incompatible")
	}
	if err := validateSortOrderingOptions(inv, sortModeFlagsFromOptions(opts), opts.dictionaryOrder, opts.ignoreNonprinting); err != nil {
		return err
	}
	for _, key := range opts.keys {
		if err := validateSortOrderingOptions(inv, sortModeFlagsFromKey(key), key.dictionaryOrder, key.ignoreNonprinting); err != nil {
			return err
		}
	}
	if opts.bufferSize != "" {
		if err := validateSortBufferSize(inv, opts.bufferSize); err != nil {
			return err
		}
	}
	if opts.fieldDelimiter != nil {
		delim := *opts.fieldDelimiter
		switch runeCount := utf8.RuneCountInString(delim); {
		case runeCount == 0:
			return sortOptionf(inv, "sort: empty tab")
		case runeCount > 1:
			return sortOptionf(inv, "sort: multi-character tab %s", quoteGNUOperand(delim))
		}
	}
	return nil
}

type sortModeFlags struct {
	numeric        bool
	generalNumeric bool
	humanNumeric   bool
	month          bool
	version        bool
	random         bool
}

func sortModeFlagsFromOptions(opts *sortOptions) sortModeFlags {
	return sortModeFlags{
		numeric:        opts.numeric,
		generalNumeric: opts.generalNumeric,
		humanNumeric:   opts.humanNumeric,
		month:          opts.monthSort,
		version:        opts.versionSort,
		random:         opts.randomSort,
	}
}

func sortModeFlagsFromKey(key sortKey) sortModeFlags {
	return sortModeFlags{
		numeric:        key.numeric,
		generalNumeric: key.generalNumeric,
		humanNumeric:   key.humanNumeric,
		month:          key.monthSort,
		version:        key.versionSort,
	}
}

func validateSortOrderingOptions(inv *Invocation, flags sortModeFlags, dictionaryOrder, ignoreNonprinting bool) error {
	modeCount := 0
	for _, enabled := range []bool{flags.numeric, flags.generalNumeric, flags.humanNumeric, flags.month} {
		if enabled {
			modeCount++
		}
	}
	if modeCount > 1 {
		return sortOptionf(inv, "sort: options '-%s' are incompatible", sortOrderingFlagsString(flags, dictionaryOrder, ignoreNonprinting))
	}
	if modeCount == 1 && (flags.version || flags.random || dictionaryOrder || ignoreNonprinting) {
		return sortOptionf(inv, "sort: options '-%s' are incompatible", sortOrderingFlagsString(flags, dictionaryOrder, ignoreNonprinting))
	}
	return nil
}

func sortOrderingFlagsString(flags sortModeFlags, dictionaryOrder, ignoreNonprinting bool) string {
	var b strings.Builder
	if dictionaryOrder {
		b.WriteByte('d')
	}
	if flags.generalNumeric {
		b.WriteByte('g')
	}
	if flags.humanNumeric {
		b.WriteByte('h')
	}
	if !dictionaryOrder && ignoreNonprinting {
		b.WriteByte('i')
	}
	if flags.month {
		b.WriteByte('M')
	}
	if flags.numeric {
		b.WriteByte('n')
	}
	if flags.random {
		b.WriteByte('R')
	}
	if flags.version {
		b.WriteByte('V')
	}
	return b.String()
}

func applySortMode(opts *sortOptions, value string, inv *Invocation) error {
	switch sortCanonicalModeName(value) {
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

func parseSortCheckMode(inv *Invocation, value string) (bool, error) {
	switch {
	case sortHasAbbrev("diagnose-first", value):
		return false, nil
	case sortHasAbbrev("silent", value):
		return true, nil
	case sortHasAbbrev("quiet", value):
		return true, nil
	default:
		return false, sortOptionf(inv, "sort: invalid argument %q for --check", value)
	}
}

func sortCanonicalModeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch {
	case sortHasAbbrev("general-numeric", value):
		return "general-numeric"
	case sortHasAbbrev("human-numeric", value):
		return "human-numeric"
	case sortHasAbbrev("month", value):
		return "month"
	case sortHasAbbrev("numeric", value):
		return "numeric"
	case sortHasAbbrev("random", value):
		return "random"
	case sortHasAbbrev("version", value):
		return "version"
	default:
		return ""
	}
}

func sortHasAbbrev(candidate, value string) bool {
	return value != "" && len(value) <= len(candidate) && candidate[:len(value)] == value
}

func sortHasExplicitEmptyOptionalValue(args []string, long string, short rune) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if name, value, ok := strings.Cut(strings.TrimPrefix(arg, "--"), "="); strings.HasPrefix(arg, "--") && ok {
			if value == "" && sortHasAbbrev(long, name) {
				return true
			}
			continue
		}
		if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") || arg == "-" {
			continue
		}
		shorts := arg[1:]
		for i := 0; i < len(shorts); i++ {
			if rune(shorts[i]) != short {
				continue
			}
			remaining := shorts[i+1:]
			if remaining == "=" {
				return true
			}
		}
	}
	return false
}

func parseSortPositiveInt(inv *Invocation, name, value string, minimum int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, sortOptionf(inv, "sort: invalid --%s argument %q", name, value)
	}
	if parsed < minimum {
		if name == "parallel" && parsed == 0 {
			return 0, sortOptionf(inv, "sort: number in parallel must be nonzero")
		}
		if name == "batch-size" && parsed >= 0 {
			return 0, sortOptionf(inv, "sort: invalid --batch-size argument %q\nsort: minimum --batch-size argument is '2'", value)
		}
		return 0, sortOptionf(inv, "sort: invalid --%s argument %q", name, value)
	}
	return parsed, nil
}

func validateSortBufferSize(inv *Invocation, value string) error {
	match := sortBufferSizeRe.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return sortOptionf(inv, "sort: invalid -S argument %q", value)
	}
	if match[2] == "%" {
		return nil
	}
	count, ok := new(big.Int).SetString(match[1], 10)
	if !ok {
		return sortOptionf(inv, "sort: invalid -S argument %q", value)
	}
	factor := sortBufferMultiplier(match[2])
	if factor.Sign() == 0 {
		return sortOptionf(inv, "sort: invalid -S argument %q", value)
	}
	count.Mul(count, factor)
	if !count.IsUint64() {
		return sortOptionf(inv, "sort: -S argument %q too large", value)
	}
	return nil
}

func sortBufferMultiplier(suffix string) *big.Int {
	if suffix == "" {
		suffix = "K"
	}
	switch suffix {
	case "%", "b":
		return big.NewInt(1)
	case "k", "K":
		return big.NewInt(1 << 10)
	case "m", "M":
		return big.NewInt(1 << 20)
	case "g", "G":
		return big.NewInt(1 << 30)
	case "t", "T":
		return big.NewInt(1 << 40)
	case "P":
		return big.NewInt(1 << 50)
	case "E":
		return big.NewInt(1 << 60)
	case "Z":
		return new(big.Int).Lsh(big.NewInt(1), 70)
	case "Y":
		return new(big.Int).Lsh(big.NewInt(1), 80)
	case "R":
		return new(big.Int).Lsh(big.NewInt(1), 90)
	case "Q":
		return new(big.Int).Lsh(big.NewInt(1), 100)
	default:
		return big.NewInt(0)
	}
}

func collectSortInputs(ctx context.Context, inv *Invocation, opts *sortOptions, files []string) (runs []sortInputRun, checkFile string, exitCode int, err error) {
	inputFiles, err := sortInputFiles(ctx, inv, opts, files)
	if err != nil {
		return nil, "", 0, err
	}

	stdinData := []byte(nil)
	stdinRead := false
	runs = make([]sortInputRun, 0)
	exitCode = 0
	checkFile = "-"

	if len(inputFiles) == 0 && opts.files0From == "" {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return nil, "", 0, err
		}
		return []sortInputRun{{name: "-", lines: decodeSortRecords(data, opts.zeroTerminated)}}, "-", 0, nil
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
				_, _ = fmt.Fprintf(inv.Stderr, "sort: %s: %s\n", file, readAllErrorText(err))
				exitCode = 2
				continue
			}
			data = read
		}
		if idx == 0 {
			checkFile = file
		}
		runs = append(runs, sortInputRun{name: file, lines: decodeSortRecords(data, opts.zeroTerminated)})
	}

	return runs, checkFile, exitCode, nil
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
			return nil, sortOptionf(inv, "sort: open failed: %s: %s", source, readAllErrorText(err))
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

func flattenSortRuns(runs []sortInputRun) []string {
	total := 0
	for _, run := range runs {
		total += len(run.lines)
	}
	lines := make([]string, 0, total)
	for _, run := range runs {
		lines = append(lines, run.lines...)
	}
	return lines
}

func mergeSortRuns(runs []sortInputRun, opts *sortOptions) []string {
	total := 0
	indexes := make([]int, len(runs))
	for _, run := range runs {
		total += len(run.lines)
	}
	out := make([]string, 0, total)
	for {
		bestRun := -1
		bestLine := ""
		for i, run := range runs {
			if indexes[i] >= len(run.lines) {
				continue
			}
			line := run.lines[indexes[i]]
			if bestRun == -1 {
				bestRun = i
				bestLine = line
				continue
			}
			cmp := compareSortLines(line, bestLine, opts)
			if cmp < 0 || (cmp == 0 && i < bestRun) {
				bestRun = i
				bestLine = line
			}
		}
		if bestRun == -1 {
			break
		}
		out = append(out, bestLine)
		indexes[bestRun]++
	}
	return out
}

func randomizeSortLines(ctx context.Context, inv *Invocation, lines []string, opts *sortOptions) ([]string, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	salt, err := sortRandomSalt(ctx, inv, opts)
	if err != nil {
		return nil, err
	}
	groups := sortEquivalentGroups(lines, opts)
	gosort.SliceStable(groups, func(i, j int) bool {
		cmp := bytes.Compare(sortGroupRandomHash(groups[i], salt, opts), sortGroupRandomHash(groups[j], salt, opts))
		if opts.reverse {
			return cmp > 0
		}
		return cmp < 0
	})
	out := make([]string, 0, len(lines))
	for _, group := range groups {
		out = append(out, group...)
	}
	return out, nil
}

func sortRandomSalt(ctx context.Context, inv *Invocation, opts *sortOptions) ([]byte, error) {
	if len(opts.randomSalt) != 0 {
		return opts.randomSalt, nil
	}
	if opts.randomSource == "" {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, &ExitError{Code: 1, Err: err}
		}
		opts.randomSalt = salt
		return salt, nil
	}
	data, _, err := readAllFile(ctx, inv, opts.randomSource)
	if err != nil {
		return nil, sortOptionf(inv, "sort: open failed: %s: %s", opts.randomSource, readAllErrorText(err))
	}
	if len(data) < 16 {
		return nil, sortOptionf(inv, "sort: %s: end of file", quoteGNUOperand(opts.randomSource))
	}
	if len(data) > sortRandomSourceMaxBytes {
		data = data[:sortRandomSourceMaxBytes]
	}
	sum := sha256.Sum256(data)
	opts.randomSalt = sum[:16]
	return opts.randomSalt, nil
}

func sortEquivalentGroups(lines []string, opts *sortOptions) [][]string {
	normalized := append([]string(nil), lines...)
	groupOpts := *opts
	groupOpts.randomSort = false
	gosort.SliceStable(normalized, func(i, j int) bool {
		return compareSortLines(normalized[i], normalized[j], &groupOpts) < 0
	})
	groups := make([][]string, 0)
	for _, line := range normalized {
		if len(groups) == 0 || !sortLinesEquivalent(groups[len(groups)-1][0], line, &groupOpts) {
			groups = append(groups, []string{line})
			continue
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], line)
	}
	return groups
}

func sortGroupRandomHash(group []string, salt []byte, opts *sortOptions) []byte {
	if len(group) == 0 {
		return nil
	}
	payload := sortRandomHashPayload(group[0], opts)
	sum := sha256.Sum256(append(append([]byte{}, salt...), payload...))
	return sum[:]
}

func sortRandomHashPayload(line string, opts *sortOptions) []byte {
	if opts.ignoreLeadingBlanks && len(opts.keys) == 0 {
		line = strings.TrimLeftFunc(line, unicode.IsSpace)
	}
	if len(opts.keys) == 0 {
		return sortCanonicalValueBytes(line, opts)
	}

	var payload bytes.Buffer
	for _, key := range opts.keys {
		value := extractSortKeyValue(line, key, opts.fieldDelimiter, key.ignoreLeading || opts.ignoreLeadingBlanks)
		keyOpts := sortKeyComparisonOptions(opts, key)
		chunk := sortCanonicalValueBytes(value, &keyOpts)
		payload.WriteString(strconv.Itoa(len(chunk)))
		payload.WriteByte(':')
		payload.Write(chunk)
		payload.WriteByte(0)
	}
	return payload.Bytes()
}

func sortCanonicalValueBytes(value string, opts *sortOptions) []byte {
	normalized := value
	if opts.ignoreNonprinting {
		normalized = toPrintableOnly(normalized)
	}
	if opts.dictionaryOrder {
		normalized = toDictionaryOrder(normalized)
	}
	if opts.ignoreCase {
		normalized = strings.ToLower(normalized)
	}

	switch {
	case opts.monthSort:
		return []byte("month:" + sortCanonicalMonthText(parseMonth(normalized)))
	case opts.humanNumeric:
		return []byte("human:" + sortCanonicalHumanNumericText(normalized))
	case opts.versionSort:
		return []byte("version:" + sortCanonicalVersionText(normalized))
	case opts.generalNumeric:
		return []byte("general:" + sortCanonicalGeneralNumberText(normalized))
	case opts.numeric:
		return []byte("numeric:" + sortCanonicalNumericText(normalized))
	default:
		return []byte("text:" + normalized)
	}
}

func sortCanonicalMonthText(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func sortCanonicalFloatText(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "+inf"
	case math.IsInf(value, -1):
		return "-inf"
	case value == 0:
		return "0"
	default:
		return strconv.FormatFloat(value, 'g', -1, 64)
	}
}

func sortCanonicalGeneralNumberText(value string) string {
	number := parseGeneralNumericValue(value)
	switch number.kind {
	case sortNumberInvalid:
		return "invalid"
	case sortNumberNaN:
		return "nan"
	default:
		return sortCanonicalFloatText(number.value)
	}
}

func sortCanonicalNumericText(value string) string {
	return sortCanonicalParsedNumericText(parseSortNumericValue(value))
}

func sortCanonicalVersionText(value string) string {
	parts := versionChunkRe.FindAllString(value, -1)
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if numeric, err := strconv.Atoi(part); err == nil {
			b.WriteByte('n')
			b.WriteString(strconv.Itoa(numeric))
		} else {
			b.WriteByte('s')
			b.WriteString(part)
		}
		b.WriteByte(0)
	}
	return b.String()
}

func compareSortLines(a, b string, opts *sortOptions) int {
	if opts.randomSort {
		if sortLinesEquivalent(a, b, opts) {
			nonRandomOpts := *opts
			nonRandomOpts.randomSort = false
			return compareSortLines(a, b, &nonRandomOpts)
		}
		return 0
	}

	return compareNonRandomSortLines(a, b, opts)
}

func compareNonRandomSortLines(a, b string, opts *sortOptions) int {
	lineA := a
	lineB := b
	if opts.ignoreLeadingBlanks && len(opts.keys) == 0 {
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
		valA := extractSortKeyValue(lineA, key, opts.fieldDelimiter, key.ignoreLeading || opts.ignoreLeadingBlanks)
		valB := extractSortKeyValue(lineB, key, opts.fieldDelimiter, key.ignoreLeading || opts.ignoreLeadingBlanks)

		keyOpts := sortKeyComparisonOptions(opts, key)

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

func extractSortKeyValue(line string, key sortKey, delimiter *string, ignoreLeading bool) string {
	start, end, ok := sortKeyRange(line, key, delimiter, ignoreLeading)
	if !ok {
		return ""
	}
	return sortSliceRunes(line, start, end)
}

func sortFieldInfos(line string, delimiter *string) []sortFieldInfo {
	runes := []rune(line)
	if delimiter == nil {
		fields := []sortFieldInfo{{start: 0}}
		previousWhitespace := true
		for i, r := range runes {
			if unicode.IsSpace(r) {
				if !previousWhitespace {
					fields[len(fields)-1].end = i
					fields = append(fields, sortFieldInfo{start: i})
				}
				previousWhitespace = true
				continue
			}
			previousWhitespace = false
		}
		fields[len(fields)-1].end = len(runes)
		for i := range fields {
			fields[i].text = string(runes[fields[i].start:fields[i].end])
		}
		return fields
	}
	delim, _ := utf8.DecodeRuneInString(*delimiter)
	fields := make([]sortFieldInfo, 0)
	start := 0
	for i, r := range runes {
		if r != delim {
			continue
		}
		fields = append(fields, sortFieldInfo{text: string(runes[start:i]), start: start, end: i})
		start = i + 1
	}
	if start < len(runes) {
		fields = append(fields, sortFieldInfo{text: string(runes[start:]), start: start, end: len(runes)})
	}
	return fields
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
	if opts.ignoreLeadingBlanks && len(opts.keys) == 0 {
		lineA = strings.TrimLeftFunc(lineA, unicode.IsSpace)
		lineB = strings.TrimLeftFunc(lineB, unicode.IsSpace)
	}

	if len(opts.keys) == 0 {
		return compareSortValues(lineA, lineB, opts) == 0
	}

	for _, key := range opts.keys {
		valA := extractSortKeyValue(lineA, key, opts.fieldDelimiter, key.ignoreLeading || opts.ignoreLeadingBlanks)
		valB := extractSortKeyValue(lineB, key, opts.fieldDelimiter, key.ignoreLeading || opts.ignoreLeadingBlanks)
		keyOpts := sortKeyComparisonOptions(opts, key)
		if compareSortValues(valA, valB, &keyOpts) != 0 {
			return false
		}
	}
	return true
}

func sortKeyComparisonOptions(opts *sortOptions, key sortKey) sortOptions {
	keyOpts := *opts
	keyOpts.randomSort = false
	keyOpts.reverse = false
	keyOpts.numeric = key.numeric || opts.numeric
	keyOpts.generalNumeric = key.generalNumeric || opts.generalNumeric
	keyOpts.ignoreCase = key.ignoreCase || opts.ignoreCase
	keyOpts.ignoreNonprinting = key.ignoreNonprinting || opts.ignoreNonprinting
	keyOpts.humanNumeric = key.humanNumeric || opts.humanNumeric
	keyOpts.versionSort = key.versionSort || opts.versionSort
	keyOpts.dictionaryOrder = key.dictionaryOrder || opts.dictionaryOrder
	keyOpts.monthSort = key.monthSort || opts.monthSort
	return keyOpts
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
		return compareHumanNumericValues(valA, valB)
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
		if r <= unicode.MaxASCII && unicode.IsPrint(r) {
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
	return compareParsedNumericValues(parseSortNumericValue(a), parseSortNumericValue(b))
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

type sortNumericValue struct {
	sign     int
	exponent int
	digits   string
}

type sortHumanNumericValue struct {
	number   sortNumericValue
	unitRank int
}

func parseSortNumericValue(value string) sortNumericValue {
	number, _ := parseSortNumericValueRemainder(value)
	return number
}

func parseSortNumericValueRemainder(value string) (sortNumericValue, string) {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return sortNumericValue{sign: 1}, ""
	}

	sign := 1
	if trimmed[0] == '-' {
		sign = -1
		trimmed = trimmed[1:]
	}

	exponent := -1
	hadDecimal := false
	hadDigit := false
	var digits strings.Builder
	consumed := 0
	for consumed < len(trimmed) {
		ch := trimmed[consumed]
		switch {
		case ch >= '1' && ch <= '9':
			hadDigit = true
			if !hadDecimal {
				exponent++
			}
			digits.WriteByte(ch)
			consumed++
		case ch == '0':
			hadDigit = true
			if digits.Len() == 0 {
				if hadDecimal {
					exponent--
				}
			} else {
				if !hadDecimal {
					exponent++
				}
				digits.WriteByte(ch)
			}
			consumed++
		case ch == '.' && !hadDecimal:
			hadDecimal = true
			consumed++
		default:
			if digits.Len() == 0 || !hadDigit {
				return sortNumericValue{sign: 1}, trimmed[consumed:]
			}
			return sortNumericValue{sign: sign, exponent: exponent, digits: digits.String()}, trimmed[consumed:]
		}
	}

	if digits.Len() == 0 || !hadDigit {
		return sortNumericValue{sign: 1}, ""
	}
	return sortNumericValue{sign: sign, exponent: exponent, digits: digits.String()}, ""
}

func parseSortHumanNumericValue(value string) sortHumanNumericValue {
	number, remainder := parseSortNumericValueRemainder(value)
	unitRank := 0
	if number.digits != "" && remainder != "" {
		unitRank = sortHumanUnitRank(remainder[0])
	}
	return sortHumanNumericValue{number: number, unitRank: unitRank}
}

func sortHumanUnitRank(ch byte) int {
	switch ch {
	case 'k', 'K':
		return 1
	case 'M':
		return 2
	case 'G':
		return 3
	case 'T':
		return 4
	case 'P':
		return 5
	case 'E':
		return 6
	case 'Z':
		return 7
	case 'Y':
		return 8
	case 'R':
		return 9
	case 'Q':
		return 10
	default:
		return 0
	}
}

func compareHumanNumericValues(a, b string) int {
	valA := parseSortHumanNumericValue(a)
	valB := parseSortHumanNumericValue(b)
	if valA.number.sign != valB.number.sign {
		if valA.number.sign < valB.number.sign {
			return -1
		}
		return 1
	}
	if valA.unitRank != valB.unitRank {
		if valA.number.sign < 0 {
			if valA.unitRank < valB.unitRank {
				return 1
			}
			return -1
		}
		if valA.unitRank < valB.unitRank {
			return -1
		}
		return 1
	}
	return compareParsedNumericValues(valA.number, valB.number)
}

func compareParsedNumericValues(a, b sortNumericValue) int {
	if a.sign != b.sign {
		if a.sign < b.sign {
			return -1
		}
		return 1
	}
	if a.digits != "" && b.digits != "" && a.exponent != b.exponent {
		if a.exponent < b.exponent {
			if a.sign < 0 {
				return 1
			}
			return -1
		}
		if a.sign < 0 {
			return -1
		}
		return 1
	}
	cmp := sortCompareDigitStrings(a.digits, b.digits)
	if a.sign < 0 {
		return -cmp
	}
	return cmp
}

func sortCompareDigitStrings(a, b string) int {
	for i := 0; ; i++ {
		switch {
		case i >= len(a) && i >= len(b):
			return 0
		case i >= len(a):
			if sortRemainingDigitsAllZero(b[i:]) {
				return 0
			}
			return -1
		case i >= len(b):
			if sortRemainingDigitsAllZero(a[i:]) {
				return 0
			}
			return 1
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
}

func sortRemainingDigitsAllZero(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '0' {
			return false
		}
	}
	return true
}

func sortCanonicalHumanNumericText(value string) string {
	parsed := parseSortHumanNumericValue(value)
	if parsed.number.digits == "" {
		return "0"
	}
	return fmt.Sprintf("%d:%s", parsed.unitRank, sortCanonicalParsedNumericText(parsed.number))
}

func sortCanonicalParsedNumericText(value sortNumericValue) string {
	if value.digits == "" {
		return "0"
	}
	digits := strings.TrimRight(value.digits, "0")
	if digits == "" {
		return "0"
	}
	return fmt.Sprintf("%d:%d:%s", value.sign, value.exponent, digits)
}

type sortKeyPositionKind int

const (
	sortKeyPositionStart sortKeyPositionKind = iota + 1
	sortKeyPositionEnd
	sortKeyPositionTooLow
	sortKeyPositionTooHigh
)

type sortKeyPosition struct {
	kind sortKeyPositionKind
	pos  int
}

func sortKeyRange(line string, key sortKey, delimiter *string, ignoreLeading bool) (int, int, bool) {
	runes := []rune(line)
	fields := sortFieldInfos(line, delimiter)

	start := sortResolveKeyPosition(runes, fields, key.startField, sortKeyStartChar(key), ignoreLeading)
	switch start.kind {
	case sortKeyPositionTooHigh:
		return len(runes), len(runes), true
	case sortKeyPositionStart:
	default:
		return 0, 0, false
	}

	if !key.hasEndField {
		return start.pos, len(runes), true
	}

	end := sortResolveKeyPosition(runes, fields, key.endField, sortKeyEndChar(key), ignoreLeading)
	switch end.kind {
	case sortKeyPositionStart:
		if end.pos < len(runes) {
			end.pos++
		}
	case sortKeyPositionEnd:
	case sortKeyPositionTooHigh:
		end.pos = len(runes)
	default:
		return 0, 0, false
	}
	if end.pos < start.pos {
		end.pos = start.pos
	}
	return start.pos, end.pos, true
}

func sortResolveKeyPosition(runes []rune, fields []sortFieldInfo, field, char int, ignoreLeading bool) sortKeyPosition {
	if field < 1 {
		return sortKeyPosition{kind: sortKeyPositionTooLow}
	}
	if field > len(fields) {
		return sortKeyPosition{kind: sortKeyPositionTooHigh}
	}
	if char == 0 {
		end := fields[field-1].end
		if end == 0 {
			return sortKeyPosition{kind: sortKeyPositionTooLow}
		}
		return sortKeyPosition{kind: sortKeyPositionEnd, pos: end}
	}

	pos := fields[field-1].start
	if ignoreLeading {
		for pos < len(runes) && unicode.IsSpace(runes[pos]) {
			pos++
		}
	}
	pos += min(char-1, len(runes)-pos)
	if pos >= len(runes) {
		return sortKeyPosition{kind: sortKeyPositionTooHigh}
	}
	return sortKeyPosition{kind: sortKeyPositionStart, pos: pos}
}

func sortKeyStartChar(key sortKey) int {
	if key.hasStartChar {
		return key.startChar
	}
	return 1
}

func sortKeyEndChar(key sortKey) int {
	if key.hasEndChar {
		return key.endChar
	}
	return 0
}

func sortSliceRunes(line string, start, end int) string {
	runes := []rune(line)
	start = min(max(start, 0), len(runes))
	end = min(max(end, 0), len(runes))
	end = max(end, start)
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

func renderSortDebugOutput(lines []string, opts *sortOptions) []byte {
	if len(lines) == 0 {
		return nil
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
		b.WriteString(sortDebugKeyUnderline(line, opts))
		b.WriteByte('\n')
		if !opts.stable {
			b.WriteString(sortDebugWholeLineUnderline(line))
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

func sortDebugKeyUnderline(line string, opts *sortOptions) string {
	runes := []rune(line)
	if len(opts.keys) == 0 {
		start := 0
		if opts.ignoreLeadingBlanks {
			for start < len(runes) && unicode.IsSpace(runes[start]) {
				start++
			}
		}
		if start >= len(runes) {
			return "^ no match for key"
		}
		marks := make([]rune, len(runes))
		for i := range marks {
			marks[i] = ' '
		}
		for i := start; i < len(runes); i++ {
			marks[i] = '_'
		}
		return string(marks)
	}

	keyLine, offset := sortDebugWorkingLine(line, opts)
	marks := make([]rune, len([]rune(line)))
	for i := range marks {
		marks[i] = ' '
	}
	matched := false
	for _, key := range opts.keys {
		start, end, ok := sortDebugKeySpan(keyLine, key, opts.fieldDelimiter, key.ignoreLeading || opts.ignoreLeadingBlanks)
		if !ok {
			continue
		}
		for i := start + offset; i < end+offset && i < len(marks); i++ {
			if i >= 0 {
				marks[i] = '_'
				matched = true
			}
		}
	}
	if !matched {
		return "^ no match for key"
	}
	return string(marks)
}

func sortDebugWholeLineUnderline(line string) string {
	if line == "" {
		return "^ no match for key"
	}
	return strings.Repeat("_", utf8.RuneCountInString(line))
}

func sortDebugWorkingLine(line string, opts *sortOptions) (string, int) {
	if !opts.ignoreLeadingBlanks || len(opts.keys) > 0 {
		return line, 0
	}
	runes := []rune(line)
	offset := 0
	for offset < len(runes) && unicode.IsSpace(runes[offset]) {
		offset++
	}
	return string(runes[offset:]), offset
}

func sortDebugKeySpan(line string, key sortKey, delimiter *string, ignoreLeading bool) (int, int, bool) {
	start, end, ok := sortKeyRange(line, key, delimiter, ignoreLeading)
	if !ok || start == end {
		return 0, 0, false
	}
	return start, end, true
}

func sortWriteDebugWarnings(w io.Writer, opts *sortOptions) {
	_, _ = fmt.Fprintln(w, "sort: text ordering performed using simple byte comparison")
	if sortUsesNumericDebug(opts) {
		_, _ = fmt.Fprintln(w, "sort: numbers use '.' as a decimal point in this locale")
	}
	if warning := sortLeadingBlanksDebugWarning(opts); warning != "" {
		_, _ = fmt.Fprintln(w, warning)
	}
}

func sortUsesNumericDebug(opts *sortOptions) bool {
	if opts.numeric || opts.generalNumeric || opts.humanNumeric {
		return true
	}
	for _, key := range opts.keys {
		if key.numeric || key.generalNumeric || key.humanNumeric {
			return true
		}
	}
	return false
}

func sortLeadingBlanksDebugWarning(opts *sortOptions) string {
	if opts.fieldDelimiter != nil || opts.ignoreLeadingBlanks {
		return ""
	}
	for i, key := range opts.keys {
		if key.startField > 1 && !key.ignoreLeading {
			return fmt.Sprintf("sort: leading blanks are significant in key %d; consider also specifying 'b'", i+1)
		}
	}
	return ""
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

var sortKeyModifierRe = regexp.MustCompile(`([bdfghiMnrRV]+)$`)
var sortBufferSizeRe = regexp.MustCompile(`^(\d+)([%A-Za-z]?)$`)

const sortRandomSourceMaxBytes = 1024 * 1024

const sortHelpText = `Usage: sort [OPTION]... [FILE]...
  or:  sort [OPTION]... --files0-from=F
Write sorted concatenation of all FILE(s) to standard output.

With no FILE, or when FILE is -, read standard input.

Mandatory arguments to long options are mandatory for short options too.
Ordering options:

  -b, --ignore-leading-blanks  ignore leading blanks
  -d, --dictionary-order      consider only blanks and alphanumeric characters
  -f, --ignore-case           fold lower case to upper case characters
  -g, --general-numeric-sort  compare according to general numerical value
  -i, --ignore-nonprinting    consider only printable characters
  -M, --month-sort            compare (unknown) < 'JAN' < ... < 'DEC'
  -h, --human-numeric-sort    compare human readable numbers (e.g., 2K 1G)
  -n, --numeric-sort          compare according to string numerical value
  -R, --random-sort           shuffle, but group identical keys
      --random-source=FILE    get random bytes from FILE
  -r, --reverse               reverse the result of comparisons
      --sort=WORD             sort according to WORD:
                                general-numeric -g, human-numeric -h, month -M,
                                numeric -n, random -R, version -V
  -V, --version-sort          natural sort of (version) numbers within text

Other options:

      --batch-size=NMERGE     merge at most NMERGE inputs at once
  -c, --check, --check=diagnose-first
                              check for sorted input; do not sort
  -C, --check=quiet, --check=silent
                              like -c, but do not report first bad line
      --compress-program=PROG
                              compress temporaries with PROG; decompress with PROG -d
      --debug                 annotate the part of the line used to sort, and warn
      --files0-from=F         read input from the files specified by NUL-terminated names in file F
  -k, --key=KEYDEF            sort via a key; KEYDEF gives location and type
  -m, --merge                 merge already sorted files; do not sort
  -o, --output=FILE           write result to FILE instead of standard output
  -s, --stable                stabilize sort by disabling last-resort comparison
  -S, --buffer-size=SIZE      use SIZE for main memory buffer
  -t, --field-separator=SEP   use SEP instead of non-blank to blank transition
  -T, --temporary-directory=DIR
                              use DIR for temporaries, not $TMPDIR or /tmp
      --parallel=N            change the number of sorts run concurrently to N
  -u, --unique                output only the first of lines with equal keys
  -z, --zero-terminated       line delimiter is NUL, not newline
      --help                  display this help and exit
      --version               output version information and exit

KEYDEF is F[.C][OPTS][,F[.C][OPTS]] for start and stop position, where F is a
field number and C a character position in the field. OPTS is one or more
single-letter ordering options [bdfgiMhnRrV].
`

const sortVersionText = "sort (gbash) dev\n"

var _ Command = (*Sort)(nil)
