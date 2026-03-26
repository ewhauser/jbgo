package builtins

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
	return RunCommand(ctx, c, inv)
}

func (c *Seq) Spec() CommandSpec {
	return CommandSpec{
		Name:  "seq",
		About: "Print numbers from FIRST to LAST, in steps of INCREMENT.",
		Usage: "seq [OPTION]... LAST",
		AfterHelp: "  or:  seq [OPTION]... FIRST LAST\n" +
			"  or:  seq [OPTION]... FIRST INCREMENT LAST",
		Options: []OptionSpec{
			{Name: "equal-width", Short: 'w', Long: "equal-width", Help: "equalize width by padding with leading zeroes"},
			{Name: "separator", Short: 's', Long: "separator", ValueName: "STRING", Arity: OptionRequiredValue, Help: "use STRING to separate numbers"},
			{Name: "terminator", Short: 't', Long: "terminator", ValueName: "STRING", Arity: OptionRequiredValue, Help: "use STRING to terminate each output line"},
			{Name: "format", Short: 'f', Long: "format", ValueName: "FORMAT", Arity: OptionRequiredValue, Help: "use printf style floating-point FORMAT"},
		},
		Args: []ArgSpec{
			{Name: "number", ValueName: "NUMBER", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			NegativeNumberPositional: true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := fmt.Fprintf(w, "%s\n\nUsage: %s\n\nOptions:\n  -f, --format=FORMAT      use printf style floating-point FORMAT\n  -s, --separator=STRING   use STRING to separate numbers\n  -t, --terminator=STRING  use STRING to terminate each output line\n  -w, --equal-width        equalize width by padding with leading zeroes\n  -h, --help               display this help and exit\n      --version            output version information and exit\n\n%s\n", spec.About, spec.Usage, spec.AfterHelp)
			return err
		},
	}
}

func (c *Seq) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	var opts seqOptions
	opts.separator = "\n"
	opts.terminator = "\n"
	opts.equalWidth = matches.Has("equal-width")
	if matches.Has("separator") {
		opts.separator = matches.Value("separator")
	}
	if matches.Has("terminator") {
		opts.terminator = matches.Value("terminator")
	}
	if matches.Has("format") {
		opts.format = matches.Value("format")
		opts.formatSet = true
	}

	rawNumbers := matches.Args("number")
	if len(rawNumbers) == 0 {
		return seqUsageErrorf(inv, "missing operand")
	}
	if len(rawNumbers) > 3 {
		return seqUsageErrorf(inv, "extra operand %s", quoteGNUOperand(rawNumbers[3]))
	}
	numbers := make([]seqNumber, 0, len(rawNumbers))
	for _, raw := range rawNumbers {
		number, err := parseSeqNumber(raw)
		if err != nil {
			return seqParseError(inv, raw, err)
		}
		numbers = append(numbers, number)
	}
	if opts.equalWidth && opts.formatSet {
		return seqUsageErrorf(inv, "format string may not be specified when printing equal width strings")
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
		return seqUsageErrorf(inv, "invalid Zero increment value: %s", quoteGNUOperand(increment.original))
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
			return seqErrorf(inv, "seq: %v", err)
		}
		if wroteAny {
			if _, err := writer.WriteString(opts.separator); err != nil {
				return seqWriteError(inv, err)
			}
		}
		if _, err := writer.WriteString(text); err != nil {
			return seqWriteError(inv, err)
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
			return seqWriteError(inv, err)
		}
	}
	if err := writer.Flush(); err != nil {
		return seqWriteError(inv, err)
	}
	return nil
}

type seqOptions struct {
	separator  string
	terminator string
	equalWidth bool
	format     string
	formatSet  bool
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
	return seqUsageErrorf(inv, "invalid %s argument: %s", kind, quoteGNUOperand(arg))
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

	scale := len(fracPart) - exp
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
	if opts.formatSet {
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
		return "", fmt.Errorf("format %s has no %% directive", quoteGNUOperand(format))
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
		if directiveStart >= 0 && (i+1 >= len(format) || format[i+1] != '%') {
			return -1, -1, fmt.Errorf("format %s has too many %% directives", quoteGNUOperand(format))
		}
		if i+1 >= len(format) {
			return -1, -1, fmt.Errorf("format %s ends in %%", quoteGNUOperand(format))
		}
		if format[i+1] == '%' {
			i++
			continue
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
			return -1, -1, fmt.Errorf("format %s ends in %%", quoteGNUOperand(format))
		}
		switch format[j] {
		case 'e', 'E', 'f', 'F', 'g', 'G':
			directiveStart = i
			directiveEnd = j + 1
			i = j
		default:
			return -1, -1, fmt.Errorf("format %s has no %% directive", quoteGNUOperand(format))
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

func seqWriteError(inv *Invocation, err error) error {
	if seqBrokenPipe(err) {
		return nil
	}
	if diag, ok := shellWriteErrorDiagnostic("seq", err); ok {
		return seqErrorf(inv, "%s", diag)
	}
	return &ExitError{Code: 1, Err: err}
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

func seqUsageErrorf(inv *Invocation, format string, args ...any) error {
	message := fmt.Sprintf("seq: "+format+"\nTry 'seq --help' for more information.", args...)
	return seqErrorf(inv, "%s", message)
}

func seqErrorf(inv *Invocation, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if inv != nil && inv.Stderr != nil {
		_, _ = fmt.Fprintln(inv.Stderr, message)
	}
	return &ExitError{Code: 1, Err: errors.New(message)}
}

var _ Command = (*Seq)(nil)
