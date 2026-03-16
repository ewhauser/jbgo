package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type Join struct{}

type joinDelimiterMode int

const (
	joinDelimiterWhitespace joinDelimiterMode = iota
	joinDelimiterLiteral
	joinDelimiterWholeLine
)

type joinCheckOrder int

const (
	joinCheckOrderDefault joinCheckOrder = iota
	joinCheckOrderDisabled
	joinCheckOrderEnabled
)

type joinOptions struct {
	field1         int
	field2         int
	delimiter      string
	delimiterMode  joinDelimiterMode
	include        [3]bool
	onlyUnpaired   [3]bool
	empty          string
	output         []joinOutputField
	autoOutput     bool
	ignoreCase     bool
	checkOrder     joinCheckOrder
	header         bool
	zeroTerminated bool
}

type joinRecord struct {
	line       string
	fields     []string
	key        string
	lineNumber int
}

type joinOutputField struct {
	file  int
	field int
	join  bool
}

type joinDisorder struct {
	fileName   string
	lineNumber int
	content    string
}

func NewJoin() *Join {
	return &Join{}
}

func (c *Join) Name() string {
	return "join"
}

func (c *Join) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Join) Spec() CommandSpec {
	return CommandSpec{
		Name:  "join",
		About: "For each pair of input lines with identical join fields, write a line to\n  standard output. The default join field is the first, delimited by blanks.\n\n  When FILE1 or FILE2 (not both) is -, read standard input.",
		Usage: "join [OPTION]... FILE1 FILE2",
		Options: []OptionSpec{
			{Name: "a", Short: 'a', Arity: OptionRequiredValue, ValueName: "FILENUM", Repeatable: true, Help: "also print unpairable lines from file FILENUM, where\n  FILENUM is 1 or 2, corresponding to FILE1 or FILE2"},
			{Name: "v", Short: 'v', Arity: OptionRequiredValue, ValueName: "FILENUM", Repeatable: true, Help: "like -a FILENUM, but suppress joined output lines"},
			{Name: "e", Short: 'e', Arity: OptionRequiredValue, ValueName: "EMPTY", Help: "replace missing input fields with EMPTY"},
			{Name: "ignore-case", Short: 'i', Long: "ignore-case", Help: "ignore differences in case when comparing fields"},
			{Name: "j", Short: 'j', Arity: OptionRequiredValue, ValueName: "FIELD", Help: "equivalent to '-1 FIELD -2 FIELD'"},
			{Name: "o", Short: 'o', Arity: OptionRequiredValue, ValueName: "FORMAT", Help: "obey FORMAT while constructing output line"},
			{Name: "t", Short: 't', Arity: OptionRequiredValue, ValueName: "CHAR", Help: "use CHAR as input and output field separator"},
			{Name: "1", Short: '1', Arity: OptionRequiredValue, ValueName: "FIELD", Help: "join on this FIELD of file 1"},
			{Name: "2", Short: '2', Arity: OptionRequiredValue, ValueName: "FIELD", Help: "join on this FIELD of file 2"},
			{Name: "check-order", Long: "check-order", Help: "check that the input is correctly sorted, even if all input lines are pairable"},
			{Name: "nocheck-order", Long: "nocheck-order", Help: "do not check that the input is correctly sorted"},
			{Name: "header", Long: "header", Help: "treat the first line in each file as field headers, print them without trying to pair them"},
			{Name: "zero-terminated", Short: 'z', Long: "zero-terminated", Help: "line delimiter is NUL, not newline"},
		},
		Args: []ArgSpec{
			{Name: "file1", ValueName: "FILE1", Required: true},
			{Name: "file2", ValueName: "FILE2", Required: true},
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

func (c *Join) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, leftName, rightName, err := parseJoinMatches(inv, matches)
	if err != nil {
		return err
	}

	leftData, rightData, err := readTwoInputs(ctx, inv, leftName, rightName)
	if err != nil {
		return err
	}

	leftLines := joinSplitLines(leftData, opts.zeroTerminated)
	rightLines := joinSplitLines(rightData, opts.zeroTerminated)

	var leftHeader *joinRecord
	var rightHeader *joinRecord
	if opts.header {
		leftHeader, leftLines = joinTakeHeader(leftLines, opts.field1, &opts)
		rightHeader, rightLines = joinTakeHeader(rightLines, opts.field2, &opts)
	}

	leftRecords := parseJoinRecords(leftLines, opts.field1, &opts)
	rightRecords := parseJoinRecords(rightLines, opts.field2, &opts)
	if opts.autoOutput {
		opts.output = joinAutoOutput(leftRecords, rightRecords, leftHeader, rightHeader)
	}

	leftDisorder := joinDetectDisorder(leftRecords, leftName)
	rightDisorder := joinDetectDisorder(rightRecords, rightName)

	if opts.header {
		if err := writeJoinLine(inv, &opts, leftHeader, rightHeader); err != nil {
			return err
		}
	}
	if opts.checkOrder == joinCheckOrderEnabled {
		if disorder := joinFirstDisorder(leftDisorder, rightDisorder); disorder != nil {
			return joinWriteDisorder(inv, disorder)
		}
	}

	rightByKey := make(map[string][]int)
	for i, record := range rightRecords {
		rightByKey[record.key] = append(rightByKey[record.key], i)
	}
	rightMatched := make([]bool, len(rightRecords))
	leftUnpaired := false
	rightUnpaired := false
	printJoined := !opts.onlyUnpaired[1] && !opts.onlyUnpaired[2]

	for _, left := range leftRecords {
		matches := rightByKey[left.key]
		if len(matches) == 0 {
			leftUnpaired = true
			if opts.include[1] || opts.onlyUnpaired[1] {
				if err := writeJoinLine(inv, &opts, &left, nil); err != nil {
					return err
				}
			}
			continue
		}
		for _, index := range matches {
			rightMatched[index] = true
			if !printJoined {
				continue
			}
			current := rightRecords[index]
			if err := writeJoinLine(inv, &opts, &left, &current); err != nil {
				return err
			}
		}
	}

	if opts.include[2] || opts.onlyUnpaired[2] {
		for index, matched := range rightMatched {
			if matched {
				continue
			}
			rightUnpaired = true
			current := rightRecords[index]
			if err := writeJoinLine(inv, &opts, nil, &current); err != nil {
				return err
			}
		}
	} else {
		for _, matched := range rightMatched {
			if !matched {
				rightUnpaired = true
				break
			}
		}
	}

	if opts.checkOrder == joinCheckOrderDefault && (leftUnpaired || rightUnpaired) {
		wroteAny := false
		if leftDisorder != nil {
			if err := joinWriteDisorder(inv, leftDisorder); err != nil {
				return err
			}
			wroteAny = true
		}
		if rightDisorder != nil {
			if err := joinWriteDisorder(inv, rightDisorder); err != nil {
				return err
			}
			wroteAny = true
		}
		if wroteAny {
			_, _ = fmt.Fprintln(inv.Stderr, "join: input is not in sorted order")
			return &ExitError{Code: 1}
		}
	}

	return nil
}

func parseJoinMatches(inv *Invocation, matches *ParsedCommand) (opts joinOptions, leftName, rightName string, err error) {
	opts = joinOptions{
		field1:        1,
		field2:        1,
		delimiterMode: joinDelimiterWhitespace,
	}

	for _, value := range matches.Values("a") {
		fileNum, err := parseJoinFileNumber(value)
		if err != nil {
			return joinOptions{}, "", "", exitf(inv, 1, "join: invalid file number: '%s'", value)
		}
		opts.include[fileNum] = true
	}
	for _, value := range matches.Values("v") {
		fileNum, err := parseJoinFileNumber(value)
		if err != nil {
			return joinOptions{}, "", "", exitf(inv, 1, "join: invalid file number: '%s'", value)
		}
		opts.onlyUnpaired[fileNum] = true
	}
	if matches.Has("e") {
		opts.empty = matches.Value("e")
	}
	if matches.Has("ignore-case") {
		opts.ignoreCase = true
	}
	if matches.Has("j") {
		field, err := parseJoinFieldNumber(matches.Value("j"))
		if err != nil {
			return joinOptions{}, "", "", exitf(inv, 1, "join: invalid field number: '%s'", matches.Value("j"))
		}
		opts.field1 = field
		opts.field2 = field
	}
	if matches.Has("1") {
		field, err := parseJoinFieldNumber(matches.Value("1"))
		if err != nil {
			return joinOptions{}, "", "", exitf(inv, 1, "join: invalid field number: '%s'", matches.Value("1"))
		}
		opts.field1 = field
	}
	if matches.Has("2") {
		field, err := parseJoinFieldNumber(matches.Value("2"))
		if err != nil {
			return joinOptions{}, "", "", exitf(inv, 1, "join: invalid field number: '%s'", matches.Value("2"))
		}
		opts.field2 = field
	}
	if matches.Has("o") {
		outputValue := matches.Value("o")
		if outputValue == "auto" {
			opts.autoOutput = true
		} else {
			output, err := parseJoinOutput(outputValue)
			if err != nil {
				return joinOptions{}, "", "", exitf(inv, 1, "%v", err)
			}
			opts.output = output
		}
	}
	if matches.Has("t") {
		delimiter, mode, err := parseJoinDelimiter(matches.Value("t"))
		if err != nil {
			return joinOptions{}, "", "", exitf(inv, 1, "%v", err)
		}
		opts.delimiter = delimiter
		opts.delimiterMode = mode
	}
	if matches.Has("check-order") {
		opts.checkOrder = joinCheckOrderEnabled
	}
	if matches.Has("nocheck-order") {
		opts.checkOrder = joinCheckOrderDisabled
	}
	if matches.Has("header") {
		opts.header = true
	}
	if matches.Has("zero-terminated") {
		opts.zeroTerminated = true
	}

	leftName = matches.Arg("file1")
	rightName = matches.Arg("file2")
	return opts, leftName, rightName, nil
}

func parseJoinFieldNumber(value string) (int, error) {
	field, err := strconv.Atoi(value)
	if err != nil || field <= 0 {
		return 0, fmt.Errorf("invalid")
	}
	return field, nil
}

func parseJoinFileNumber(value string) (int, error) {
	fileNum, err := strconv.Atoi(value)
	if err != nil || (fileNum != 1 && fileNum != 2) {
		return 0, fmt.Errorf("invalid")
	}
	return fileNum, nil
}

func parseJoinDelimiter(value string) (string, joinDelimiterMode, error) {
	if value == "" {
		return "", joinDelimiterWholeLine, nil
	}
	decoded, err := joinDecodeDelimiter(value)
	if err != nil {
		return "", joinDelimiterWhitespace, err
	}
	if len([]rune(decoded)) != 1 {
		return "", joinDelimiterWhitespace, fmt.Errorf("join: multi-character tab %s", value)
	}
	return decoded, joinDelimiterLiteral, nil
}

func joinDecodeDelimiter(value string) (string, error) {
	if !strings.HasPrefix(value, "\\") {
		return value, nil
	}
	switch value {
	case "\\0":
		return "\x00", nil
	case "\\n":
		return "\n", nil
	case "\\t":
		return "\t", nil
	case "\\\\":
		return "\\", nil
	default:
		return "", fmt.Errorf("join: invalid field separator %q", value)
	}
}

func parseJoinOutput(value string) ([]joinOutputField, error) {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || unicodeSpace(r)
	})
	fields := make([]joinOutputField, 0, len(parts))
	for _, part := range parts {
		if part == "0" {
			fields = append(fields, joinOutputField{join: true})
			continue
		}
		fileText, fieldText, ok := strings.Cut(part, ".")
		if !ok {
			return nil, fmt.Errorf("join: invalid field specifier: %s", part)
		}
		file, err := strconv.Atoi(fileText)
		if err != nil || (file != 1 && file != 2) {
			return nil, fmt.Errorf("join: invalid file number in field spec: %s", part)
		}
		field, err := strconv.Atoi(fieldText)
		if err != nil || field <= 0 {
			return nil, fmt.Errorf("join: invalid field specifier: %s", part)
		}
		fields = append(fields, joinOutputField{file: file, field: field})
	}
	return fields, nil
}

