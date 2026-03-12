package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

const (
	seqExponentLimit = 10000
	seqShortestPrec  = 6
)

type Seq struct{}

func NewSeq() *Seq {
	return &Seq{}
}

func (c *Seq) Name() string {
	return "seq"
}

func (c *Seq) Run(ctx context.Context, inv *Invocation) error {
	opts, mode, numbers, err := parseSeqArgs(inv)
	if err != nil {
		return err
	}
	switch mode {
	case "help":
		_, _ = fmt.Fprintln(inv.Stdout, "usage: seq [OPTION]... LAST")
		_, _ = fmt.Fprintln(inv.Stdout, "  or:  seq [OPTION]... FIRST LAST")
		_, _ = fmt.Fprintln(inv.Stdout, "  or:  seq [OPTION]... FIRST INCREMENT LAST")
		return nil
	case "version":
		_, _ = fmt.Fprintln(inv.Stdout, "seq (jbgo)")
		return nil
	}
	if len(numbers) == 0 {
		return exitf(inv, 1, "seq: missing operand")
	}
	if opts.equalWidth && opts.format != "" {
		return exitf(inv, 1, "seq: format string may not be specified when printing equal width strings")
	}

	first := seqOne()
	increment := seqOne()
	last := numbers[len(numbers)-1]

	if len(numbers) >= 2 {
		first = numbers[0]
	}
	if len(numbers) == 3 {
		increment = numbers[1]
	}

	if seqValueIsZero(increment.value) {
		return exitf(inv, 1, "seq: invalid Zero increment value: %q", increment.original)
	}

	precision, hasPrecision := selectSeqPrecision(first, increment, last)
	padding := 0
	if opts.equalWidth && hasPrecision {
		padding = maxInt(first.integralDigits, increment.integralDigits, last.integralDigits)
		if precision > 0 {
			padding += precision + 1
		}
	}

	writer := bufio.NewWriter(inv.Stdout)
	defer func() { _ = writer.Flush() }()

	value := first.value
	wroteAny := false
	for !seqDonePrinting(value, increment.value, last.value) {
		if err := ctx.Err(); err != nil {
			return err
		}
		text, err := formatSeqValue(value, opts, precision, hasPrecision, padding)
		if err != nil {
			return exitf(inv, 1, "seq: %v", err)
		}
		if wroteAny {
			if _, err := writer.WriteString(opts.separator); err != nil {
				if seqBrokenPipe(err) {
					return nil
				}
				return &ExitError{Code: 1, Err: err}
			}
		}
		if _, err := writer.WriteString(text); err != nil {
			if seqBrokenPipe(err) {
				return nil
			}
			return &ExitError{Code: 1, Err: err}
		}
		wroteAny = true

		next, err := seqAdd(value, increment.value)
		if err != nil {
			return exitf(inv, 1, "seq: %v", err)
		}
		value = next
	}

	if wroteAny {
		if _, err := writer.WriteString(opts.terminator); err != nil {
			if seqBrokenPipe(err) {
				return nil
			}
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := writer.Flush(); err != nil {
		if seqBrokenPipe(err) {
			return nil
		}
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

type seqOptions struct {
	separator  string
	terminator string
	equalWidth bool
	format     string
}

type seqNumber struct {
	original       string
	value          seqValue
	integralDigits int
	fractionDigits *int
}

type seqKind int

const (
	seqFinite seqKind = iota
	seqPosInf
	seqNegInf
)

type seqValue struct {
	kind    seqKind
	rat     *big.Rat
	negZero bool
}

func parseSeqArgs(inv *Invocation) (seqOptions, string, []seqNumber, error) {
	opts := seqOptions{
		separator:  "\n",
		terminator: "\n",
	}
	args := append([]string(nil), inv.Args...)
	numbers := make([]seqNumber, 0, 3)
	parsingOptions := true

	for len(args) > 0 {
		arg := args[0]
		args = args[1:]

		if !parsingOptions {
			number, err := parseSeqNumber(arg)
			if err != nil {
				return seqOptions{}, "", nil, seqParseError(inv, arg, err)
			}
			numbers = append(numbers, number)
			continue
		}

		switch {
		case arg == "--help":
			return opts, "help", nil, nil
		case arg == "--version":
			return opts, "version", nil, nil
		case arg == "--":
			parsingOptions = false
		case arg == "-w" || arg == "--equal-width":
			opts.equalWidth = true
		case arg == "-s" || arg == "--separator":
			if len(args) == 0 {
				return seqOptions{}, "", nil, exitf(inv, 1, "seq: option requires an argument -- 's'")
			}
			opts.separator = args[0]
			args = args[1:]
		case arg == "-t" || arg == "--terminator":
			if len(args) == 0 {
				return seqOptions{}, "", nil, exitf(inv, 1, "seq: option requires an argument -- 't'")
			}
			opts.terminator = args[0]
			args = args[1:]
		case arg == "-f" || arg == "--format":
			if len(args) == 0 {
				return seqOptions{}, "", nil, exitf(inv, 1, "seq: option requires an argument -- 'f'")
			}
			opts.format = args[0]
			args = args[1:]
		case strings.HasPrefix(arg, "--separator="):
			opts.separator = strings.TrimPrefix(arg, "--separator=")
		case strings.HasPrefix(arg, "--terminator="):
			opts.terminator = strings.TrimPrefix(arg, "--terminator=")
		case strings.HasPrefix(arg, "--format="):
			opts.format = strings.TrimPrefix(arg, "--format=")
		case len(arg) > 2 && strings.HasPrefix(arg, "-s"):
			opts.separator = arg[2:]
		case len(arg) > 2 && strings.HasPrefix(arg, "-t"):
			opts.terminator = arg[2:]
		case len(arg) > 2 && strings.HasPrefix(arg, "-f"):
			opts.format = arg[2:]
		default:
			parsingOptions = false
			number, err := parseSeqNumber(arg)
			if err != nil {
				return seqOptions{}, "", nil, seqParseError(inv, arg, err)
			}
			numbers = append(numbers, number)
		}
	}

	if len(numbers) > 3 {
		return seqOptions{}, "", nil, exitf(inv, 1, "seq: extra operand %q", numbers[3].original)
	}
	return opts, "", numbers, nil
}

type seqParseKind int

const (
	seqParseFloat seqParseKind = iota
	seqParseNaN
)

type seqParseNumberError struct {
	kind seqParseKind
}

func (e seqParseNumberError) Error() string {
	switch e.kind {
	case seqParseNaN:
		return "not-a-number"
	default:
		return "floating point"
	}
}

func seqParseError(inv *Invocation, arg string, err error) error {
	kind := "floating point"
	var parseErr seqParseNumberError
	if errors.As(err, &parseErr) && parseErr.kind == seqParseNaN {
		kind = "'not-a-number'"
	}
	return exitf(inv, 1, "seq: invalid %s argument: %q", kind, arg)
}

func parseSeqNumber(input string) (seqNumber, error) {
	trimmed := strings.TrimLeftFunc(input, unicode.IsSpace)
	if trimmed == "" {
		return seqNumber{}, seqParseNumberError{kind: seqParseFloat}
	}
	if strings.TrimRightFunc(trimmed, unicode.IsSpace) != trimmed {
		return seqNumber{}, seqParseNumberError{kind: seqParseFloat}
	}

	integralDigits, fractionDigits := seqDigitMetadata(trimmed)
	lower := strings.ToLower(trimmed)
	switch lower {
	case "nan", "+nan", "-nan":
		return seqNumber{}, seqParseNumberError{kind: seqParseNaN}
	case "inf", "+inf", "infinity", "+infinity":
		return seqNumber{original: trimmed, value: seqValue{kind: seqPosInf}, integralDigits: 0, fractionDigits: ptrInt(0)}, nil
	case "-inf", "-infinity":
		return seqNumber{original: trimmed, value: seqValue{kind: seqNegInf}, integralDigits: 0, fractionDigits: ptrInt(0)}, nil
	}

	value, err := parseSeqFiniteValue(trimmed)
	if err != nil {
		return seqNumber{}, err
	}
	return seqNumber{
		original:       trimmed,
		value:          value,
		integralDigits: integralDigits,
		fractionDigits: fractionDigits,
	}, nil
}

func parseSeqFiniteValue(input string) (seqValue, error) {
	sign := 1
	if strings.HasPrefix(input, "+") {
		input = input[1:]
	} else if strings.HasPrefix(input, "-") {
		sign = -1
		input = input[1:]
	}
	lower := strings.ToLower(input)
	if strings.HasPrefix(lower, "0x") {
		return parseSeqHex(lower[2:], sign)
	}
	return parseSeqDecimal(input, sign)
}

func parseSeqDecimal(input string, sign int) (seqValue, error) {
	mantissa := input
	expText := ""
	if idx := strings.IndexAny(input, "eE"); idx >= 0 {
		mantissa = input[:idx]
		expText = input[idx+1:]
	}

	dotIndex := strings.IndexByte(mantissa, '.')
	if dotIndex >= 0 && strings.LastIndexByte(mantissa, '.') != dotIndex {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	intPart := mantissa
	fracPart := ""
	if dotIndex >= 0 {
		intPart = mantissa[:dotIndex]
		fracPart = mantissa[dotIndex+1:]
	}
	if intPart == "" && fracPart == "" {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}
	if intPart == "" {
		intPart = "0"
	}
	if fracPart == "" && strings.Contains(mantissa, ".") {
		fracPart = ""
	}
	if !seqDecimalDigits(intPart) || !seqDecimalDigits(fracPart) {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	exp, expMode, err := seqParseExponent(expText)
	if err != nil {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	allDigits := strings.TrimLeft(intPart+fracPart, "0")
	if allDigits == "" || expMode == seqExpUnderflow {
		return seqValue{
			kind:    seqFinite,
			rat:     new(big.Rat),
			negZero: sign < 0,
		}, nil
	}
	if expMode == seqExpOverflow {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	num := new(big.Int)
	if _, ok := num.SetString(allDigits, 10); !ok {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}
	if sign < 0 {
		num.Neg(num)
	}

	leadingTrimmed := strings.TrimLeft(intPart+fracPart, "0")
	missingLeadingZeros := len(intPart+fracPart) - len(leadingTrimmed)
	scale := len(fracPart) - exp - missingLeadingZeros
	if scale < 0 {
		scale = -scale
		num.Mul(num, seqPow10(scale))
		return seqValue{kind: seqFinite, rat: new(big.Rat).SetInt(num)}, nil
	}

	den := seqPow10(scale)
	return seqValue{kind: seqFinite, rat: new(big.Rat).SetFrac(num, den)}, nil
}

func parseSeqHex(input string, sign int) (seqValue, error) {
	mantissa, expText, hasExponent := strings.Cut(input, "p")
	if !hasExponent {
		expText = ""
	}
	if mantissa == "" {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	dotIndex := strings.IndexByte(mantissa, '.')
	if dotIndex >= 0 && strings.LastIndexByte(mantissa, '.') != dotIndex {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	intPart := mantissa
	fracPart := ""
	if dotIndex >= 0 {
		intPart = mantissa[:dotIndex]
		fracPart = mantissa[dotIndex+1:]
	}
	if intPart == "" && fracPart == "" {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}
	if intPart == "" {
		intPart = "0"
	}
	if !seqHexDigits(intPart) || !seqHexDigits(fracPart) {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	exp, expMode, err := seqParseExponent(expText)
	if err != nil {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}
	if expMode == seqExpUnderflow {
		return seqValue{
			kind:    seqFinite,
			rat:     new(big.Rat),
			negZero: sign < 0,
		}, nil
	}
	if expMode == seqExpOverflow {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}

	digits := strings.TrimLeft(intPart+fracPart, "0")
	if digits == "" {
		return seqValue{
			kind:    seqFinite,
			rat:     new(big.Rat),
			negZero: sign < 0,
		}, nil
	}
	num := new(big.Int)
	if _, ok := num.SetString(digits, 16); !ok {
		return seqValue{}, seqParseNumberError{kind: seqParseFloat}
	}
	if sign < 0 {
		num.Neg(num)
	}

	binaryExp := exp - 4*len(fracPart)
	if binaryExp >= 0 {
		num.Lsh(num, uint(binaryExp))
		return seqValue{kind: seqFinite, rat: new(big.Rat).SetInt(num)}, nil
	}

	den := new(big.Int).Lsh(big.NewInt(1), uint(-binaryExp))
	return seqValue{kind: seqFinite, rat: new(big.Rat).SetFrac(num, den)}, nil
}

type seqExponentMode int

const (
	seqExpNormal seqExponentMode = iota
	seqExpUnderflow
	seqExpOverflow
)

func seqParseExponent(raw string) (int, seqExponentMode, error) {
	if raw == "" {
		return 0, seqExpNormal, nil
	}
	sign := 1
	switch raw[0] {
	case '+':
		raw = raw[1:]
	case '-':
		sign = -1
		raw = raw[1:]
	}
	if raw == "" || !seqDecimalDigits(raw) {
		return 0, seqExpNormal, errors.New("invalid exponent")
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		return 0, seqExpNormal, nil
	}
	if len(raw) > 5 {
		if sign < 0 {
			return 0, seqExpUnderflow, nil
		}
		return 0, seqExpOverflow, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, seqExpNormal, err
	}
	if value > seqExponentLimit {
		if sign < 0 {
			return 0, seqExpUnderflow, nil
		}
		return 0, seqExpOverflow, nil
	}
	return sign * value, seqExpNormal, nil
}

func seqDecimalDigits(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func seqHexDigits(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if ('0' <= r && r <= '9') || ('a' <= r && r <= 'f') || ('A' <= r && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func seqPow10(exp int) *big.Int {
	if exp <= 0 {
		return big.NewInt(1)
	}
	base := big.NewInt(10)
	return new(big.Int).Exp(base, big.NewInt(int64(exp)), nil)
}

func seqDigitMetadata(input string) (integralDigits int, fractionalDigits *int) {
	lower := strings.ToLower(strings.TrimLeftFunc(input, unicode.IsSpace))
	lower = strings.TrimPrefix(lower, "+")

	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "-0x") {
		if strings.Contains(lower, ".") || strings.Contains(lower, "p") {
			return 0, nil
		}
		return 0, ptrInt(0)
	}

	parts := strings.Split(lower, "e")
	whole := parts[0]
	intDigits := len(whole)
	fracDigits := 0
	if dot := strings.IndexByte(whole, '.'); dot >= 0 {
		switch {
		case dot == 0:
			intDigits = 1
		case dot == 1 && strings.HasPrefix(whole, "-"):
			intDigits = 2
		default:
			intDigits = dot
		}
		fracDigits = len(whole) - dot - 1
	}

	if len(parts) == 2 {
		exp, _ := strconv.ParseInt(parts[1], 10, 64)
		if exp > 0 {
			if exp > math.MaxInt32 {
				exp = math.MaxInt32
			}
			intDigits += int(exp)
		}
		if exp < int64(fracDigits) {
			fracDigits = int(int64(fracDigits) - exp)
		} else {
			fracDigits = 0
		}
	}
	return intDigits, ptrInt(fracDigits)
}

func seqOne() seqNumber {
	return seqNumber{
		original:       "1",
		value:          seqValue{kind: seqFinite, rat: big.NewRat(1, 1)},
		integralDigits: 1,
		fractionDigits: ptrInt(0),
	}
}

func selectSeqPrecision(first, increment, last seqNumber) (int, bool) {
	switch {
	case first.fractionDigits == nil || increment.fractionDigits == nil || last.fractionDigits == nil:
		return 0, false
	case *first.fractionDigits == 0 && *increment.fractionDigits == 0 && *last.fractionDigits == 0:
		return 0, true
	default:
		return maxInt(*first.fractionDigits, *increment.fractionDigits), true
	}
}

func seqValueIsZero(value seqValue) bool {
	return value.kind == seqFinite && value.rat.Sign() == 0
}

func seqDonePrinting(next, increment, last seqValue) bool {
	if seqIncrementSign(increment) >= 0 {
		return seqCompare(next, last) > 0
	}
	return seqCompare(next, last) < 0
}

func seqIncrementSign(value seqValue) int {
	switch value.kind {
	case seqPosInf:
		return 1
	case seqNegInf:
		return -1
	default:
		return value.rat.Sign()
	}
}

func seqCompare(left, right seqValue) int {
	switch {
	case left.kind == right.kind && left.kind != seqFinite:
		return 0
	case left.kind == seqNegInf || right.kind == seqPosInf:
		return -1
	case left.kind == seqPosInf || right.kind == seqNegInf:
		return 1
	default:
		return left.rat.Cmp(right.rat)
	}
}

func seqAdd(left, right seqValue) (seqValue, error) {
	switch {
	case left.kind == seqFinite && right.kind == seqFinite:
		sum := new(big.Rat).Add(left.rat, right.rat)
		return seqValue{kind: seqFinite, rat: sum}, nil
	case left.kind == seqFinite:
		return right, nil
	case right.kind == seqFinite:
		return left, nil
	case left.kind == right.kind:
		return left, nil
	default:
		return seqValue{}, errors.New("cannot add opposite infinities")
	}
}

func formatSeqValue(value seqValue, opts seqOptions, precision int, hasPrecision bool, padding int) (string, error) {
	if opts.format != "" {
		return seqFormatWithDirective(value, opts.format)
	}
	if value.kind == seqPosInf {
		return seqPadWidth("inf", padding, false), nil
	}
	if value.kind == seqNegInf {
		return seqPadWidth("-inf", padding, false), nil
	}
	if hasPrecision {
		text := value.rat.FloatString(precision)
		if value.rat.Sign() == 0 && value.negZero && !strings.HasPrefix(text, "-") {
			text = "-" + text
		}
		if opts.equalWidth {
			text = seqPadWidth(text, padding, true)
		}
		return text, nil
	}
	text := seqShortestString(value)
	if opts.equalWidth && padding > 0 {
		text = seqPadWidth(text, padding, false)
	}
	return text, nil
}

func seqShortestString(value seqValue) string {
	if value.kind == seqPosInf {
		return "inf"
	}
	if value.kind == seqNegInf {
		return "-inf"
	}
	if value.rat.Sign() == 0 {
		if value.negZero {
			return "-0"
		}
		return "0"
	}
	prec := uint(value.rat.Num().BitLen() + value.rat.Denom().BitLen() + 64)
	prec = max(prec, 128)
	f := new(big.Float).SetPrec(prec).SetRat(value.rat)
	return f.Text('g', seqShortestPrec)
}

func seqPadWidth(text string, width int, zeroPad bool) string {
	if width <= 0 || len(text) >= width {
		return text
	}
	if !zeroPad {
		return strings.Repeat(" ", width-len(text)) + text
	}
	if strings.HasPrefix(text, "-") {
		return "-" + strings.Repeat("0", width-len(text)) + text[1:]
	}
	return strings.Repeat("0", width-len(text)) + text
}

func seqFormatWithDirective(value seqValue, format string) (string, error) {
	start, end, err := seqFindDirective(format)
	if err != nil {
		return "", err
	}
	if start < 0 {
		return "", fmt.Errorf("format %q has no %% directive", format)
	}

	if value.kind == seqPosInf {
		return format[:start] + "inf" + format[end:], nil
	}
	if value.kind == seqNegInf {
		return format[:start] + "-inf" + format[end:], nil
	}

	f, _ := new(big.Float).SetPrec(256).SetRat(value.rat).Float64()
	return fmt.Sprintf(format, f), nil
}

func seqFindDirective(format string) (start, end int, err error) {
	directiveStart := -1
	directiveEnd := -1

	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if i+1 >= len(format) {
			return -1, -1, fmt.Errorf("format %q ends in %%", format)
		}
		if format[i+1] == '%' {
			i++
			continue
		}
		if directiveStart >= 0 {
			return -1, -1, fmt.Errorf("format %q has too many %% directives", format)
		}
		j := i + 1
		for j < len(format) && strings.ContainsRune(" +-#0", rune(format[j])) {
			j++
		}
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
			j++
		}
		if j < len(format) && format[j] == '.' {
			j++
			for j < len(format) && format[j] >= '0' && format[j] <= '9' {
				j++
			}
		}
		if j >= len(format) {
			return -1, -1, fmt.Errorf("format %q ends in %%", format)
		}
		switch format[j] {
		case 'e', 'E', 'f', 'F', 'g', 'G':
			directiveStart = i
			directiveEnd = j + 1
			i = j
		default:
			return -1, -1, fmt.Errorf("format %q has no %% directive", format)
		}
	}
	if directiveStart < 0 {
		return -1, -1, nil
	}
	return directiveStart, directiveEnd, nil
}

func seqBrokenPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || strings.Contains(strings.ToLower(err.Error()), "broken pipe")
}

func ptrInt(value int) *int {
	v := value
	return &v
}

func maxInt(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	best := values[0]
	for _, value := range values[1:] {
		if value > best {
			best = value
		}
	}
	return best
}

var _ Command = (*Seq)(nil)
