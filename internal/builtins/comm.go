package builtins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"strings"
)

type Comm struct{}

type commOptions struct {
	suppress        [4]bool
	outputDelimiter string
	delimiterSet    bool
	zeroTerminated  bool
	total           bool
	checkOrder      bool
	noCheckOrder    bool
}

type commOrderChecker struct {
	fileNum  int
	lastLine []byte
	hasError bool
}

func NewComm() *Comm {
	return &Comm{}
}

func (c *Comm) Name() string {
	return "comm"
}

func (c *Comm) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Comm) Spec() CommandSpec {
	return CommandSpec{
		Name:  "comm",
		About: "Compare sorted files FILE1 and FILE2 line by line.",
		Usage: "comm [OPTION]... FILE1 FILE2",
		Options: []OptionSpec{
			{Name: "column-1", Short: '1', Help: "suppress column 1 (lines unique to FILE1)"},
			{Name: "column-2", Short: '2', Help: "suppress column 2 (lines unique to FILE2)"},
			{Name: "column-3", Short: '3', Help: "suppress column 3 (lines that appear in both files)"},
			{Name: "check-order", Long: "check-order", Help: "check that the input is correctly sorted"},
			{Name: "nocheck-order", Long: "nocheck-order", Help: "do not check that the input is correctly sorted"},
			{Name: "output-delimiter", Long: "output-delimiter", ValueName: "STR", Arity: OptionRequiredValue, Help: "separate columns with STR"},
			{Name: "total", Long: "total", Help: "output a summary"},
			{Name: "zero-terminated", Short: 'z', Long: "zero-terminated", Help: "line delimiter is NUL, not newline"},
		},
		Args: []ArgSpec{
			{Name: "file1", ValueName: "FILE1", Help: "first input file"},
			{Name: "file2", ValueName: "FILE2", Help: "second input file"},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
		HelpRenderer: renderStaticHelp(commHelpText),
		VersionRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, commVersionText)
			return err
		},
	}
}

func (c *Comm) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, leftName, rightName, err := parseCommMatches(inv, matches)
	if err != nil {
		return err
	}

	leftData, leftLabel, err := readCommInput(ctx, inv, leftName)
	if err != nil {
		return commInputFailure(inv, leftName, leftLabel, err)
	}
	rightData, rightLabel, err := readCommInput(ctx, inv, rightName)
	if err != nil {
		return commInputFailure(inv, rightName, rightLabel, err)
	}

	recordDelim := byte('\n')
	if opts.zeroTerminated {
		recordDelim = 0
	}
	left := commSplitRecords(leftData, recordDelim)
	right := commSplitRecords(rightData, recordDelim)

	shouldCheckOrder := !opts.noCheckOrder && (opts.checkOrder || !commRecordsEqual(left, right))
	checker1 := commOrderChecker{fileNum: 1}
	checker2 := commOrderChecker{fileNum: 2}
	var (
		i, j       int
		total1     int
		total2     int
		total3     int
		inputError bool
	)

	for i < len(left) || j < len(right) {
		switch commCompareRecords(left, right, i, j) {
		case -1:
			if shouldCheckOrder && !checker1.Verify(inv.Stderr, left[i], opts.checkOrder) {
				goto finish
			}
			if err := writeCommRecord(inv.Stdout, opts, 1, left[i]); err != nil {
				return err
			}
			i++
			total1++
		case 1:
			if shouldCheckOrder && !checker2.Verify(inv.Stderr, right[j], opts.checkOrder) {
				goto finish
			}
			if err := writeCommRecord(inv.Stdout, opts, 2, right[j]); err != nil {
				return err
			}
			j++
			total2++
		default:
			if shouldCheckOrder && (!checker1.Verify(inv.Stderr, left[i], opts.checkOrder) || !checker2.Verify(inv.Stderr, right[j], opts.checkOrder)) {
				goto finish
			}
			if err := writeCommRecord(inv.Stdout, opts, 3, left[i]); err != nil {
				return err
			}
			i++
			j++
			total3++
		}
		if shouldCheckOrder && !opts.checkOrder && (checker1.hasError || checker2.hasError) {
			inputError = true
		}
	}