func joinSplitLines(data []byte, zeroTerminated bool) []string {
	if len(data) == 0 {
		return nil
	}
	sep := byte('\n')
	if zeroTerminated {
		sep = 0
	}

	parts := strings.Split(string(data), string([]byte{sep}))
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func joinTakeHeader(lines []string, fieldIndex int, opts *joinOptions) (header *joinRecord, remaining []string) {
	if len(lines) == 0 {
		return nil, nil
	}
	record := parseJoinRecord(lines[0], fieldIndex, opts, 1)
	return &record, lines[1:]
}

func parseJoinRecords(lines []string, fieldIndex int, opts *joinOptions) []joinRecord {
	records := make([]joinRecord, 0, len(lines))
	for i, line := range lines {
		records = append(records, parseJoinRecord(line, fieldIndex, opts, i+1))
	}
	return records
}

func parseJoinRecord(line string, fieldIndex int, opts *joinOptions, lineNumber int) joinRecord {
	fields := joinSplitFields(line, opts)
	key := joinFieldValue(fields, fieldIndex)
	if opts.ignoreCase {
		key = strings.ToLower(key)
	}
	return joinRecord{
		line:       line,
		fields:     fields,
		key:        key,
		lineNumber: lineNumber,
	}
}

func joinSplitFields(line string, opts *joinOptions) []string {
	switch opts.delimiterMode {
	case joinDelimiterWholeLine:
		return []string{line}
	case joinDelimiterLiteral:
		return strings.Split(line, opts.delimiter)
	default:
		return strings.Fields(line)
	}
}

func joinFieldValue(fields []string, index int) string {
	if index <= 0 || index > len(fields) {
		return ""
	}
	return fields[index-1]
}

func joinAutoOutput(left, right []joinRecord, leftHeader, rightHeader *joinRecord) []joinOutputField {
	leftFields := joinAutoWidth(left, leftHeader)
	rightFields := joinAutoWidth(right, rightHeader)

	output := make([]joinOutputField, 0, leftFields+rightFields-1)
	output = append(output, joinOutputField{join: true})
	for field := 2; field <= leftFields; field++ {
		output = append(output, joinOutputField{file: 1, field: field})
	}
	for field := 2; field <= rightFields; field++ {
		output = append(output, joinOutputField{file: 2, field: field})
	}
	return output
}

func joinAutoWidth(records []joinRecord, header *joinRecord) int {
	if header != nil && len(records) == 0 {
		return len(header.fields)
	}
	if len(records) == 0 {
		return 1
	}
	return len(records[0].fields)
}

func joinDetectDisorder(records []joinRecord, fileName string) *joinDisorder {
	for i := 1; i < len(records); i++ {
		if joinCompareKeys(records[i-1].key, records[i].key) > 0 {
			return &joinDisorder{
				fileName:   fileName,
				lineNumber: records[i].lineNumber + 1,
				content:    records[i].line,
			}
		}
	}
	return nil
}

func joinCompareKeys(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func joinFirstDisorder(left, right *joinDisorder) *joinDisorder {
	if left != nil {
		return left
	}
	return right
}

func joinWriteDisorder(inv *Invocation, disorder *joinDisorder) error {
	if disorder == nil {
		return nil
	}
	if _, err := fmt.Fprintf(inv.Stderr, "join: %s:%d: is not sorted: %s\n", disorder.fileName, disorder.lineNumber, disorder.content); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func writeJoinLine(inv *Invocation, opts *joinOptions, left, right *joinRecord) error {
	fields := formatJoinFields(opts, left, right)
	sep := joinOutputSeparator(opts)
	lineEnding := "\n"
	if opts.zeroTerminated {
		lineEnding = "\x00"
	}
	if _, err := fmt.Fprint(inv.Stdout, strings.Join(fields, sep), lineEnding); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func joinOutputSeparator(opts *joinOptions) string {
	switch opts.delimiterMode {
	case joinDelimiterWholeLine:
		return ""
	case joinDelimiterLiteral:
		return opts.delimiter
	default:
		return " "
	}
}

func formatJoinFields(opts *joinOptions, left, right *joinRecord) []string {
	if len(opts.output) > 0 {
		fields := make([]string, 0, len(opts.output))
		for _, spec := range opts.output {
			switch {
			case spec.join:
				fields = append(fields, joinJoinKey(left, right, opts))
			case spec.file == 1:
				fields = append(fields, joinRecordField(left, spec.field, opts.empty))
			case spec.file == 2:
				fields = append(fields, joinRecordField(right, spec.field, opts.empty))
			}
		}
		return fields
	}

	fields := []string{joinJoinKey(left, right, opts)}
	if left != nil {
		for i := 1; i <= len(left.fields); i++ {
			if i == opts.field1 {
				continue
			}
			fields = append(fields, left.fields[i-1])
		}
	}
	if right != nil {
		for i := 1; i <= len(right.fields); i++ {
			if i == opts.field2 {
				continue
			}
			fields = append(fields, right.fields[i-1])
		}
	}
	return replaceJoinEmpty(fields, opts.empty)
}

func joinJoinKey(left, right *joinRecord, opts *joinOptions) string {
	switch {
	case left != nil:
		return joinRecordField(left, opts.field1, opts.empty)
	case right != nil:
		return joinRecordField(right, opts.field2, opts.empty)
	default:
		return opts.empty
	}
}

func joinRecordField(record *joinRecord, field int, empty string) string {
	if record == nil || field <= 0 || field > len(record.fields) {
		return empty
	}
	value := record.fields[field-1]
	if value == "" {
		return empty
	}
	return value
}

func replaceJoinEmpty(fields []string, empty string) []string {
	if empty == "" {
		return fields
	}
	out := make([]string, len(fields))
	for i, field := range fields {
		if field == "" {
			out[i] = empty
			continue
		}
		out[i] = field
	}
	return out
}

func unicodeSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

var _ Command = (*Join)(nil)
var _ SpecProvider = (*Join)(nil)
var _ ParsedRunner = (*Join)(nil)
