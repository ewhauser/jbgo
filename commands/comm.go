package commands

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
	showHelp        bool
	showVersion     bool
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
	opts, leftName, rightName, err := parseCommArgs(inv)
	if err != nil {
		return err
	}
	if opts.showHelp {
		_, _ = io.WriteString(inv.Stdout, commHelpText)
		return nil
	}
	if opts.showVersion {
		_, _ = io.WriteString(inv.Stdout, commVersionText)
		return nil
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

func parseCommArgs(inv *Invocation) (opts commOptions, leftName, rightName string, err error) {
	opts.outputDelimiter = "\t"
	args := append([]string(nil), inv.Args...)

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		if strings.HasPrefix(arg, "--") {
			consumed, parseErr := parseCommLongOption(inv, args, &opts)
			if parseErr != nil {
				return commOptions{}, "", "", parseErr
			}
			if opts.showHelp || opts.showVersion {
				return opts, "", "", nil
			}
			args = args[consumed:]
			continue
		}
		if parseErr := parseCommShortOptions(inv, arg, &opts); parseErr != nil {
			return commOptions{}, "", "", parseErr
		}
		args = args[1:]
	}

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

func parseCommLongOption(inv *Invocation, args []string, opts *commOptions) (int, error) {
	arg := args[0]
	name := strings.TrimPrefix(arg, "--")
	value := ""
	hasValue := false
	if before, after, ok := strings.Cut(name, "="); ok {
		name = before
		value = after
		hasValue = true
	}

	match, err := matchCommLongOption(inv, name)
	if err != nil {
		return 0, err
	}
	switch match {
	case "help":
		opts.showHelp = true
		return 1, nil
	case "version":
		opts.showVersion = true
		return 1, nil
	case "total":
		opts.total = true
		return 1, nil
	case "zero-terminated":
		opts.zeroTerminated = true
		return 1, nil
	case "check-order":
		opts.checkOrder = true
		return 1, nil
	case "nocheck-order":
		opts.noCheckOrder = true
		return 1, nil
	case "output-delimiter":
		if hasValue {
			return 1, setCommDelimiter(inv, opts, value)
		}
		if len(args) < 2 {
			return 0, commUsageError(inv, "comm: option '--output-delimiter' requires an argument")
		}
		return 2, setCommDelimiter(inv, opts, args[1])
	default:
		return 0, commOptionf(inv, "comm: unrecognized option '%s'", arg)
	}
}

func matchCommLongOption(inv *Invocation, name string) (string, error) {
	candidates := []string{
		"check-order",
		"help",
		"nocheck-order",
		"output-delimiter",
		"total",
		"version",
		"zero-terminated",
	}
	for _, candidate := range candidates {
		if candidate == name {
			return candidate, nil
		}
	}
	var matches []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, name) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return "", commOptionf(inv, "comm: unrecognized option '%s'", "--"+name)
	case 1:
		return matches[0], nil
	default:
		return "", commOptionf(inv, "comm: option '%s' is ambiguous", "--"+name)
	}
}

func parseCommShortOptions(inv *Invocation, arg string, opts *commOptions) error {
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case '1', '2', '3':
			opts.suppress[arg[i]-'0'] = true
		case 'z':
			opts.zeroTerminated = true
		default:
			return commOptionf(inv, "comm: invalid option -- '%c'", arg[i])
		}
	}
	return nil
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

func readCommInput(ctx context.Context, inv *Invocation, name string) (data []byte, label string, err error) {
	if name == "-" {
		data, err := readAllStdin(inv)
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
	data, err = io.ReadAll(file)
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

func commOptionf(inv *Invocation, format string, args ...any) error {
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
