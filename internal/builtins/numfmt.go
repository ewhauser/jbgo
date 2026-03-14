package builtins

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const numfmtAfterHelp = `UNIT options:

  - none: no auto-scaling is done; suffixes will trigger an error
  - auto: accept optional single/two letter suffix:

      1K = 1000, 1Ki = 1024, 1M = 1000000, 1Mi = 1048576,

  - si: accept optional single letter suffix:

      1K = 1000, 1M = 1000000, ...

  - iec: accept optional single letter suffix:

      1K = 1024, 1M = 1048576, ...

  - iec-i: accept optional two-letter suffix:

      1Ki = 1024, 1Mi = 1048576, ...

  - FIELDS supports cut(1) style field ranges:

      N N'th field, counted from 1
      N- from N'th field, to end of line
      N-M from N'th to M'th field (inclusive)
      -M from first to M'th field (inclusive)
      - all fields

  Multiple fields/ranges can be separated with commas

  FORMAT must be suitable for printing one floating-point argument %f.
  Optional quote (%'f) will enable --grouping (if supported by current locale).
  Optional width value (%10f) will pad output. Optional zero (%010f) width
  will zero pad the number. Optional negative values (%-10f) will left align.
  Optional precision (%.1f) will override the input determined precision.`

var (
	numfmtSIBases = [...]float64{
		1,
		1e3,
		1e6,
		1e9,
		1e12,
		1e15,
		1e18,
		1e21,
		1e24,
		1e27,
		1e30,
	}
	numfmtIECBases = [...]float64{
		1,
		1024,
		1048576,
		1073741824,
		1099511627776,
		1125899906842624,
		1152921504606846976,
		1180591620717411303424,
		1208925819614629174706176,
		1237940039285380274899124224,
		1267650600228229401496703205376,
	}
)

type Numfmt struct{}

type numfmtUnit int

const (
	numfmtUnitNone numfmtUnit = iota
	numfmtUnitAuto
	numfmtUnitSI
	numfmtUnitIEC
	numfmtUnitIECI
)

type numfmtInvalidMode int

const (
	numfmtInvalidAbort numfmtInvalidMode = iota
	numfmtInvalidFail
	numfmtInvalidWarn
	numfmtInvalidIgnore
)

type numfmtRoundMethod int

const (
	numfmtRoundUp numfmtRoundMethod = iota
	numfmtRoundDown
	numfmtRoundFromZero
	numfmtRoundTowardsZero
	numfmtRoundNearest
)

type numfmtRawSuffix int

const (
	numfmtSuffixK numfmtRawSuffix = iota
	numfmtSuffixM
	numfmtSuffixG
	numfmtSuffixT
	numfmtSuffixP
	numfmtSuffixE
	numfmtSuffixZ
	numfmtSuffixY
	numfmtSuffixR
	numfmtSuffixQ
)

type numfmtSuffix struct {
	raw   numfmtRawSuffix
	withI bool
}

type numfmtTransformOptions struct {
	from     numfmtUnit
	fromUnit uint64
	to       numfmtUnit
	toUnit   uint64
}

type numfmtFormatOptions struct {
	grouping    bool
	padding     *int
	precision   *int
	prefix      string
	suffix      string
	zeroPadding bool
}

type numfmtFieldRange struct {
	low  int
	high int
}

type numfmtOptions struct {
	transform      numfmtTransformOptions
	padding        int
	header         int
	fields         []numfmtFieldRange
	delimiter      []byte
	delimiterSet   bool
	round          numfmtRoundMethod
	suffix         string
	suffixSet      bool
	unitSeparator  string
	maxWhitespace  int
	format         numfmtFormatOptions
	invalid        numfmtInvalidMode
	zeroTerminated bool
	debug          bool
}

type numfmtError struct {
	code int
	msg  string
}

func (e *numfmtError) Error() string {
	return e.msg
}

func NewNumfmt() *Numfmt {
	return &Numfmt{}
}

func (c *Numfmt) Name() string {
	return "numfmt"
}

