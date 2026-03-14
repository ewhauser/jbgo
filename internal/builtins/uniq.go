package builtins

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Uniq struct{}

type uniqDelimiterMode int

const (
	uniqDelimiterNone uniqDelimiterMode = iota
	uniqDelimiterAppend
	uniqDelimiterPrepend
	uniqDelimiterSeparate
	uniqDelimiterBoth
)

type uniqOptions struct {
	countOnly      bool
	checkChars     int
	checkCharsSet  bool
	duplicatesOnly bool
	ignoreCase     bool
	uniqueOnly     bool
	allRepeated    bool
	groupAll       bool
	skipFields     int
	skipChars      int
	zeroTerminated bool
	delimiters     uniqDelimiterMode
	input          string
	output         string
}

type uniqLineMeta struct {
	keyStart int
	keyEnd   int
}

func NewUniq() *Uniq {
	return &Uniq{}
}

func (c *Uniq) Name() string {
	return "uniq"
}

func (c *Uniq) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Uniq) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}
	args := normalizeUniqLegacyArgs(inv.Args, uniqLegacyPlusEnabled(inv))
	if slicesEqual(args, inv.Args) {
		return inv
	}
	clone := *inv
	clone.Args = args
	return &clone
}

func (c *Uniq) Spec() CommandSpec {
	return CommandSpec{
		Name:      "uniq",
		About:     "Filter adjacent matching lines from INPUT (or standard input), writing to OUTPUT (or standard output).",
		Usage:     "uniq [OPTION]... [INPUT [OUTPUT]]",
		AfterHelp: "Note: 'uniq' does not detect repeated lines unless they are adjacent. You may want to sort the input first, or use 'sort -u' without 'uniq'. Also, comparisons honor the rules specified by the options.",
		Options: []OptionSpec{
			{Name: "all-repeated", Short: 'D', Long: "all-repeated", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "delimit-method", Help: "print all duplicate lines; delimit-method can be none, prepend, or separate"},
			{Name: "group", Long: "group", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "group-method", Help: "show all items, separating groups with an empty line"},
			{Name: "check-chars", Short: 'w', Long: "check-chars", Arity: OptionRequiredValue, ValueName: "N", Help: "compare no more than N characters in lines"},
			{Name: "count", Short: 'c', Long: "count", Help: "prefix lines by the number of occurrences"},
			{Name: "ignore-case", Short: 'i', Long: "ignore-case", Help: "ignore differences in case when comparing"},
			{Name: "repeated", Short: 'd', Long: "repeated", Help: "only print duplicate lines"},
			{Name: "skip-chars", Short: 's', Long: "skip-chars", Arity: OptionRequiredValue, ValueName: "N", Help: "avoid comparing the first N characters"},
			{Name: "skip-fields", Short: 'f', Long: "skip-fields", Arity: OptionRequiredValue, ValueName: "N", Help: "avoid comparing the first N fields"},
			{Name: "unique", Short: 'u', Long: "unique", Help: "only print unique lines"},
			{Name: "zero-terminated", Short: 'z', Long: "zero-terminated", Help: "line delimiter is NUL, not newline"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Uniq) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseUniqMatches(inv, matches)
	if err != nil {
		return err
	}

	reader, closeReader, err := openUniqInput(ctx, inv, opts.input)
	if err != nil {
		return err
	}
	defer closeReader()

	writer, closeWriter, err := openUniqOutput(inv, opts.output)
	if err != nil {
		return err
	}
	defer closeWriter()

	return uniqWrite(ctx, inv, reader, writer, &opts)
}

func parseUniqMatches(inv *Invocation, matches *ParsedCommand) (uniqOptions, error) {
	opts := uniqOptions{
		countOnly:      matches.Has("count"),
		duplicatesOnly: matches.Has("repeated"),
		ignoreCase:     matches.Has("ignore-case"),
		uniqueOnly:     matches.Has("unique"),
		zeroTerminated: matches.Has("zero-terminated"),
	}

	if matches.Has("check-chars") {
		value, err := parseUniqNumericOption(inv, "check-chars", matches.Value("check-chars"))
		if err != nil {
			return uniqOptions{}, err
		}
		opts.checkChars = value
		opts.checkCharsSet = true
	}
	if matches.Has("skip-fields") {
		value, err := parseUniqNumericOption(inv, "skip-fields", matches.Value("skip-fields"))
		if err != nil {
			return uniqOptions{}, err
		}
		opts.skipFields = value
	}
	if matches.Has("skip-chars") {
		value, err := parseUniqNumericOption(inv, "skip-chars", matches.Value("skip-chars"))
		if err != nil {
			return uniqOptions{}, err
		}
		opts.skipChars = value
	}

	if matches.Has("all-repeated") {
		method := matches.Value("all-repeated")
		if method == "" {
			method = "none"
		}
		delimiters, err := parseUniqAllRepeatedMethod(inv, method)
		if err != nil {
			return uniqOptions{}, err
		}
		opts.allRepeated = true
		opts.duplicatesOnly = true
		opts.delimiters = delimiters
	}
	if matches.Has("group") {
		if opts.countOnly || opts.duplicatesOnly || opts.uniqueOnly || opts.allRepeated {
			return uniqOptions{}, exitf(inv, 1, "uniq: --group is mutually exclusive with -c/-d/-D/-u\nTry 'uniq --help' for more information.")
		}
		method := matches.Value("group")
		if method == "" {
			method = "separate"
		}
		delimiters, err := parseUniqGroupMethod(inv, method)
		if err != nil {
			return uniqOptions{}, err
		}
		opts.groupAll = true
		opts.allRepeated = true
		opts.delimiters = delimiters
	}
	if opts.countOnly && opts.allRepeated {
		return uniqOptions{}, exitf(inv, 1, "uniq: printing all duplicated lines and repeat counts is meaningless\nTry 'uniq --help' for more information.")
	}

	files := matches.Args("file")
	if len(files) > 2 {
		return uniqOptions{}, exitf(inv, 1, "uniq: extra operand %q", files[2])
	}
	if len(files) > 0 {
		opts.input = files[0]
	}
	if len(files) > 1 {
		opts.output = files[1]
	}

	return opts, nil
}

func parseUniqNumericOption(inv *Invocation, optionName, raw string) (int, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, exitf(inv, 1, "uniq: invalid %s: %q", optionName, raw)
	}
	if value > uint64(^uint(0)>>1) {
		return int(^uint(0) >> 1), nil
	}
	return int(value), nil
}

func parseUniqAllRepeatedMethod(inv *Invocation, value string) (uniqDelimiterMode, error) {
	switch value {
	case "none":
		return uniqDelimiterNone, nil
	case "prepend":
		return uniqDelimiterPrepend, nil
	case "separate":
		return uniqDelimiterSeparate, nil
	default:
		return uniqDelimiterNone, exitf(inv, 1, "uniq: invalid argument %q for '--all-repeated'\nValid arguments are:\n  - 'none'\n  - 'prepend'\n  - 'separate'\nTry 'uniq --help' for more information.", value)
	}
}

func parseUniqGroupMethod(inv *Invocation, value string) (uniqDelimiterMode, error) {
	switch value {
	case "prepend":
		return uniqDelimiterPrepend, nil
	case "append":
		return uniqDelimiterAppend, nil
	case "separate":
		return uniqDelimiterSeparate, nil
	case "both":
		return uniqDelimiterBoth, nil
	default:
		return uniqDelimiterNone, exitf(inv, 1, "uniq: invalid argument %q for '--group'\nValid arguments are:\n  - 'prepend'\n  - 'append'\n  - 'separate'\n  - 'both'\nTry 'uniq --help' for more information.", value)
	}
}

func openUniqInput(ctx context.Context, inv *Invocation, name string) (io.Reader, func(), error) {
	if name == "" || name == "-" {
		return inv.Stdin, func() {}, nil
	}
	handle, _, err := openRead(ctx, inv, name)
	if err != nil {
		return nil, nil, exitf(inv, 1, "uniq: %s: No such file or directory", name)
	}
	return handle, func() { _ = handle.Close() }, nil
}

func openUniqOutput(inv *Invocation, name string) (io.Writer, func(), error) {
	if name == "" || name == "-" {
		return inv.Stdout, func() {}, nil
	}
	targetAbs := gbfs.Resolve(inv.Cwd, name)
	if err := ensureParentDirExists(context.Background(), inv, targetAbs); err != nil {
		return nil, nil, err
	}
	file, err := inv.FS.OpenFile(context.Background(), targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, exitf(inv, 1, "uniq: %s: %v", name, err)
	}
	return file, func() { _ = file.Close() }, nil
}

func uniqWrite(_ context.Context, inv *Invocation, reader io.Reader, writer io.Writer, opts *uniqOptions) error {
	lineTerminator := byte('\n')
	if opts.zeroTerminated {
		lineTerminator = 0
	}

	bufReader := bufio.NewReader(reader)
	bufWriter := bufio.NewWriter(writer)
	defer func() { _ = bufWriter.Flush() }()

	var current []byte
	if !uniqReadRecord(bufReader, &current, lineTerminator) {
		return nil
	}
	currentMeta := uniqBuildMeta(current, opts, inv)
	groupCount := 1
	firstLinePrinted := false

	var next []byte
	for uniqReadRecord(bufReader, &next, lineTerminator) {
		nextMeta := uniqBuildMeta(next, opts, inv)
		if uniqKeysEqual(current, currentMeta, next, nextMeta, opts) {
			if opts.allRepeated {
				if err := uniqWriteLine(bufWriter, current, groupCount, firstLinePrinted, opts, lineTerminator); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				firstLinePrinted = true
				current = append(current[:0], next...)
				currentMeta = nextMeta
			}
			groupCount++
			next = next[:0]
			continue
		}

		if uniqShouldPrintGroup(groupCount, opts) {
			if err := uniqWriteLine(bufWriter, current, groupCount, firstLinePrinted, opts, lineTerminator); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			firstLinePrinted = true
		}
		current = append(current[:0], next...)
		currentMeta = nextMeta
		groupCount = 1
		next = next[:0]
	}

	if uniqShouldPrintGroup(groupCount, opts) {
		if err := uniqWriteLine(bufWriter, current, groupCount, firstLinePrinted, opts, lineTerminator); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		firstLinePrinted = true
	}
	if (opts.delimiters == uniqDelimiterAppend || opts.delimiters == uniqDelimiterBoth) && firstLinePrinted {
		if err := bufWriter.WriteByte(lineTerminator); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := bufWriter.Flush(); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func uniqReadRecord(reader *bufio.Reader, dst *[]byte, terminator byte) bool {
	*dst = (*dst)[:0]
	line, err := reader.ReadBytes(terminator)
	if len(line) == 0 && err != nil {
		return false
	}
	if len(line) > 0 && line[len(line)-1] == terminator {
		line = line[:len(line)-1]
	}
	*dst = append(*dst, line...)
	return true
}

func uniqBuildMeta(line []byte, opts *uniqOptions, inv *Invocation) uniqLineMeta {
	start := uniqSkipFieldsOffset(line, opts.skipFields)
	if opts.skipChars > 0 {
		start += opts.skipChars
		if start > len(line) {
			start = len(line)
		}
	}
	end := len(line)
	if opts.checkCharsSet {
		end = uniqKeyEndIndex(line, start, opts.checkChars, inv)
	}
	return uniqLineMeta{keyStart: start, keyEnd: end}
}

func uniqSkipFieldsOffset(line []byte, skipFields int) int {
	if skipFields <= 0 {
		return 0
	}
	idx := 0
	for range skipFields {
		for idx < len(line) && uniqIsBlank(line[idx]) {
			idx++
		}
		if idx >= len(line) {
			return len(line)
		}
		for idx < len(line) && !uniqIsBlank(line[idx]) {
			idx++
		}
		if idx >= len(line) {
			return len(line)
		}
	}
	return idx
}

func uniqIsBlank(b byte) bool {
	return b == ' ' || b == '\t'
}

func uniqKeyEndIndex(line []byte, start, limit int, inv *Invocation) int {
	if limit <= 0 || start >= len(line) {
		return start
	}
	remainder := line[start:]
	if uniqIsCLocale(inv) || !utf8.Valid(remainder) {
		if limit > len(remainder) {
			return len(line)
		}
		return start + limit
	}
	count := 0
	for idx := range string(remainder) {
		if count == limit {
			return start + idx
		}
		count++
	}
	return len(line)
}

func uniqIsCLocale(inv *Invocation) bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value, ok := inv.Env[key]; ok && value != "" {
			return value == "C" || value == "POSIX"
		}
	}
	return true
}

func uniqKeysEqual(left []byte, leftMeta uniqLineMeta, right []byte, rightMeta uniqLineMeta, opts *uniqOptions) bool {
	leftSlice := left[leftMeta.keyStart:leftMeta.keyEnd]
	rightSlice := right[rightMeta.keyStart:rightMeta.keyEnd]
	if opts.ignoreCase {
		return uniqEqualFoldASCII(leftSlice, rightSlice)
	}
	return bytes.Equal(leftSlice, rightSlice)
}

func uniqEqualFoldASCII(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if uniqASCIIFold(left[i]) != uniqASCIIFold(right[i]) {
			return false
		}
	}
	return true
}