finish:
	if opts.total {
		if err := writeCommTotal(inv.Stdout, opts, total1, total2, total3, recordDelim); err != nil {
			return err
		}
	}
	if shouldCheckOrder && (checker1.hasError || checker2.hasError) {
		if inputError {
			_, _ = fmt.Fprintln(inv.Stderr, "comm: input is not in sorted order")
		}
		return &ExitError{Code: 1, Err: errors.New("comm: input is not in sorted order")}
	}
	return nil
}

func parseCommMatches(inv *Invocation, matches *ParsedCommand) (opts commOptions, leftName, rightName string, err error) {
	opts.outputDelimiter = "\t"
	delimiterValues := commDelimiterValues(inv, matches)
	delimiterIndex := 0
	for _, name := range matches.OptionOrder() {
		switch name {
		case "column-1":
			opts.suppress[1] = true
		case "column-2":
			opts.suppress[2] = true
		case "column-3":
			opts.suppress[3] = true
		case "zero-terminated":
			opts.zeroTerminated = true
		case "total":
			opts.total = true
		case "check-order":
			opts.checkOrder = true
		case "nocheck-order":
			opts.noCheckOrder = true
		case "output-delimiter":
			if delimiterIndex >= len(delimiterValues) {
				return commOptions{}, "", "", exitf(inv, 1, "comm: internal option parse error")
			}
			if err := setCommDelimiter(inv, &opts, delimiterValues[delimiterIndex]); err != nil {
				return commOptions{}, "", "", err
			}
			delimiterIndex++
		}
	}

	args := matches.Positionals()
	switch len(args) {
	case 0:
		return commOptions{}, "", "", commUsageError(inv, "comm: missing operand")
	case 1:
		return commOptions{}, "", "", commUsageError(inv, "comm: missing operand after %s", quoteGNUOperand(args[0]))
	case 2:
	default:
		return commOptions{}, "", "", commUsageError(inv, "comm: extra operand %s", quoteGNUOperand(args[2]))
	}
	if opts.checkOrder && opts.noCheckOrder {
		return commOptions{}, "", "", exitf(inv, 1, "comm: options '--check-order' and '--nocheck-order' are mutually exclusive")
	}
	if args[0] == "-" && args[1] == "-" {
		return commOptions{}, "", "", exitf(inv, 1, "comm: only one input file may be standard input")
	}
	return opts, args[0], args[1], nil
}

func setCommDelimiter(inv *Invocation, opts *commOptions, value string) error {
	actual := value
	if value == "" {
		actual = "\x00"
	}
	if opts.delimiterSet && opts.outputDelimiter != actual {
		return exitf(inv, 1, "comm: multiple output delimiters specified")
	}
	opts.outputDelimiter = actual
	opts.delimiterSet = true
	return nil
}

func commDelimiterValues(inv *Invocation, matches *ParsedCommand) []string {
	values := matches.Values("output-delimiter")
	if matches.Count("output-delimiter") == len(values) {
		return values
	}

	var rebuilt []string
	args := inv.Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if delimiter, ok := strings.CutPrefix(arg, "--output-delimiter="); ok {
			rebuilt = append(rebuilt, delimiter)
			continue
		}
		if arg == "--output-delimiter" {
			if i+1 < len(args) {
				rebuilt = append(rebuilt, args[i+1])
				i++
			} else {
				rebuilt = append(rebuilt, "")
			}
		}
	}
	if len(rebuilt) == matches.Count("output-delimiter") {
		return rebuilt
	}
	return values
}
func readCommInput(ctx context.Context, inv *Invocation, name string) (data []byte, label string, err error) {
	if name == "-" {
		data, err := readAllStdin(ctx, inv)
		return data, name, err
	}
	abs, err := allowPath(ctx, inv, "", name)
	if err != nil {
		return nil, "", err
	}
	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrInvalid) {
			info, _, statErr := statPath(ctx, inv, name)
			if statErr == nil && info != nil && info.IsDir() {
				return nil, abs, errors.New("is a directory")
			}
		}
		return nil, abs, err
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr == nil && info != nil && info.IsDir() {
		return nil, abs, errors.New("is a directory")
	}
	data, err = readAllReader(ctx, inv, file)
	return data, abs, err
}

