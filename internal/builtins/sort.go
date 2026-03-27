package builtins

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"math/big"
	"os"
	"path"
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
	startField         int
	startChar          int
	hasStartChar       bool
	endField           int
	hasEndField        bool
	endChar            int
	hasEndChar         bool
	numeric            bool
	generalNumeric     bool
	reverse            bool
	ignoreCase         bool
	ignoreNonprinting  bool
	startIgnoreLeading bool
	endIgnoreLeading   bool
	humanNumeric       bool
	versionSort        bool
	dictionaryOrder    bool
	monthSort          bool
}

type sortGeneralNumber struct {
	kind    int
	value   *big.Float
	text    string
	nanSign int
}

type sortInputRun struct {
	name  string
	lines []string
}

const (
	sortNumberInvalid = iota
	sortNumberNaN
	sortNumberFinite
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
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	if opts.outputFile != "" {
		targetAbs := gbfs.Resolve(inv.Cwd, opts.outputFile)
		if err := sortWriteOutputFile(ctx, inv, opts.outputFile, targetAbs, output); err != nil {
			return err
		}
		return nil
	}

	if _, err := inv.Stdout.Write(output); err != nil {
		return &ExitError{Code: 1, Err: err}
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
			return sortOptions{}, nil, sortKeySpecError(inv, value)
		}
	}
	for _, value := range matches.Values("legacy-key") {
		start, end, _ := strings.Cut(value, "\x00")
		key, err := parseLegacySortKey(start, end)
		if err != nil {
			return sortOptions{}, nil, sortOptionf(inv, "sort: invalid field specification %s", quoteGNUOperand(start))
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

type sortBlankType int

const (
	sortBlankNone sortBlankType = iota
	sortBlankStart
	sortBlankEnd
)

type sortKeyPart struct {
	field     int
	char      int
	hasChar   bool
	modifiers string
}

func parseLegacySortKey(start, end string) (sortKey, error) {
	startPart, err := parseLegacySortPoint(strings.TrimPrefix(start, "+"))
	if err != nil {
		return sortKey{}, err
	}
	key := sortKey{
		startField:   startPart.field + 1,
		startChar:    startPart.char + 1,
		hasStartChar: startPart.hasChar,
	}
	if !key.hasStartChar {
		key.startChar = 1
	}
	if err := applySortKeyModifiers(&key, startPart.modifiers, sortBlankStart); err != nil {
		return sortKey{}, err
	}
	if end != "" {
		endPart, err := parseLegacySortPoint(strings.TrimPrefix(end, "-"))
		if err != nil {
			return sortKey{}, err
		}
		key.hasEndField = true
		if endPart.hasChar && endPart.char > 0 {
			key.endField = endPart.field + 1
			key.endChar = endPart.char
			key.hasEndChar = true
		} else {
			key.endField = endPart.field
			key.endChar = 0
			key.hasEndChar = endPart.hasChar
		}
		if err := applySortKeyModifiers(&key, endPart.modifiers, sortBlankEnd); err != nil {
			return sortKey{}, err
		}
	}
	return key, nil
}

func parseLegacySortPoint(spec string) (sortKeyPart, error) {
	fieldText, rest := consumeDigits(spec)
	if fieldText == "" {
		return sortKeyPart{}, fmt.Errorf("missing field")
	}
	fieldValue, err := parseSortCount(fieldText)
	if err != nil {
		return sortKeyPart{}, err
	}

	part := sortKeyPart{field: fieldValue}
	if strings.HasPrefix(rest, ".") {
		charText, more := consumeDigits(rest[1:])
		if charText == "" {
			part.hasChar = true
			part.char = 0
		} else {
			charValue, err := parseSortCount(charText)
			if err != nil {
				return sortKeyPart{}, err
			}
			part.hasChar = true
			part.char = charValue
		}
		rest = more
	}
	if rest != "" && !validSortKeyModifiers(rest) {
		return sortKeyPart{}, fmt.Errorf("invalid modifier")
	}
	part.modifiers = rest
	return part, nil
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

	if strings.Count(spec, ",") > 1 {
		return sortKey{}, fmt.Errorf("too many separators")
	}
	startSpec := spec
	endSpec := ""
	if before, after, ok := strings.Cut(spec, ","); ok {
		startSpec = before
		endSpec = after
		if endSpec == "" {
			return sortKey{}, fmt.Errorf("missing end field")
		}
	}
	if startSpec == "" {
		return sortKey{}, fmt.Errorf("missing start field")
	}

	startPart, err := parseSortFieldPart(startSpec, false)
	if err != nil {
		return sortKey{}, err
	}
	key.startField = startPart.field
	key.startChar = startPart.char
	key.hasStartChar = startPart.hasChar
	if err := applySortKeyModifiers(&key, startPart.modifiers, sortBlankStart); err != nil {
		return sortKey{}, err
	}

	if endSpec != "" {
		endPart, err := parseSortFieldPart(endSpec, true)
		if err != nil {
			return sortKey{}, err
		}
		key.hasEndField = true
		key.endField = endPart.field
		key.endChar = endPart.char
		key.hasEndChar = endPart.hasChar
		if err := applySortKeyModifiers(&key, endPart.modifiers, sortBlankEnd); err != nil {
			return sortKey{}, err
		}
	}
	return key, nil
}

func validSortKeyModifiers(value string) bool {
	for _, flag := range value {
		switch flag {
		case 'b', 'd', 'f', 'g', 'h', 'i', 'M', 'n', 'r', 'R', 'V':
		default:
			return false
		}
	}
	return true
}

func applySortKeyModifiers(key *sortKey, modifiers string, blankType sortBlankType) error {
	for _, flag := range modifiers {
		switch flag {
		case 'b':
			switch blankType {
			case sortBlankStart:
				key.startIgnoreLeading = true
			case sortBlankEnd:
				key.endIgnoreLeading = true
			}
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
		default:
			return fmt.Errorf("invalid modifier")
		}
	}
	return nil
}

func parseSortFieldPart(spec string, allowZeroChar bool) (sortKeyPart, error) {
	fieldText, rest := consumeDigits(spec)
	if fieldText == "" {
		return sortKeyPart{}, fmt.Errorf("invalid field")
	}
	fieldValue, err := parseSortCount(fieldText)
	if err != nil || fieldValue < 1 {
		return sortKeyPart{}, fmt.Errorf("invalid field")
	}

	part := sortKeyPart{field: fieldValue}
	if strings.HasPrefix(rest, ".") {
		charText, more := consumeDigits(rest[1:])
		if charText == "" {
			return sortKeyPart{}, fmt.Errorf("invalid character position")
		}
		charValue, err := parseSortCount(charText)
		if err != nil || charValue < 0 || (!allowZeroChar && charValue < 1) {
			return sortKeyPart{}, fmt.Errorf("invalid character position")
		}
		part.char = charValue
		part.hasChar = true
		rest = more
	}
	if rest != "" && !validSortKeyModifiers(rest) {
		return sortKeyPart{}, fmt.Errorf("invalid field")
	}
	part.modifiers = rest
	return part, nil
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

func sortKeySpecError(inv *Invocation, value string) error {
	switch {
	case value == "0":
		return sortOptionf(inv, "sort: -: invalid field specification %s", quoteGNUOperand(value))
	case value == "1.0":
		return sortOptionf(inv, "sort: character offset is zero: invalid field specification %s", quoteGNUOperand(value))
	case strings.HasSuffix(value, ","):
		return sortOptionf(inv, "sort: invalid number after ',': invalid count at start of %s", quoteGNUOperand(""))
	case strings.Contains(value, ",-k"):
		_, after, _ := strings.Cut(value, ",")
		return sortOptionf(inv, "sort: invalid number after ',': invalid count at start of %s", quoteGNUOperand(after))
	case strings.Contains(value, ".,"):
		idx := strings.Index(value, ".,")
		return sortOptionf(inv, "sort: invalid number after '.': invalid count at start of %s", quoteGNUOperand(value[idx+1:]))
	default:
		return sortOptionf(inv, "sort: invalid field specification %s", quoteGNUOperand(value))
	}
}

func validateSortOptions(inv *Invocation, opts *sortOptions) error {
	if opts.checkOnly && opts.outputFile != "" {
		if opts.checkQuiet {
			return sortOptionf(inv, "sort: options '-Co' are incompatible")
		}
		return sortOptionf(inv, "sort: options '-co' are incompatible")
	}
	if len(opts.keys) == 0 {
		if err := validateSortOrderingOptions(inv, sortModeFlagsFromOptions(opts), opts.dictionaryOrder, opts.ignoreNonprinting, opts.ignoreCase); err != nil {
			return err
		}
	}
	for _, key := range opts.keys {
		if err := validateSortOrderingOptions(inv, sortModeFlagsFromKey(key), key.dictionaryOrder, key.ignoreNonprinting, key.ignoreCase); err != nil {
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

func validateSortOrderingOptions(inv *Invocation, flags sortModeFlags, dictionaryOrder, ignoreNonprinting, ignoreCase bool) error {
	modeCount := 0
	for _, enabled := range []bool{flags.numeric, flags.generalNumeric, flags.humanNumeric, flags.month} {
		if enabled {
			modeCount++
		}
	}
	if modeCount > 1 {
		return sortOptionf(inv, "sort: options '-%s' are incompatible", sortOrderingFlagsString(flags, dictionaryOrder, ignoreNonprinting, ignoreCase))
	}
	if modeCount == 1 && (flags.version || flags.random || dictionaryOrder || ignoreNonprinting) {
		return sortOptionf(inv, "sort: options '-%s' are incompatible", sortOrderingFlagsString(flags, dictionaryOrder, ignoreNonprinting, ignoreCase))
	}
	return nil
}

func sortOrderingFlagsString(flags sortModeFlags, dictionaryOrder, ignoreNonprinting, ignoreCase bool) string {
	var b strings.Builder
	if dictionaryOrder {
		b.WriteByte('d')
	}
	if ignoreCase {
		b.WriteByte('f')
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
	parsedBig, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return 0, sortOptionf(inv, "sort: invalid --%s argument %s", name, quoteGNUOperand(value))
	}
	if parsedBig.Sign() < 0 {
		return 0, sortOptionf(inv, "sort: invalid --%s argument %s", name, quoteGNUOperand(value))
	}
	if !parsedBig.IsInt64() || parsedBig.Int64() > int64(^uint(0)>>1) {
		if name == "batch-size" {
			return 0, sortOptionf(inv, "sort: --batch-size argument %s too large\nsort: maximum --batch-size argument with current rlimit is", quoteGNUOperand(value))
		}
		return 0, sortOptionf(inv, "sort: invalid --%s argument %s", name, quoteGNUOperand(value))
	}
	parsed := int(parsedBig.Int64())
	if parsed < minimum {
		if name == "parallel" && parsed == 0 {
			return 0, sortOptionf(inv, "sort: number in parallel must be nonzero")
		}
		if name == "batch-size" && parsed >= 0 {
			return 0, sortOptionf(inv, "sort: invalid --batch-size argument %s\nsort: minimum --batch-size argument is '2'", quoteGNUOperand(value))
		}
		return 0, sortOptionf(inv, "sort: invalid --%s argument %s", name, quoteGNUOperand(value))
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
	if err := sortValidateMergeResources(inv, opts, len(inputFiles)); err != nil {
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
				_, _ = fmt.Fprintf(inv.Stderr, "sort: cannot read: %s: %s\n", file, readAllErrorText(err))
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

func sortWriteOutputFile(ctx context.Context, inv *Invocation, rawTarget, targetAbs string, output []byte) error {
	if err := sortValidateOutputTarget(ctx, inv, rawTarget, targetAbs); err != nil {
		return err
	}
	if err := writeFileContents(ctx, inv, targetAbs, output, 0o644); err != nil {
		return sortOutputOpenError(inv, rawTarget, err)
	}
	return nil
}

func sortValidateOutputTarget(ctx context.Context, inv *Invocation, rawTarget, targetAbs string) error {
	parent := path.Dir(targetAbs)
	info, _, exists, err := statMaybe(ctx, inv, parent)
	if err != nil {
		return sortOutputOpenError(inv, rawTarget, err)
	}
	if !exists {
		return sortOptionf(inv, "sort: open failed: %s: No such file or directory", rawTarget)
	}
	if !info.IsDir() {
		return sortOptionf(inv, "sort: open failed: %s: Not a directory", rawTarget)
	}
	file, err := inv.FS.OpenFile(ctx, targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return sortOutputOpenError(inv, rawTarget, err)
	}
	return file.Close()
}

func sortOutputOpenError(inv *Invocation, name string, err error) error {
	return sortOptionf(inv, "sort: open failed: %s: %s", name, readAllErrorText(err))
}

func sortValidateMergeResources(inv *Invocation, opts *sortOptions, inputCount int) error {
	if !opts.merge || !opts.batchSizeSet || inputCount <= opts.batchSize {
		return nil
	}
	if len(opts.tempDirs) == 0 {
		return nil
	}
	return sortOptionf(inv, "sort: cannot create temporary file in %s:", quoteGNUOperand(opts.tempDirs[0]))
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
		line = strings.TrimLeftFunc(line, sortIsBlankRune)
	}
	if len(opts.keys) == 0 {
		return sortCanonicalValueBytes(line, opts)
	}

	var payload bytes.Buffer
	for _, key := range opts.keys {
		value := extractSortKeyValue(line, key, opts.fieldDelimiter, sortKeyPositions(opts, key))
		keyOpts := sortKeyComparisonOptions(opts, key)
		chunk := sortCanonicalValueBytes(value, &keyOpts)
		payload.WriteString(strconv.Itoa(len(chunk)))
		payload.WriteByte(':')
		payload.Write(chunk)
		payload.WriteByte(0)
	}
	return payload.Bytes()
}

func sortKeyUsesDefaultCompare(key sortKey) bool {
	return !key.ignoreCase &&
		!key.ignoreNonprinting &&
		!key.startIgnoreLeading &&
		!key.endIgnoreLeading &&
		!key.numeric &&
		!key.generalNumeric &&
		!key.humanNumeric &&
		!key.versionSort &&
		!key.dictionaryOrder &&
		!key.monthSort
}

type sortKeyPositionOptions struct {
	startIgnoreLeading bool
	endIgnoreLeading   bool
}

func sortInheritedKeyPositionOptions(opts *sortOptions, key sortKey) sortKeyPositionOptions {
	if sortKeyUsesDefaultCompare(key) && !key.reverse {
		return sortKeyPositionOptions{
			startIgnoreLeading: opts.ignoreLeadingBlanks,
			endIgnoreLeading:   opts.ignoreLeadingBlanks,
		}
	}
	return sortKeyPositionOptions{}
}

func sortKeyPositions(opts *sortOptions, key sortKey) sortKeyPositionOptions {
	base := sortInheritedKeyPositionOptions(opts, key)
	base.startIgnoreLeading = base.startIgnoreLeading || key.startIgnoreLeading
	base.endIgnoreLeading = base.endIgnoreLeading || key.endIgnoreLeading
	return base
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
		normalized = strings.ToUpper(normalized)
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

func sortCanonicalGeneralNumberText(value string) string {
	number := parseGeneralNumericValue(value)
	switch number.kind {
	case sortNumberInvalid:
		return "invalid"
	case sortNumberNaN:
		return "nan"
	default:
		return number.text
	}
}

func sortCanonicalNumericText(value string) string {
	return sortCanonicalParsedNumericText(parseSortNumericValue(value))
}

func sortCanonicalVersionText(value string) string {
	return value
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
		lineA = strings.TrimLeftFunc(lineA, sortIsBlankRune)
		lineB = strings.TrimLeftFunc(lineB, sortIsBlankRune)
	}

	if len(opts.keys) == 0 {
		cmp := compareSortValues(lineA, lineB, opts)
		if cmp != 0 {
			if opts.reverse {
				return -cmp
			}
			return cmp
		}
		if !opts.stable && !opts.unique {
			tiebreaker := strings.Compare(a, b)
			if opts.reverse {
				return -tiebreaker
			}
			return tiebreaker
		}
		return 0
	}

	for _, key := range opts.keys {
		valA := extractSortKeyValue(lineA, key, opts.fieldDelimiter, sortKeyPositions(opts, key))
		valB := extractSortKeyValue(lineB, key, opts.fieldDelimiter, sortKeyPositions(opts, key))

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

	if !opts.stable && !opts.unique {
		tiebreaker := strings.Compare(a, b)
		if opts.reverse {
			return -tiebreaker
		}
		return tiebreaker
	}
	return 0
}

func extractSortKeyValue(line string, key sortKey, delimiter *string, positions sortKeyPositionOptions) string {
	start, end := sortKeyRange(line, key, delimiter, positions)
	return sortSliceRunes(line, start, end)
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
		lineA = strings.TrimLeftFunc(lineA, sortIsBlankRune)
		lineB = strings.TrimLeftFunc(lineB, sortIsBlankRune)
	}

	if len(opts.keys) == 0 {
		return compareSortValues(lineA, lineB, opts) == 0
	}

	for _, key := range opts.keys {
		valA := extractSortKeyValue(lineA, key, opts.fieldDelimiter, sortKeyPositions(opts, key))
		valB := extractSortKeyValue(lineB, key, opts.fieldDelimiter, sortKeyPositions(opts, key))
		keyOpts := sortKeyComparisonOptions(opts, key)
		if compareSortValues(valA, valB, &keyOpts) != 0 {
			return false
		}
	}
	return true
}

func sortKeyComparisonOptions(opts *sortOptions, key sortKey) sortOptions {
	keyOpts := sortOptions{
		stable: opts.stable,
		unique: opts.unique,
	}
	if sortKeyUsesDefaultCompare(key) && !key.reverse {
		keyOpts = *opts
	}
	keyOpts.randomSort = false
	keyOpts.reverse = false
	keyOpts.ignoreLeadingBlanks = false
	keyOpts.numeric = keyOpts.numeric || key.numeric
	keyOpts.generalNumeric = keyOpts.generalNumeric || key.generalNumeric
	keyOpts.ignoreCase = keyOpts.ignoreCase || key.ignoreCase
	keyOpts.ignoreNonprinting = keyOpts.ignoreNonprinting || key.ignoreNonprinting
	keyOpts.humanNumeric = keyOpts.humanNumeric || key.humanNumeric
	keyOpts.versionSort = keyOpts.versionSort || key.versionSort
	keyOpts.dictionaryOrder = keyOpts.dictionaryOrder || key.dictionaryOrder
	keyOpts.monthSort = keyOpts.monthSort || key.monthSort
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
		valA = strings.ToUpper(valA)
		valB = strings.ToUpper(valB)
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
		case sortIsBlankRune(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sortIsBlankRune(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
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
	trimmed := sortNormalizeMonthText(value)
	if trimmed == "" {
		return 0
	}
	if numText, ok := strings.CutSuffix(trimmed, "月"); ok {
		if monthNumber, err := strconv.Atoi(numText); err == nil && monthNumber >= 1 && monthNumber <= 12 {
			return monthNumber
		}
	}
	switch {
	case strings.HasPrefix(trimmed, "jan"):
		return 1
	case strings.HasPrefix(trimmed, "feb"), strings.HasPrefix(trimmed, "fev"):
		return 2
	case strings.HasPrefix(trimmed, "mar"):
		return 3
	case strings.HasPrefix(trimmed, "apr"), strings.HasPrefix(trimmed, "avr"):
		return 4
	case strings.HasPrefix(trimmed, "may"), strings.HasPrefix(trimmed, "mai"):
		return 5
	case strings.HasPrefix(trimmed, "jun"), strings.HasPrefix(trimmed, "juin"):
		return 6
	case strings.HasPrefix(trimmed, "jul"), strings.HasPrefix(trimmed, "juil"):
		return 7
	case strings.HasPrefix(trimmed, "aug"), strings.HasPrefix(trimmed, "aou"):
		return 8
	case strings.HasPrefix(trimmed, "sep"):
		return 9
	case strings.HasPrefix(trimmed, "oct"):
		return 10
	case strings.HasPrefix(trimmed, "nov"):
		return 11
	case strings.HasPrefix(trimmed, "dec"):
		return 12
	default:
		return 0
	}
}

func sortNormalizeMonthText(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	var b strings.Builder
	for trimmed != "" {
		r, size := utf8.DecodeRuneInString(trimmed)
		if r == utf8.RuneError && size == 1 && trimmed[0] >= 0x80 {
			if mapped, ok := sortFoldMonthLatin1Byte(trimmed[0]); ok {
				b.WriteByte(mapped)
			}
			trimmed = trimmed[1:]
			continue
		}
		trimmed = trimmed[size:]
		if r == '.' {
			continue
		}
		if r == '月' {
			b.WriteRune(r)
			continue
		}
		r = unicode.ToLower(r)
		if mapped, ok := sortFoldMonthRune(r); ok {
			b.WriteByte(mapped)
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}

	normalized := b.String()
	if len(normalized) > 4 && !strings.HasSuffix(normalized, "月") {
		normalized = normalized[:4]
	}
	return normalized
}

func sortFoldMonthRune(r rune) (byte, bool) {
	switch r {
	case 'à', 'â', 'ä':
		return 'a', true
	case 'ç':
		return 'c', true
	case 'è', 'é', 'ê', 'ë':
		return 'e', true
	case 'ì', 'í', 'î', 'ï':
		return 'i', true
	case 'ò', 'ó', 'ô', 'ö':
		return 'o', true
	case 'ù', 'ú', 'û', 'ü':
		return 'u', true
	}
	return 0, false
}

func sortFoldMonthLatin1Byte(b byte) (byte, bool) {
	switch b {
	case 0xc0, 0xc2, 0xc4, 0xe0, 0xe2, 0xe4:
		return 'a', true
	case 0xc7, 0xe7:
		return 'c', true
	case 0xc8, 0xc9, 0xca, 0xcb, 0xe8, 0xe9, 0xea, 0xeb:
		return 'e', true
	case 0xcc, 0xcd, 0xce, 0xcf, 0xec, 0xed, 0xee, 0xef:
		return 'i', true
	case 0xd2, 0xd3, 0xd4, 0xd6, 0xf2, 0xf3, 0xf4, 0xf6:
		return 'o', true
	case 0xd9, 0xda, 0xdb, 0xdc, 0xf9, 0xfa, 0xfb, 0xfc:
		return 'u', true
	}
	return 0, false
}

func sortNormalizeGeneralNumericPrefix(prefix string) string {
	trimmed := strings.TrimSpace(prefix)
	sign := ""
	if trimmed != "" && (trimmed[0] == '+' || trimmed[0] == '-') {
		sign = trimmed[:1]
		trimmed = trimmed[1:]
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "infinity"), strings.HasPrefix(lower, "inf"):
		return sign + "inf"
	case strings.HasPrefix(lower, "nan"):
		return sign + "nan"
	}
	if strings.Contains(trimmed, ",") && !strings.Contains(trimmed, ".") {
		trimmed = strings.Replace(trimmed, ",", ".", 1)
	}
	return sign + trimmed
}

func sortGeneralNumericPrefixSign(prefix string) int {
	trimmed := strings.TrimSpace(prefix)
	switch {
	case strings.HasPrefix(trimmed, "-"):
		return -1
	case strings.HasPrefix(trimmed, "+"):
		return 1
	default:
		return 0
	}
}

func sortUnsignedGeneralNumericPrefix(prefix string) string {
	trimmed := strings.ToLower(strings.TrimSpace(prefix))
	if trimmed != "" && (trimmed[0] == '+' || trimmed[0] == '-') {
		return trimmed[1:]
	}
	return trimmed
}

func sortGeneralNumericPrefix(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return ""
	}
	i := 0
	if trimmed[i] == '+' || trimmed[i] == '-' {
		i++
	}
	lower := strings.ToLower(trimmed[i:])
	switch {
	case strings.HasPrefix(lower, "infinity"):
		return trimmed[:i+len("infinity")]
	case strings.HasPrefix(lower, "inf"):
		return trimmed[:i+len("inf")]
	case strings.HasPrefix(lower, "nan"):
		return trimmed[:i+len("nan")]
	}

	start := i
	sawDigit := false
	sawDecimal := false
	for i < len(trimmed) {
		switch ch := trimmed[i]; {
		case ch >= '0' && ch <= '9':
			sawDigit = true
			i++
		case (ch == '.' || ch == ',') && !sawDecimal:
			sawDecimal = true
			i++
		default:
			goto exponent
		}
	}

exponent:
	if !sawDigit {
		return ""
	}
	if i < len(trimmed) && (trimmed[i] == 'e' || trimmed[i] == 'E') {
		j := i + 1
		if j < len(trimmed) && (trimmed[j] == '+' || trimmed[j] == '-') {
			j++
		}
		k := j
		for k < len(trimmed) && trimmed[k] >= '0' && trimmed[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	if i == start {
		return ""
	}
	return trimmed[:i]
}

func sortHumanNumericPrefixAndRemainder(value string) (string, string) {
	data := []byte(value)
	i := 0
	for i < len(data) {
		width, group := sortHumanBlankWidth(data, i)
		if width == 0 {
			break
		}
		_ = group
		i += width
	}
	useCommaDecimal := bytes.IndexByte(data[i:], ',') >= 0 && bytes.IndexByte(data[i:], '.') < 0
	var b strings.Builder
	if i < len(data) && (data[i] == '+' || data[i] == '-') {
		b.WriteByte(data[i])
		i++
	}
	sawDigit := false
	sawDecimal := false
	for i < len(data) {
		ch := data[i]
		switch {
		case ch >= '0' && ch <= '9':
			sawDigit = true
			b.WriteByte(ch)
			i++
		case (ch == '.' || ch == ',') && !sawDecimal:
			sawDecimal = true
			if useCommaDecimal && ch == ',' {
				b.WriteByte('.')
			} else {
				b.WriteByte(ch)
			}
			i++
		case sortHasHumanBlankAt(data, i) && sawDigit:
			width, groupSep := sortHumanBlankWidth(data, i)
			if groupSep {
				next := i + width
				if next < len(data) && data[next] >= '0' && data[next] <= '9' {
					i = next
					continue
				}
				goto done
			}
			j := i
			for j < len(data) {
				nextWidth, _ := sortHumanBlankWidth(data, j)
				if nextWidth == 0 {
					break
				}
				j += nextWidth
			}
			i = j
			goto done
		default:
			goto done
		}
	}
done:
	return b.String(), string(data[i:])
}

func sortHumanBlankWidth(data []byte, i int) (width int, groupSeparator bool) {
	if i >= len(data) {
		return 0, false
	}
	switch data[i] {
	case ' ':
		return 1, true
	case '\t', '\r', '\n', '\f', '\v':
		return 1, false
	case 0xa0:
		return 1, true
	}
	if bytes.HasPrefix(data[i:], []byte{0xc2, 0xa0}) {
		return 2, true
	}
	if bytes.HasPrefix(data[i:], []byte{0xe2, 0x80, 0xaf}) {
		return 3, true
	}
	return 0, false
}

func sortHasHumanBlankAt(data []byte, i int) bool {
	width, _ := sortHumanBlankWidth(data, i)
	return width != 0
}

func sortVersionPrefixLen(s string) int {
	if s == "" {
		return 0
	}
	prefixLen := 0
	for i := 0; ; {
		if i == len(s) {
			return prefixLen
		}
		i++
		prefixLen = i
		for i+1 < len(s) && s[i] == '.' && (sortIsAlpha(s[i+1]) || s[i+1] == '~') {
			i += 2
			for i < len(s) && (sortIsAlphaNum(s[i]) || s[i] == '~') {
				i++
			}
		}
	}
}

func sortVersionOrder(s string, pos, limit int) int {
	if pos == limit {
		return -1
	}
	c := s[pos]
	switch {
	case c >= '0' && c <= '9':
		return 0
	case sortIsAlpha(c):
		return int(c)
	case c == '~':
		return -2
	default:
		return int(c) + 256
	}
}

func sortVersionRevCompare(a string, aLen int, b string, bLen int) int {
	aPos := 0
	bPos := 0
	for aPos < aLen || bPos < bLen {
		firstDiff := 0
		for (aPos < aLen && !sortIsDigit(a[aPos])) || (bPos < bLen && !sortIsDigit(b[bPos])) {
			aOrd := sortVersionOrder(a, aPos, aLen)
			bOrd := sortVersionOrder(b, bPos, bLen)
			if aOrd != bOrd {
				return aOrd - bOrd
			}
			if aPos < aLen {
				aPos++
			}
			if bPos < bLen {
				bPos++
			}
		}
		for aPos < aLen && a[aPos] == '0' {
			aPos++
		}
		for bPos < bLen && b[bPos] == '0' {
			bPos++
		}
		for aPos < aLen && bPos < bLen && sortIsDigit(a[aPos]) && sortIsDigit(b[bPos]) {
			if firstDiff == 0 {
				firstDiff = int(a[aPos]) - int(b[bPos])
			}
			aPos++
			bPos++
		}
		if aPos < aLen && sortIsDigit(a[aPos]) {
			return 1
		}
		if bPos < bLen && sortIsDigit(b[bPos]) {
			return -1
		}
		if firstDiff != 0 {
			return firstDiff
		}
	}
	return 0
}

func sortFileVersionCompare(a, b string) int {
	if a == "" || b == "" {
		switch a {
		case b:
			return 0
		case "":
			return -1
		default:
			return 1
		}
	}
	if a[0] == '.' {
		if b[0] != '.' {
			return -1
		}
		switch {
		case a == ".":
			if b == "." {
				return 0
			}
			return -1
		case b == ".":
			return 1
		case a == "..":
			if b == ".." {
				return 0
			}
			return -1
		case b == "..":
			return 1
		}
	} else if b[0] == '.' {
		return 1
	}

	aPrefix := sortVersionPrefixLen(a)
	bPrefix := sortVersionPrefixLen(b)
	onePassOnly := aPrefix == len(a) && bPrefix == len(b)
	result := sortVersionRevCompare(a, aPrefix, b, bPrefix)
	if result != 0 || onePassOnly {
		return result
	}
	return sortVersionRevCompare(a, len(a), b, len(b))
}

func sortIsDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func sortIsAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func sortIsAlphaNum(ch byte) bool {
	return sortIsAlpha(ch) || sortIsDigit(ch)
}

func compareVersions(a, b string) int {
	return sortFileVersionCompare(a, b)
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
	if valA.kind == sortNumberNaN {
		if valA.nanSign < valB.nanSign {
			return -1
		}
		if valA.nanSign > valB.nanSign {
			return 1
		}
		return 0
	}
	if valA.kind != sortNumberFinite {
		return 0
	}
	return valA.value.Cmp(valB.value)
}

func parseGeneralNumericValue(value string) sortGeneralNumber {
	prefix := sortGeneralNumericPrefix(value)
	if prefix == "" {
		return sortGeneralNumber{kind: sortNumberInvalid}
	}
	normalized := sortNormalizeGeneralNumericPrefix(prefix)
	unsigned := sortUnsignedGeneralNumericPrefix(normalized)
	if strings.HasPrefix(unsigned, "nan") {
		return sortGeneralNumber{
			kind:    sortNumberNaN,
			nanSign: sortGeneralNumericPrefixSign(normalized),
		}
	}
	number, _, err := big.ParseFloat(normalized, 10, 256, big.ToNearestEven)
	if err != nil {
		return sortGeneralNumber{kind: sortNumberInvalid}
	}
	return sortGeneralNumber{
		kind:  sortNumberFinite,
		value: number,
		text:  strings.ToLower(number.Text('g', -1)),
	}
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
	return parseSortNumericValueRemainder(value)
}

func parseSortNumericValueRemainder(value string) sortNumericValue {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return sortNumericValue{sign: 1}
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
				return sortNumericValue{sign: 1}
			}
			return sortNumericValue{sign: sign, exponent: exponent, digits: digits.String()}
		}
	}

	if digits.Len() == 0 || !hadDigit {
		return sortNumericValue{sign: 1}
	}
	return sortNumericValue{sign: sign, exponent: exponent, digits: digits.String()}
}

func parseSortHumanNumericValue(value string) sortHumanNumericValue {
	numberText, remainder := sortHumanNumericPrefixAndRemainder(value)
	number := parseSortNumericValueRemainder(numberText)
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

func sortKeyRange(line string, key sortKey, delimiter *string, positions sortKeyPositionOptions) (int, int) {
	runes := []rune(line)
	start := sortKeyBegin(runes, key.startField, keyStartOffset(key), delimiter, positions.startIgnoreLeading)
	if !key.hasEndField {
		return start, len(runes)
	}

	end := sortKeyLimit(runes, key.endField, keyEndOffset(key), delimiter, positions.endIgnoreLeading)
	return start, max(end, start)
}

func sortKeyBegin(runes []rune, field, offset int, delimiter *string, ignoreLeading bool) int {
	ptr := 0
	for remaining := max(field-1, 0); remaining > 0 && ptr < len(runes); remaining-- {
		ptr = sortAdvanceField(runes, ptr, delimiter)
	}
	if ignoreLeading {
		ptr = sortSkipBlanks(runes, ptr)
	}
	ptr = min(ptr+max(offset, 0), len(runes))
	return ptr
}

func sortKeyLimit(runes []rune, field, offset int, delimiter *string, ignoreLeading bool) int {
	if field <= 0 {
		return 0
	}
	if delimiter != nil && offset == 0 {
		ptr := 0
		for remaining := max(field-1, 0); remaining > 0 && ptr < len(runes); remaining-- {
			ptr = sortAdvanceField(runes, ptr, delimiter)
		}
		return sortFieldEnd(runes, ptr, delimiter)
	}
	ptr := 0
	steps := field - 1
	if offset == 0 {
		steps++
	}
	for remaining := max(steps, 0); remaining > 0 && ptr < len(runes); remaining-- {
		ptr = sortAdvanceField(runes, ptr, delimiter)
	}
	if offset != 0 {
		if ignoreLeading {
			ptr = sortSkipBlanks(runes, ptr)
		}
		ptr = min(ptr+offset, len(runes))
	}
	return ptr
}

func sortFieldEnd(runes []rune, ptr int, delimiter *string) int {
	if delimiter != nil {
		delim, _ := utf8.DecodeRuneInString(*delimiter)
		for ptr < len(runes) && runes[ptr] != delim {
			ptr++
		}
		return ptr
	}
	for ptr < len(runes) && sortIsBlankRune(runes[ptr]) {
		ptr++
	}
	for ptr < len(runes) && !sortIsBlankRune(runes[ptr]) {
		ptr++
	}
	return ptr
}

func sortAdvanceField(runes []rune, ptr int, delimiter *string) int {
	if delimiter != nil {
		delim, _ := utf8.DecodeRuneInString(*delimiter)
		for ptr < len(runes) && runes[ptr] != delim {
			ptr++
		}
		if ptr < len(runes) {
			ptr++
		}
		return ptr
	}
	for ptr < len(runes) && sortIsBlankRune(runes[ptr]) {
		ptr++
	}
	for ptr < len(runes) && !sortIsBlankRune(runes[ptr]) {
		ptr++
	}
	return ptr
}

func sortSkipBlanks(runes []rune, ptr int) int {
	for ptr < len(runes) && sortIsBlankRune(runes[ptr]) {
		ptr++
	}
	return ptr
}

func keyStartOffset(key sortKey) int {
	if key.hasStartChar {
		return max(key.startChar-1, 0)
	}
	return 0
}

func keyEndOffset(key sortKey) int {
	if key.hasEndChar {
		return max(key.endChar, 0)
	}
	return 0
}

func sortSliceRunes(line string, start, end int) string {
	start = max(start, 0)
	end = max(end, start)
	byteStart := sortRuneIndexToByteOffset(line, start)
	byteEnd := sortRuneIndexToByteOffset(line, end)
	return line[byteStart:byteEnd]
}

func sortRuneIndexToByteOffset(line string, target int) int {
	if target <= 0 {
		return 0
	}
	runeIndex := 0
	for byteIndex := range line {
		if runeIndex == target {
			return byteIndex
		}
		runeIndex++
	}
	return len(line)
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
			for start < len(runes) && sortIsBlankRune(runes[start]) {
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
	noMatchPos := -1
	for _, key := range opts.keys {
		start, end, ok := sortDebugKeySpan(keyLine, key, opts.fieldDelimiter, sortKeyPositions(opts, key))
		if !ok {
			if noMatchPos == -1 || start < noMatchPos {
				noMatchPos = start + offset
			}
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
		if noMatchPos > 0 {
			return strings.Repeat(" ", noMatchPos) + "^ no match for key"
		}
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
	for offset < len(runes) && sortIsBlankRune(runes[offset]) {
		offset++
	}
	return string(runes[offset:]), offset
}

func sortDebugKeySpan(line string, key sortKey, delimiter *string, positions sortKeyPositionOptions) (int, int, bool) {
	start, end := sortKeyRange(line, key, delimiter, positions)
	if start == end {
		return start, end, false
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
		if key.startField > 1 && !key.startIgnoreLeading {
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
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return 0, fmt.Errorf("invalid count")
	}
	if parsed.Sign() < 0 {
		return 0, fmt.Errorf("negative count")
	}
	if !parsed.IsInt64() || parsed.Int64() > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1), nil
	}
	return int(parsed.Int64()), nil
}

func sortOptionf(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 2, format, args...)
}

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