func (c *Numfmt) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Numfmt) Spec() CommandSpec {
	return CommandSpec{
		Name:      "numfmt",
		About:     "Convert numbers from/to human-readable strings",
		Usage:     "numfmt [OPTION]... [NUMBER]...",
		AfterHelp: numfmtAfterHelp,
		Options: []OptionSpec{
			{Name: "debug", Long: "debug", Help: "print warnings about invalid input"},
			{Name: "delimiter", Short: 'd', Long: "delimiter", ValueName: "X", Arity: OptionRequiredValue, Help: "use X instead of whitespace for field delimiter"},
			{Name: "field", Long: "field", ValueName: "FIELDS", Arity: OptionRequiredValue, Help: "replace the numbers in these input fields; see FIELDS below"},
			{Name: "format", Long: "format", ValueName: "FORMAT", Arity: OptionRequiredValue, Help: "use printf style floating-point FORMAT; see FORMAT below for details"},
			{Name: "from", Long: "from", ValueName: "UNIT", Arity: OptionRequiredValue, Help: "auto-scale input numbers to UNITs; see UNIT below"},
			{Name: "from-unit", Long: "from-unit", ValueName: "N", Arity: OptionRequiredValue, Help: "specify the input unit size"},
			{Name: "to", Long: "to", ValueName: "UNIT", Arity: OptionRequiredValue, Help: "auto-scale output numbers to UNITs; see UNIT below"},
			{Name: "to-unit", Long: "to-unit", ValueName: "N", Arity: OptionRequiredValue, Help: "the output unit size"},
			{Name: "padding", Long: "padding", ValueName: "N", Arity: OptionRequiredValue, Help: "pad the output to N characters; positive N will right-align; negative N will left-align; padding is ignored if the output is wider than N; the default is to automatically pad if a whitespace is found"},
			{Name: "header", Long: "header", ValueName: "N", Arity: OptionOptionalValue, Help: "print (without converting) the first N header lines; N defaults to 1 if not specified"},
			{Name: "round", Long: "round", ValueName: "METHOD", Arity: OptionRequiredValue, Help: "use METHOD for rounding when scaling"},
			{Name: "suffix", Long: "suffix", ValueName: "SUFFIX", Arity: OptionRequiredValue, Help: "print SUFFIX after each formatted number, and accept inputs optionally ending with SUFFIX"},
			{Name: "unit-separator", Long: "unit-separator", ValueName: "STRING", Arity: OptionRequiredValue, Help: "use STRING to separate the number from any unit when printing; by default, no separator is used"},
			{Name: "invalid", Long: "invalid", ValueName: "INVALID", Arity: OptionRequiredValue, Help: "set the failure mode for invalid input"},
			{Name: "zero-terminated", Short: 'z', Long: "zero-terminated", Help: "line delimiter is NUL, not newline"},
		},
		Args: []ArgSpec{
			{Name: "number", ValueName: "NUMBER", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			NegativeNumberPositional: true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Numfmt) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseNumfmtMatches(matches)
	if err != nil {
		return numfmtExit(inv, err)
	}

	args := matches.Args("number")
	if opts.debug {
		if opts.transform.from == numfmtUnitNone && opts.transform.to == numfmtUnitNone && opts.padding == 0 {
			_, _ = fmt.Fprintln(inv.Stderr, "numfmt: no conversion option specified")
		}
		if opts.header > 0 && len(args) > 0 {
			_, _ = fmt.Fprintln(inv.Stderr, "numfmt: --header ignored with command-line input")
		}
	}

	writer := bufio.NewWriter(inv.Stdout)
	hadFail := false

	if len(args) > 0 {
		hadFail, err = numfmtHandleArgs(ctx, writer, inv.Stderr, args, &opts)
	} else {
		hadFail, err = numfmtHandleBuffer(ctx, writer, inv.Stderr, inv.Stdin, &opts)
	}

	flushErr := writer.Flush()
	if err != nil {
		if flushErr != nil && numfmtBrokenPipe(flushErr) {
			return nil
		}
		return numfmtExit(inv, err)
	}
	if flushErr != nil {
		if numfmtBrokenPipe(flushErr) {
			return nil
		}
		return &ExitError{Code: 1, Err: flushErr}
	}
	if hadFail {
		return &ExitError{Code: 2}
	}
	return nil
}

func parseNumfmtMatches(matches *ParsedCommand) (numfmtOptions, error) {
	opts := numfmtOptions{
		transform: numfmtTransformOptions{
			from:     numfmtUnitNone,
			fromUnit: 1,
			to:       numfmtUnitNone,
			toUnit:   1,
		},
		fields:        []numfmtFieldRange{{low: 1, high: math.MaxInt}},
		round:         numfmtRoundFromZero,
		maxWhitespace: 1,
		invalid:       numfmtInvalidAbort,
	}

	var err error
	if matches.Has("debug") {
		opts.debug = true
	}
	if matches.Has("delimiter") {
		opts.delimiter, err = parseNumfmtDelimiter(matches.Value("delimiter"))
		if err != nil {
			return numfmtOptions{}, err
		}
		opts.delimiterSet = true
	}
	if matches.Has("field") {
		opts.fields, err = parseNumfmtFields(matches.Value("field"))
		if err != nil {
			return numfmtOptions{}, err
		}
	} else {
		opts.fields = []numfmtFieldRange{{low: 1, high: 1}}
	}
	if matches.Has("format") {
		opts.format, err = parseNumfmtFormat(matches.Value("format"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("from") {
		opts.transform.from, err = parseNumfmtUnit(matches.Value("from"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("from-unit") {
		opts.transform.fromUnit, err = parseNumfmtUnitSize(matches.Value("from-unit"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("to") {
		opts.transform.to, err = parseNumfmtUnit(matches.Value("to"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("to-unit") {
		opts.transform.toUnit, err = parseNumfmtUnitSize(matches.Value("to-unit"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("padding") {
		opts.padding, err = parseNumfmtPadding(matches.Value("padding"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("header") {
		opts.header, err = parseNumfmtHeader(matches.Value("header"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("round") {
		opts.round, err = parseNumfmtRound(matches.Value("round"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("suffix") {
		opts.suffix = matches.Value("suffix")
		opts.suffixSet = true
	}
	if matches.Has("unit-separator") {
		opts.unitSeparator = matches.Value("unit-separator")
		opts.maxWhitespace = len(opts.unitSeparator)
	}
	if matches.Has("invalid") {
		opts.invalid, err = parseNumfmtInvalid(matches.Value("invalid"))
		if err != nil {
			return numfmtOptions{}, err
		}
	}
	if matches.Has("zero-terminated") {
		opts.zeroTerminated = true
	}
	if opts.format.grouping && opts.transform.to != numfmtUnitNone {
		return numfmtOptions{}, numfmtIllegalf("grouping cannot be combined with --to")
	}

	return opts, nil
}

func parseNumfmtUnit(value string) (numfmtUnit, error) {
	switch value {
	case "auto":
		return numfmtUnitAuto, nil
	case "si":
		return numfmtUnitSI, nil
	case "iec":
		return numfmtUnitIEC, nil
	case "iec-i":
		return numfmtUnitIECI, nil
	case "none":
		return numfmtUnitNone, nil
	default:
		return numfmtUnitNone, numfmtIllegalf("Unsupported unit is specified")
	}
}

func parseNumfmtUnitSize(value string) (uint64, error) {
	digitsLen := 0
	for digitsLen < len(value) && value[digitsLen] >= '0' && value[digitsLen] <= '9' {
		digitsLen++
	}
	number := value[:digitsLen]
	suffix := value[digitsLen:]

	if number == "" || strings.Repeat("0", len(number)) != number {
		multiplier, ok := parseNumfmtUnitSizeSuffix(suffix)
		if ok {
			if number == "" {
				return multiplier, nil
			}
			n, err := strconv.ParseUint(number, 10, 64)
			if err == nil && n <= math.MaxUint64/multiplier {
				return n * multiplier, nil
			}
		}
	}

	return 0, numfmtIllegalf("invalid unit size: %s", quoteGNUOperand(value))
}

func parseNumfmtUnitSizeSuffix(value string) (uint64, bool) {
	if value == "" {
		return 1, true
	}

	var index int
	switch value {
	case "K":
		index = 1
	case "Ki":
		return 1024, true
	case "M":
		index = 2
	case "Mi":
		return 1048576, true
	case "G":
		index = 3
	case "Gi":
		return 1073741824, true
	case "T":
		index = 4
	case "Ti":
		return 1099511627776, true
	case "P":
		index = 5
	case "Pi":
		return 1125899906842624, true
	case "E":
		index = 6
	case "Ei":
		return 1152921504606846976, true
	default:
		return 0, false
	}
	return uint64(numfmtSIBases[index]), true
}

func parseNumfmtPadding(value string) (int, error) {
	n, err := strconv.ParseInt(value, 10, 0)
	if err != nil || n == 0 {
		return 0, numfmtIllegalf("invalid padding value %s", quoteGNUOperand(value))
	}
	return int(n), nil
}

func parseNumfmtHeader(value string) (int, error) {
	if value == "" {
		return 1, nil
	}
	n, err := strconv.ParseUint(value, 10, 0)
	if err != nil || n == 0 {
		return 0, numfmtIllegalf("invalid header value %s", quoteGNUOperand(value))
	}
	return int(n), nil
}

func parseNumfmtRound(value string) (numfmtRoundMethod, error) {
	candidates := []struct {
		name   string
		method numfmtRoundMethod
	}{
		{name: "up", method: numfmtRoundUp},
		{name: "down", method: numfmtRoundDown},
		{name: "from-zero", method: numfmtRoundFromZero},
		{name: "towards-zero", method: numfmtRoundTowardsZero},
		{name: "nearest", method: numfmtRoundNearest},
	}

	for _, candidate := range candidates {
		if value == candidate.name {
			return candidate.method, nil
		}
	}

	var matches []numfmtRoundMethod
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate.name, value) {
			matches = append(matches, candidate.method)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	return 0, numfmtIllegalf("invalid rounding method %s", quoteGNUOperand(value))
}

func parseNumfmtInvalid(value string) (numfmtInvalidMode, error) {
	switch value {
	case "abort":
		return numfmtInvalidAbort, nil
	case "fail":
		return numfmtInvalidFail, nil
	case "warn":
		return numfmtInvalidWarn, nil
	case "ignore":
		return numfmtInvalidIgnore, nil
	default:
		return 0, numfmtIllegalf("Unknown invalid mode: %s", value)
	}
}

func parseNumfmtDelimiter(value string) ([]byte, error) {
	if utf8.ValidString(value) {
		if utf8.RuneCountInString(value) > 1 {
			return nil, numfmtIllegalf("the delimiter must be a single character")
		}
		return []byte(value), nil
	}
	if len(value) > 4 {
		return nil, numfmtIllegalf("the delimiter must be a single character")
	}
	return []byte(value), nil
}

func parseNumfmtFields(value string) ([]numfmtFieldRange, error) {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	if len(parts) == 0 {
		return nil, numfmtIllegalf("invalid field specifier %s", quoteGNUOperand(value))
	}
	if slices.Contains(parts, "-") {
		return []numfmtFieldRange{{low: 1, high: math.MaxInt}}, nil
	}

	ranges := make([]numfmtFieldRange, 0, len(parts))
	for _, part := range parts {
		r, err := parseNumfmtFieldPart(part)
		if err != nil {
			return nil, numfmtIllegalf("invalid field specifier %s", quoteGNUOperand(value))
		}
		ranges = append(ranges, r)
	}
	return ranges, nil
}

func parseNumfmtFieldPart(part string) (numfmtFieldRange, error) {
	if !strings.Contains(part, "-") {
		n, err := parseNumfmtFieldNumber(part)
		if err != nil {
			return numfmtFieldRange{}, err
		}
		return numfmtFieldRange{low: n, high: n}, nil
	}

	left, right, _ := strings.Cut(part, "-")
	switch {
	case left == "" && right == "":
		return numfmtFieldRange{}, errors.New("empty range")
	case left == "":
		high, err := parseNumfmtFieldNumber(right)
		if err != nil {
			return numfmtFieldRange{}, err
		}
		return numfmtFieldRange{low: 1, high: high}, nil
	case right == "":
		low, err := parseNumfmtFieldNumber(left)
		if err != nil {
			return numfmtFieldRange{}, err
		}
		return numfmtFieldRange{low: low, high: math.MaxInt}, nil
	default:
		low, err := parseNumfmtFieldNumber(left)
		if err != nil {
			return numfmtFieldRange{}, err
		}
		high, err := parseNumfmtFieldNumber(right)
		if err != nil {
			return numfmtFieldRange{}, err
		}
		if high < low {
			return numfmtFieldRange{}, errors.New("decreasing range")
		}
		return numfmtFieldRange{low: low, high: high}, nil
	}
}

func parseNumfmtFieldNumber(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, errors.New("invalid field")
	}
	return n, nil
}

func parseNumfmtFormat(value string) (numfmtFormatOptions, error) {
	var opts numfmtFormatOptions

	runes := []rune(value)
	i := 0
	doublePercents := 0
	for i < len(runes) {
		switch {
		case runes[i] == '%' && i+1 < len(runes) && runes[i+1] == '%':
			opts.prefix += "%%"
			doublePercents++
			i += 2
		case runes[i] == '%':
			i++
			goto parseDirective
		default:
			opts.prefix += string(runes[i])
			i++
		}
	}
	if opts.prefix == value {
		return numfmtFormatOptions{}, numfmtIllegalf("format %s has no %% directive", quoteGNUOperand(value))
	}
	return numfmtFormatOptions{}, numfmtIllegalf("format %s ends in %%", quoteGNUOperand(value))

parseDirective:
	for i := 0; i < doublePercents; i++ {
		if opts.prefix == "" {
			break
		}
		_, size := utf8.DecodeLastRuneInString(opts.prefix)
		opts.prefix = opts.prefix[:len(opts.prefix)-size]
	}

	for i < len(runes) {
		switch runes[i] {
		case ' ':
		case '\'':
			opts.grouping = true
		case '0':
			opts.zeroPadding = true
		default:
			goto parseWidth
		}
		i++
	}

parseWidth:
	paddingRaw := ""
	if i < len(runes) && runes[i] == '-' {
		i++
		if i >= len(runes) || runes[i] < '0' || runes[i] > '9' {
			return numfmtFormatOptions{}, numfmtIllegalf("invalid format %s, directive must be %%[0]['][-][N][.][N]f", quoteGNUOperand(value))
		}
		paddingRaw = "-"
	}
	for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
		paddingRaw += string(runes[i])
		i++
	}
	if paddingRaw != "" {
		paddingValue, err := strconv.ParseInt(paddingRaw, 10, 0)
		if err != nil {
			return numfmtFormatOptions{}, numfmtIllegalf("invalid format %s (width overflow)", quoteGNUOperand(value))
		}
		padding := int(paddingValue)
		opts.padding = &padding
	}

	if i < len(runes) && runes[i] == '.' {
		i++
		if i < len(runes) {
			switch runes[i] {
			case ' ', '+', '-':
				return numfmtFormatOptions{}, numfmtIllegalf("invalid precision in format %s", quoteGNUOperand(value))
			}
		}

		precisionRaw := ""
		for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
			precisionRaw += string(runes[i])
			i++
		}

		precisionValue := 0
		if precisionRaw != "" {
			parsed, err := strconv.ParseInt(precisionRaw, 10, 0)
			if err != nil {
				return numfmtFormatOptions{}, numfmtIllegalf("invalid precision in format %s", quoteGNUOperand(value))
			}
			precisionValue = int(parsed)
		}
		opts.precision = &precisionValue
	}

	if i >= len(runes) || runes[i] != 'f' {
		return numfmtFormatOptions{}, numfmtIllegalf("invalid format %s, directive must be %%[0]['][-][N][.][N]f", quoteGNUOperand(value))
	}
	i++

	for i < len(runes) {
		if runes[i] != '%' {
			opts.suffix += string(runes[i])
			i++
			continue
		}
		if i+1 >= len(runes) || runes[i+1] != '%' {
			return numfmtFormatOptions{}, numfmtIllegalf("format %s has too many %% directives", quoteGNUOperand(value))
		}
		opts.suffix += "%%"
		i += 2
	}

	return opts, nil
}

func numfmtHandleArgs(ctx context.Context, writer *bufio.Writer, stderr io.Writer, args []string, opts *numfmtOptions) (bool, error) {
	terminator := byte('\n')
	if opts.zeroTerminated {
		terminator = 0
	}

	hadFail := false
	for _, arg := range args {
		if err := ctx.Err(); err != nil {
			return hadFail, err
		}
		lineFail, err := numfmtWriteLine(writer, stderr, []byte(arg), opts, true, terminator)
		if err != nil {
			return hadFail, err
		}
		hadFail = hadFail || lineFail
	}
	return hadFail, nil
}

func numfmtHandleBuffer(ctx context.Context, writer *bufio.Writer, stderr io.Writer, input io.Reader, opts *numfmtOptions) (bool, error) {
	terminator := byte('\n')
	if opts.zeroTerminated {
		terminator = 0
	}

	reader := bufio.NewReader(input)
	hadFail := false
	lineIndex := 0
	for {
		if err := ctx.Err(); err != nil {
			return hadFail, err
		}

		line, err := reader.ReadBytes(terminator)
		if err != nil && !errors.Is(err, io.EOF) {
			return hadFail, numfmtIOError(err)
		}
		if len(line) == 0 && errors.Is(err, io.EOF) {
			break
		}

		hasTerminator := len(line) > 0 && line[len(line)-1] == terminator
		if hasTerminator {
			line = line[:len(line)-1]
		}

		if lineIndex < opts.header {
			if writeErr := numfmtWriteRawLine(writer, line, hasTerminator, terminator); writeErr != nil {
				return hadFail, writeErr
			}
		} else {
			lineFail, writeErr := numfmtWriteLine(writer, stderr, line, opts, hasTerminator, terminator)
			if writeErr != nil {
				return hadFail, writeErr
			}
			hadFail = hadFail || lineFail
		}
		lineIndex++

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return hadFail, nil
}

func numfmtWriteLine(writer, stderr io.Writer, inputLine []byte, opts *numfmtOptions, emitTerminator bool, terminator byte) (bool, error) {
	line := inputLine
	if idx := bytes.IndexByte(line, 0); idx >= 0 {
		line = line[:idx]
	}

	var err error
	switch {
	case opts.delimiterSet:
		err = numfmtWriteFormattedWithDelimiter(writer, line, opts, emitTerminator, terminator)
	case utf8.Valid(line):
		err = numfmtWriteFormattedWithWhitespace(writer, string(line), opts, emitTerminator, terminator)
	default:
		err = numfmtFormatf("invalid number: %s", quoteGNUOperand(numfmtEscapeLine(line)))
	}

	if err == nil {
		return false, nil
	}

	var nf *numfmtError
	if !errors.As(err, &nf) || nf.code != 2 {
		return false, err
	}

	switch opts.invalid {
	case numfmtInvalidAbort:
		return false, err
	case numfmtInvalidFail:
		_, _ = fmt.Fprintln(stderr, nf.msg)
		if writeErr := numfmtWriteRawLine(writer, inputLine, emitTerminator, terminator); writeErr != nil {
			return false, writeErr
		}
		return true, nil
	case numfmtInvalidWarn:
		_, _ = fmt.Fprintln(stderr, nf.msg)
		if writeErr := numfmtWriteRawLine(writer, inputLine, emitTerminator, terminator); writeErr != nil {
			return false, writeErr
		}
		return false, nil
	case numfmtInvalidIgnore:
		if writeErr := numfmtWriteRawLine(writer, inputLine, emitTerminator, terminator); writeErr != nil {
			return false, writeErr
		}
		return false, nil
	default:
		return false, err
	}
}

func numfmtWriteFormattedWithDelimiter(writer io.Writer, input []byte, opts *numfmtOptions, emitTerminator bool, terminator byte) error {
	delimiter := opts.delimiter
	fieldIndex := 1

	if len(delimiter) == 0 {
		formatted, err := numfmtFormatString(string(bytes.TrimLeftFunc(input, unicode.IsSpace)), opts, nil)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(writer, formatted); err != nil {
			return numfmtIOError(err)
		}
		if emitTerminator {
			if _, err := writer.Write([]byte{terminator}); err != nil {
				return numfmtIOError(err)
			}
		}
		return nil
	}

	remaining := input
	for {
		var field []byte
		if idx := bytes.Index(remaining, delimiter); idx >= 0 {
			field = remaining[:idx]
			remaining = remaining[idx+len(delimiter):]
		} else {
			field = remaining
			remaining = nil
		}

		if fieldIndex > 1 {
			if _, err := writer.Write(delimiter); err != nil {
				return numfmtIOError(err)
			}
		}

		if numfmtFieldSelected(opts.fields, fieldIndex) {
			if !utf8.Valid(field) {
				return numfmtFormatf("invalid number: %s", quoteGNUOperand(numfmtEscapeLine(field)))
			}
			formatted, err := numfmtFormatString(strings.TrimLeftFunc(string(field), unicode.IsSpace), opts, nil)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(writer, formatted); err != nil {
				return numfmtIOError(err)
			}
		} else if _, err := writer.Write(field); err != nil {
			return numfmtIOError(err)
		}

		fieldIndex++
		if remaining == nil {
			break
		}
	}

	if emitTerminator {
		if _, err := writer.Write([]byte{terminator}); err != nil {
			return numfmtIOError(err)
		}
	}
	return nil
}

func numfmtWriteFormattedWithWhitespace(writer io.Writer, input string, opts *numfmtOptions, emitTerminator bool, terminator byte) error {
	remaining := input
	fieldIndex := 1
	for {
		prefix, field, rest := numfmtNextWhitespaceField(remaining)
		if numfmtFieldSelected(opts.fields, fieldIndex) {
			emptyPrefix := prefix == ""
			if fieldIndex > 1 {
				if _, err := io.WriteString(writer, " "); err != nil {
					return numfmtIOError(err)
				}
			}

			var implicitPadding *int
			if !emptyPrefix && opts.padding == 0 {
				value := len(prefix) + len(field)
				implicitPadding = &value
			}

			formatted, err := numfmtFormatString(field, opts, implicitPadding)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(writer, formatted); err != nil {
				return numfmtIOError(err)
			}
		} else {
			if opts.zeroTerminated && strings.HasPrefix(prefix, "\n") {
				if _, err := io.WriteString(writer, " "); err != nil {
					return numfmtIOError(err)
				}
				prefix = prefix[1:]
			}
			if _, err := io.WriteString(writer, prefix); err != nil {
				return numfmtIOError(err)
			}
			if _, err := io.WriteString(writer, field); err != nil {
				return numfmtIOError(err)
			}
		}

		fieldIndex++
		if rest == "" {
			break
		}
		remaining = rest
	}

	if emitTerminator {
		if _, err := writer.Write([]byte{terminator}); err != nil {
			return numfmtIOError(err)
		}
	}
	return nil
}

func numfmtNextWhitespaceField(input string) (prefix, field, rest string) {
	if input == "" {
		return "", "", ""
	}

	start := 0
	for start < len(input) {
		r, size := utf8.DecodeRuneInString(input[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	prefix = input[:start]

	end := start
	for end < len(input) {
		r, size := utf8.DecodeRuneInString(input[end:])
		if unicode.IsSpace(r) {
			break
		}
		end += size
	}

	field = input[start:end]
	if end < len(input) {
		rest = input[end:]
	}
	return prefix, field, rest
}

func numfmtFieldSelected(ranges []numfmtFieldRange, field int) bool {
	for _, current := range ranges {
		if field >= current.low && field <= current.high {
			return true
		}
	}
	return false
}

func numfmtFormatString(source string, opts *numfmtOptions, implicitPadding *int) (string, error) {
	sourceWithoutSuffix := source
	if opts.suffixSet && strings.HasSuffix(sourceWithoutSuffix, opts.suffix) {
		sourceWithoutSuffix = strings.TrimSuffix(sourceWithoutSuffix, opts.suffix)
	}

	precision := 0
	if opts.format.precision != nil {
		precision = *opts.format.precision
	} else if opts.transform.from == numfmtUnitNone && opts.transform.to == numfmtUnitNone {
		precision = numfmtImplicitPrecision(sourceWithoutSuffix)
	}

	value, err := numfmtTransformFrom(sourceWithoutSuffix, &opts.transform, opts.maxWhitespace)
	if err != nil {
		return "", err
	}
	number, err := numfmtTransformTo(value, &opts.transform, opts.round, precision, opts.unitSeparator)
	if err != nil {
		return "", err
	}

	if opts.suffixSet {
		number += opts.suffix
	}

	padding := opts.padding
	if implicitPadding != nil {
		padding = *implicitPadding
	}
	if opts.format.padding != nil {
		padding = *opts.format.padding
	}

	padded := number
	switch {
	case padding == 0:
	case padding > 0 && opts.format.zeroPadding:
		zeroPadded := fmt.Sprintf("%0*s", padding, number)
		outerPadding := opts.padding
		if implicitPadding != nil {
			outerPadding = *implicitPadding
		}
		switch {
		case outerPadding == 0:
			padded = zeroPadded
		case outerPadding > 0:
			padded = fmt.Sprintf("%*s", outerPadding, zeroPadded)
		default:
			padded = fmt.Sprintf("%-*s", -outerPadding, zeroPadded)
		}
	case padding > 0:
		padded = fmt.Sprintf("%*s", padding, number)
	default:
		padded = fmt.Sprintf("%-*s", -padding, number)
	}

	return opts.format.prefix + padded + opts.format.suffix, nil
}

func numfmtTransformFrom(source string, opts *numfmtTransformOptions, maxWhitespace int) (float64, error) {
	number, suffix, err := numfmtParseSuffix(source, opts.from, maxWhitespace)
	if err != nil {
		if message, ok := numfmtDetailedErrorMessage(source, opts.from); ok {
			return 0, numfmtFormatf("%s", message)
		}
		return 0, err
	}

	number *= float64(opts.fromUnit)
	value, err := numfmtRemoveSuffix(number, suffix, opts.from)
	if err != nil {
		return 0, err
	}

	if opts.from == numfmtUnitNone {
		if value == 0 && math.Signbit(value) {
			return 0, nil
		}
		return value, nil
	}
	if value < 0 {
		return -math.Ceil(math.Abs(value)), nil
	}
	return math.Ceil(value), nil
}

func numfmtTransformTo(value float64, opts *numfmtTransformOptions, round numfmtRoundMethod, precision int, unitSeparator string) (string, error) {
	scaled, suffix, err := numfmtConsiderSuffix(value, opts.to, round, precision)
	if err != nil {
		return "", err
	}
	scaled /= float64(opts.toUnit)

	if suffix == nil {
		rounded := numfmtRoundWithPrecision(scaled, round, precision)
		return fmt.Sprintf("%.*f", precision, rounded), nil
	}

	displaySuffix := numfmtDisplaySuffix(*suffix, opts.to)
	switch {
	case precision > 0:
		return fmt.Sprintf("%.*f%s%s", precision, scaled, unitSeparator, displaySuffix), nil
	case math.Abs(scaled) < 10:
		return fmt.Sprintf("%.1f%s%s", scaled, unitSeparator, displaySuffix), nil
	default:
		return fmt.Sprintf("%.0f%s%s", scaled, unitSeparator, displaySuffix), nil
	}
}

func numfmtParseSuffix(input string, unit numfmtUnit, maxWhitespace int) (float64, *numfmtSuffix, error) {
	trimmed := strings.TrimRightFunc(input, unicode.IsSpace)
	if trimmed == "" {
		return 0, nil, numfmtFormatf("invalid number: ''")
	}

	withI := strings.HasSuffix(trimmed, "i")
	if withI && unit != numfmtUnitAuto && unit != numfmtUnitIECI {
		return 0, nil, numfmtFormatf("invalid suffix in input: %s", quoteGNUOperand(input))
	}

	working := trimmed
	if withI {
		working = working[:len(working)-1]
	}

	var suffix *numfmtSuffix
	if working != "" {
		last := working[len(working)-1]
		if raw, ok := numfmtRawSuffixFromByte(last); ok {
			suffix = &numfmtSuffix{raw: raw, withI: withI}
		} else if last < '0' || last > '9' || withI {
			return 0, nil, numfmtFormatf("invalid number: %s", quoteGNUOperand(input))
		}
	}

	suffixLen := 0
	if suffix != nil {
		suffixLen = 1
		if suffix.withI {
			suffixLen = 2
		}
	}

	numberPart := trimmed[:len(trimmed)-suffixLen]
	numberTrimmed := strings.TrimRightFunc(numberPart, unicode.IsSpace)
	if suffix != nil && len(numberPart)-len(numberTrimmed) > maxWhitespace {
		return 0, nil, numfmtFormatf("invalid suffix in input: %s", quoteGNUOperand(input))
	}

	number, err := strconv.ParseFloat(numberTrimmed, 64)
	if err != nil {
		return 0, nil, numfmtFormatf("invalid number: %s", quoteGNUOperand(input))
	}

	return number, suffix, nil
}

func numfmtDetailedErrorMessage(input string, unit numfmtUnit) (string, bool) {
	if input == "" {
		return "invalid number: ''", true
	}

	validPart, ok := numfmtFindValidNumberWithSuffix(input, unit)
	if !ok {
		return fmt.Sprintf("invalid number: %s", quoteGNUOperand(input)), true
	}
	if validPart == input {
		return "", false
	}

	if _, err := strconv.ParseFloat(validPart, 64); err == nil {
		remainder := input[len(validPart):]
		if remainder != "" {
			if raw, ok := numfmtRawSuffixFromByte(remainder[0]); ok {
				_ = raw
				return fmt.Sprintf("rejecting suffix in input: %s (consider using --from)", quoteGNUOperand(validPart+remainder)), true
			}
		}
		return fmt.Sprintf("invalid suffix in input: %s", quoteGNUOperand(input)), true
	}

	return fmt.Sprintf("invalid suffix in input %s: %s", quoteGNUOperand(input), quoteGNUOperand(input[len(validPart):])), true
}

func numfmtFindValidNumberWithSuffix(input string, unit numfmtUnit) (string, bool) {
	numericPart, ok := numfmtFindNumericBeginning(input)
	if !ok {
		return "", false
	}

	acceptsSuffix := unit != numfmtUnitNone
	acceptsI := unit == numfmtUnitAuto || unit == numfmtUnitIECI
	if !acceptsSuffix {
		return numericPart, true
	}

	remainder := input[len(numericPart):]
	if remainder == "" {
		return numericPart, true
	}

	first := remainder[0]
	_, suffixOK := numfmtRawSuffixFromByte(first)
	switch {
	case suffixOK && len(remainder) == 1:
		return input[:len(numericPart)+1], true
	case suffixOK && len(remainder) >= 2 && remainder[1] == 'i' && acceptsI:
		return input[:len(numericPart)+2], true
	case suffixOK:
		return input[:len(numericPart)+1], true
	default:
		return numericPart, true
	}
}

func numfmtFindNumericBeginning(input string) (string, bool) {
	decimalPointSeen := false
	if input == "" {
		return "", false
	}

	for idx, r := range input {
		if r == '-' && idx == 0 {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && !decimalPointSeen {
			decimalPointSeen = true
			continue
		}
		if _, err := strconv.ParseFloat(input[:idx], 64); err != nil {
			return "", false
		}
		return input[:idx], true
	}

	return input, true
}

func numfmtRemoveSuffix(number float64, suffix *numfmtSuffix, unit numfmtUnit) (float64, error) {
	if suffix == nil {
		return number, nil
	}

	switch {
	case !suffix.withI && (unit == numfmtUnitAuto || unit == numfmtUnitSI):
		return number * numfmtSuffixBase(suffix.raw, false), nil
	case (!suffix.withI && unit == numfmtUnitIEC) || (suffix.withI && (unit == numfmtUnitAuto || unit == numfmtUnitIECI)):
		return number * numfmtSuffixBase(suffix.raw, true), nil
	case !suffix.withI && unit == numfmtUnitIECI:
		return 0, numfmtFormatf("missing 'i' suffix in input: %s (e.g Ki/Mi/Gi)", quoteGNUOperand(numfmtFormatInputFloat(number)+numfmtRawSuffixString(suffix.raw, false)))
	case unit == numfmtUnitNone:
		return 0, numfmtFormatf("rejecting suffix in input: %s (consider using --from)", quoteGNUOperand(numfmtFormatInputFloat(number)+numfmtRawSuffixString(suffix.raw, suffix.withI)))
	default:
		return 0, numfmtFormatf("This suffix is unsupported for specified unit")
	}
}

func numfmtConsiderSuffix(number float64, unit numfmtUnit, round numfmtRoundMethod, precision int) (float64, *numfmtSuffix, error) {
	if unit == numfmtUnitAuto {
		return 0, nil, numfmtFormatf("Unit 'auto' isn't supported with --to options")
	}
	if unit == numfmtUnitNone {
		return number, nil, nil
	}

	bases := numfmtSIBases[:]
	withI := false
	if unit == numfmtUnitIEC || unit == numfmtUnitIECI {
		bases = numfmtIECBases[:]
		withI = unit == numfmtUnitIECI
	}

	abs := math.Abs(number)
	if abs <= bases[1]-1 {
		return number, nil, nil
	}

	index := 0
	switch {
	case abs < bases[2]:
		index = 1
	case abs < bases[3]:
		index = 2
	case abs < bases[4]:
		index = 3
	case abs < bases[5]:
		index = 4
	case abs < bases[6]:
		index = 5
	case abs < bases[7]:
		index = 6
	case abs < bases[8]:
		index = 7
	case abs < bases[9]:
		index = 8
	case abs < bases[10]:
		index = 9
	case abs < bases[10]*1000:
		index = 10
	default:
		return 0, nil, numfmtFormatf("Number is too big and unsupported")
	}

	scaled := number / bases[index]
	if precision > 0 {
		scaled = numfmtRoundWithPrecision(scaled, round, precision)
	} else {
		scaled = numfmtDivRound(number, bases[index], round)
	}

	if math.Abs(scaled) >= bases[1] {
		next := numfmtSuffixForIndex(index)
		return scaled / bases[1], &numfmtSuffix{raw: next, withI: withI}, nil
	}

	current := numfmtSuffixForIndex(index - 1)
	return scaled, &numfmtSuffix{raw: current, withI: withI}, nil
}

func numfmtDivRound(numerator, denominator float64, method numfmtRoundMethod) float64 {
	value := numerator / denominator
	if math.Abs(value) < 10 {
		return numfmtApplyRound(method, value*10) / 10
	}
	return numfmtApplyRound(method, value)
}

func numfmtRoundWithPrecision(value float64, method numfmtRoundMethod, precision int) float64 {
	power := math.Pow(10, float64(precision))
	return numfmtApplyRound(method, value*power) / power
}

func numfmtApplyRound(method numfmtRoundMethod, value float64) float64 {
	switch method {
	case numfmtRoundUp:
		return math.Ceil(value)
	case numfmtRoundDown:
		return math.Floor(value)
	case numfmtRoundFromZero:
		if value < 0 {
			return math.Floor(value)
		}
		return math.Ceil(value)
	case numfmtRoundTowardsZero:
		if value < 0 {
			return math.Ceil(value)
		}
		return math.Floor(value)
	default:
		return math.Round(value)
	}
}

func numfmtImplicitPrecision(value string) int {
	dot := strings.IndexByte(value, '.')
	if dot < 0 {
		return 0
	}
	count := 0
	for i := dot + 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			break
		}
		count++
	}
	return count
}

func numfmtDisplaySuffix(suffix numfmtSuffix, unit numfmtUnit) string {
	raw := numfmtRawSuffixString(suffix.raw, false)
	if suffix.raw == numfmtSuffixK && unit == numfmtUnitSI {
		raw = "k"
	}
	if suffix.withI {
		return raw + "i"
	}
	return raw
}

func numfmtSuffixBase(raw numfmtRawSuffix, iec bool) float64 {
	index := int(raw) + 1
	if iec {
		return numfmtIECBases[index]
	}
	return numfmtSIBases[index]
}

func numfmtSuffixForIndex(index int) numfmtRawSuffix {
	switch index {
	case 0:
		return numfmtSuffixK
	case 1:
		return numfmtSuffixM
	case 2:
		return numfmtSuffixG
	case 3:
		return numfmtSuffixT
	case 4:
		return numfmtSuffixP
	case 5:
		return numfmtSuffixE
	case 6:
		return numfmtSuffixZ
	case 7:
		return numfmtSuffixY
	case 8:
		return numfmtSuffixR
	default:
		return numfmtSuffixQ
	}
}

func numfmtRawSuffixString(raw numfmtRawSuffix, withI bool) string {
	var suffix string
	switch raw {
	case numfmtSuffixK:
		suffix = "K"
	case numfmtSuffixM:
		suffix = "M"
	case numfmtSuffixG:
		suffix = "G"
	case numfmtSuffixT:
		suffix = "T"
	case numfmtSuffixP:
		suffix = "P"
	case numfmtSuffixE:
		suffix = "E"
	case numfmtSuffixZ:
		suffix = "Z"
	case numfmtSuffixY:
		suffix = "Y"
	case numfmtSuffixR:
		suffix = "R"
	default:
		suffix = "Q"
	}
	if withI {
		return suffix + "i"
	}
	return suffix
}

func numfmtRawSuffixFromByte(value byte) (numfmtRawSuffix, bool) {
	switch value {
	case 'K', 'k':
		return numfmtSuffixK, true
	case 'M':
		return numfmtSuffixM, true
	case 'G':
		return numfmtSuffixG, true
	case 'T':
		return numfmtSuffixT, true
	case 'P':
		return numfmtSuffixP, true
	case 'E':
		return numfmtSuffixE, true
	case 'Z':
		return numfmtSuffixZ, true
	case 'Y':
		return numfmtSuffixY, true
	case 'R':
		return numfmtSuffixR, true
	case 'Q':
		return numfmtSuffixQ, true
	default:
		return 0, false
	}
}

func numfmtFormatInputFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func numfmtEscapeLine(line []byte) string {
	var builder strings.Builder
	for len(line) > 0 {
		r, size := utf8.DecodeRune(line)
		if r == utf8.RuneError && size == 1 {
			_, _ = fmt.Fprintf(&builder, "\\%03o", line[0])
			line = line[1:]
			continue
		}
		if r < utf8.RuneSelf && !unicode.IsGraphic(r) && !unicode.IsSpace(r) {
			_, _ = fmt.Fprintf(&builder, "\\%03o", byte(r))
		} else {
			builder.WriteRune(r)
		}
		line = line[size:]
	}
	return builder.String()
}

func numfmtWriteRawLine(writer io.Writer, line []byte, emitTerminator bool, terminator byte) error {
	if len(line) > 0 {
		if _, err := writer.Write(line); err != nil {
			return numfmtIOError(err)
		}
	}
	if emitTerminator {
		if _, err := writer.Write([]byte{terminator}); err != nil {
			return numfmtIOError(err)
		}
	}
	return nil
}

func numfmtBrokenPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.EPIPE) ||
		strings.Contains(strings.ToLower(err.Error()), "broken pipe")
}

func numfmtIllegalf(format string, args ...any) error {
	return &numfmtError{code: 1, msg: "numfmt: " + fmt.Sprintf(format, args...)}
}

func numfmtFormatf(format string, args ...any) error {
	return &numfmtError{code: 2, msg: "numfmt: " + fmt.Sprintf(format, args...)}
}

func numfmtIOError(err error) error {
	return &numfmtError{code: 1, msg: "numfmt: " + err.Error()}
}

func numfmtExit(inv *Invocation, err error) error {
	if err == nil {
		return nil
	}
	if numfmtBrokenPipe(err) {
		return nil
	}

	var nf *numfmtError
	if errors.As(err, &nf) {
		if nf.msg != "" && inv != nil && inv.Stderr != nil {
			_, _ = fmt.Fprintln(inv.Stderr, nf.msg)
		}
		if nf.msg == "" {
			return &ExitError{Code: nf.code}
		}
		return &ExitError{Code: nf.code, Err: errors.New(nf.msg)}
	}
	return &ExitError{Code: 1, Err: err}
}

var _ Command = (*Numfmt)(nil)
var _ SpecProvider = (*Numfmt)(nil)
var _ ParsedRunner = (*Numfmt)(nil)