func commInputFailure(inv *Invocation, name, label string, err error) error {
	if code, ok := ExitCode(err); ok && code == 126 {
		return err
	}
	return exitf(inv, 1, "comm: %s", commInputError(name, label, err))
}

func commInputError(name, label string, err error) string {
	if err == nil {
		return name
	}
	message := err.Error()
	if message == "is a directory" {
		message = "Is a directory"
	}
	for _, prefix := range []string{
		label + ": ",
		"open " + label + ": ",
		"stat " + label + ": ",
		name + ": ",
		"open " + name + ": ",
		"stat " + name + ": ",
	} {
		if prefix == ": " || prefix == "open : " || prefix == "stat : " {
			continue
		}
		message = strings.TrimPrefix(message, prefix)
	}
	return fmt.Sprintf("%s: %s", name, message)
}

func commSplitRecords(data []byte, delim byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	var records [][]byte
	start := 0
	for i, b := range data {
		if b != delim {
			continue
		}
		records = append(records, append([]byte(nil), data[start:i+1]...))
		start = i + 1
	}
	if start < len(data) {
		record := append([]byte(nil), data[start:]...)
		record = append(record, delim)
		records = append(records, record)
	}
	return records
}

func commRecordsEqual(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !bytes.Equal(left[i], right[i]) {
			return false
		}
	}
	return true
}

func commCompareRecords(left, right [][]byte, i, j int) int {
	switch {
	case i >= len(left):
		return 1
	case j >= len(right):
		return -1
	default:
		return bytes.Compare(left[i], right[j])
	}
}

func (c *commOrderChecker) Verify(stderr io.Writer, current []byte, fatal bool) bool {
	if len(c.lastLine) == 0 {
		c.lastLine = append(c.lastLine[:0], current...)
		return true
	}
	ordered := bytes.Compare(current, c.lastLine) >= 0
	if !ordered && !c.hasError {
		_, _ = fmt.Fprintf(stderr, "comm: file %d is not in sorted order\n", c.fileNum)
		c.hasError = true
	}
	c.lastLine = append(c.lastLine[:0], current...)
	return ordered || !fatal
}

func writeCommRecord(w io.Writer, opts commOptions, column int, record []byte) error {
	if opts.suppress[column] {
		return nil
	}
	prefix := 0
	for i := 1; i < column; i++ {
		if !opts.suppress[i] {
			prefix++
		}
	}
	if prefix > 0 {
		if _, err := io.WriteString(w, strings.Repeat(opts.outputDelimiter, prefix)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if _, err := w.Write(record); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func writeCommTotal(w io.Writer, opts commOptions, total1, total2, total3 int, recordDelim byte) error {
	_, err := fmt.Fprintf(w, "%d%s%d%s%d%stotal%c", total1, opts.outputDelimiter, total2, opts.outputDelimiter, total3, opts.outputDelimiter, recordDelim)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func commUsageError(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, format+"\nTry 'comm --help' for more information.", args...)
}

const commHelpText = `Usage: comm [OPTION]... FILE1 FILE2
Compare sorted files FILE1 and FILE2 line by line.

  -1                      suppress column 1 (lines unique to FILE1)
  -2                      suppress column 2 (lines unique to FILE2)
  -3                      suppress column 3 (lines that appear in both files)
      --check-order       check that the input is correctly sorted
      --nocheck-order     do not check that the input is correctly sorted
      --output-delimiter=STR
                          separate columns with STR
      --total             output a summary
  -z, --zero-terminated   line delimiter is NUL, not newline
      --help              display this help and exit
      --version           output version information and exit
`

const commVersionText = "comm (gbash) dev\n"

var _ Command = (*Comm)(nil)
var _ SpecProvider = (*Comm)(nil)
var _ ParsedRunner = (*Comm)(nil)