func uniqASCIIFold(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func uniqShouldPrintGroup(groupCount int, opts *uniqOptions) bool {
	switch {
	case opts.groupAll:
		return true
	case opts.duplicatesOnly && opts.uniqueOnly:
		return false
	case opts.duplicatesOnly:
		return groupCount > 1
	case opts.uniqueOnly:
		return groupCount == 1
	default:
		return true
	}
}

func uniqShouldPrintDelimiter(groupCount int, firstLinePrinted bool, opts *uniqOptions) bool {
	if opts.delimiters == uniqDelimiterNone || groupCount != 1 {
		return false
	}
	return firstLinePrinted || opts.delimiters == uniqDelimiterPrepend || opts.delimiters == uniqDelimiterBoth
}

func uniqWriteLine(writer *bufio.Writer, line []byte, count int, firstLinePrinted bool, opts *uniqOptions, terminator byte) error {
	if uniqShouldPrintDelimiter(count, firstLinePrinted, opts) {
		if err := writer.WriteByte(terminator); err != nil {
			return err
		}
	}
	if opts.countOnly {
		if _, err := fmt.Fprintf(writer, "%7d ", count); err != nil {
			return err
		}
	}
	if _, err := writer.Write(line); err != nil {
		return err
	}
	return writer.WriteByte(terminator)
}

func normalizeUniqLegacyArgs(args []string, enablePlus bool) []string {
	normalized := make([]string, 0, len(args)+2)
	var obsoleteFields string
	var obsoleteChars string
	expectValueForLong := false
	expectValueForShort := false

	for _, arg := range args {
		if enablePlus && shouldNormalizeUniqLegacyPlus(arg, expectValueForLong, expectValueForShort) {
			obsoleteChars = strings.TrimPrefix(arg, "+")
			expectValueForLong = false
			expectValueForShort = false
			continue
		}
		if shouldNormalizeUniqLegacyFields(arg, expectValueForLong, expectValueForShort) {
			filtered, extracted, overwritten := uniqExtractLegacyFields(arg)
			if extracted != "" {
				if overwritten {
					obsoleteFields = ""
				} else {
					obsoleteFields = extracted + obsoleteFields
				}
				if filtered == "" {
					expectValueForLong = false
					expectValueForShort = false
					continue
				}
				arg = filtered
			}
		}

		if strings.HasPrefix(arg, "-f") {
			obsoleteFields = ""
		}
		if strings.HasPrefix(arg, "-s") {
			obsoleteChars = ""
		}
		normalized = append(normalized, arg)
		expectValueForLong, expectValueForShort = uniqRequiresSeparateValue(arg)
	}

	if obsoleteFields != "" && !uniqHasOption(normalized, "-f", "--skip-fields") {
		normalized = append([]string{"-f" + obsoleteFields}, normalized...)
	}
	if obsoleteChars != "" && !uniqHasOption(normalized, "-s", "--skip-chars") {
		normalized = append([]string{"-s" + obsoleteChars}, normalized...)
	}
	return normalized
}

func uniqLegacyPlusEnabled(inv *Invocation) bool {
	if inv == nil {
		return false
	}
	value := inv.Env["_POSIX2_VERSION"]
	if value == "" {
		return false
	}
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed <= 199209
}

func shouldNormalizeUniqLegacyPlus(arg string, expectLongValue, expectShortValue bool) bool {
	if expectLongValue || expectShortValue {
		return false
	}
	if !strings.HasPrefix(arg, "+") || len(arg) < 2 {
		return false
	}
	return isDecimalDigits(arg[1:])
}

func shouldNormalizeUniqLegacyFields(arg string, expectLongValue, expectShortValue bool) bool {
	if expectLongValue || expectShortValue {
		return false
	}
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	if strings.HasPrefix(arg, "-f") || strings.HasPrefix(arg, "-s") || strings.HasPrefix(arg, "-w") {
		return false
	}
	return true
}

func uniqExtractLegacyFields(arg string) (filteredArg, extractedDigits string, overwrittenByNew bool) {
	runes := []rune(arg)
	filtered := make([]rune, 0, len(runes))
	filtered = append(filtered, runes[0])

	var extracted []rune
	collecting := false
	for _, r := range runes[1:] {
		if r == 'f' {
			overwrittenByNew = true
		}
		if r >= '0' && r <= '9' && !collecting {
			collecting = true
			extracted = append(extracted, r)
			continue
		}
		if collecting {
			if r >= '0' && r <= '9' {
				extracted = append(extracted, r)
				continue
			}
			collecting = false
		}
		filtered = append(filtered, r)
	}
	if len(extracted) == 0 {
		return arg, "", false
	}
	if len(filtered) == 1 {
		return "", string(extracted), overwrittenByNew
	}
	return string(filtered), string(extracted), overwrittenByNew
}

func uniqRequiresSeparateValue(arg string) (expectsLongValue, expectsShortValue bool) {
	if strings.HasPrefix(arg, "--") {
		if strings.Contains(arg, "=") {
			return false, false
		}
		switch arg {
		case "--skip-chars", "--skip-fields", "--check-chars", "--group", "--all-repeated":
			return true, false
		default:
			return false, false
		}
	}
	switch arg {
	case "-s", "-f", "-w":
		return false, true
	default:
		return false, false
	}
}

func uniqHasOption(args []string, shortPrefix, longName string) bool {
	for _, arg := range args {
		if arg == shortPrefix || strings.HasPrefix(arg, shortPrefix) {
			return true
		}
		if arg == longName || strings.HasPrefix(arg, longName+"=") {
			return true
		}
	}
	return false
}

var _ Command = (*Uniq)(nil)
var _ SpecProvider = (*Uniq)(nil)
var _ ParsedRunner = (*Uniq)(nil)
var _ ParseInvocationNormalizer = (*Uniq)(nil)
