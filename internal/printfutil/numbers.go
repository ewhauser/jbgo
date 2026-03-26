package printfutil

import (
	"fmt"
	"math"
	"math/big"
	"runtime"
	"strconv"
	"strings"
)

var (
	maxInt64Big  = big.NewInt(math.MaxInt64)
	minInt64Big  = big.NewInt(math.MinInt64)
	maxUint64Big = new(big.Int).SetUint64(math.MaxUint64)
	twoTo64Big   = new(big.Int).Add(maxUint64Big, big.NewInt(1))
)

type numericParse struct {
	value    *big.Int
	diagnose string
}

type gnuNumericParse struct {
	value    *big.Int
	warning  string
	diagnose string
}

func parseWidthArg(arg string, present bool, opts Options) (int, bool, string, bool) {
	if opts.Dialect == DialectGNU {
		return parseGNUFieldArg(arg, present, opts, "field width")
	}
	value, ok, diag := parseShellWidthArg(arg, present)
	return value, ok, diag, false
}

func parsePrecisionArg(arg string, present bool, opts Options) (int, bool, string, bool) {
	if opts.Dialect == DialectGNU {
		return parseGNUFieldArg(arg, present, opts, "precision")
	}
	value, ok, diag := parseShellWidthArg(arg, present)
	return value, ok, diag, false
}

func parseShellWidthArg(arg string, present bool) (int, bool, string) {
	if !present {
		return 0, true, ""
	}
	parsed := parseInteger(arg, true)
	if parsed.diagnose != "" {
		return clampInt(parsed.value), false, parsed.diagnose
	}
	return clampInt(parsed.value), true, ""
}

func parseGNUFieldArg(arg string, present bool, opts Options, label string) (int, bool, string, bool) {
	if !present {
		return 0, true, "", false
	}
	parsed := parseGNUInteger(arg, true, opts)
	if parsed.diagnose != "" {
		return clampGNUInt(parsed.value), false, fmt.Sprintf("invalid %s: %s", label, quoteGNUDiagnosticOperand(arg)), false
	}
	value := clampGNUInt(parsed.value)
	if parsed.value != nil {
		maxLimit := big.NewInt(int64(gnuMaxArgIndex))
		if parsed.value.Sign() < 0 {
			return value, true, "", false
		}
		if parsed.value.Cmp(maxLimit) > 0 {
			return value, false, fmt.Sprintf("invalid %s: %s", label, quoteGNUDiagnosticOperand(arg)), true
		}
	}
	return value, true, "", false
}

func clampInt(value *big.Int) int {
	if value == nil {
		return 0
	}
	if value.Cmp(maxInt64Big) > 0 {
		return math.MaxInt
	}
	if value.Cmp(minInt64Big) < 0 {
		return math.MinInt
	}
	return int(value.Int64())
}

func clampGNUInt(value *big.Int) int {
	if value == nil {
		return 0
	}
	maxLimit := big.NewInt(int64(gnuMaxArgIndex))
	minLimit := big.NewInt(-int64(gnuMaxArgIndex))
	switch {
	case value.Cmp(maxLimit) > 0:
		return gnuMaxArgIndex
	case value.Cmp(minLimit) < 0:
		return -gnuMaxArgIndex
	default:
		return int(value.Int64())
	}
}

func formatSigned(arg string, present bool, spec *formatSpec, opts Options) (string, string, string) {
	if opts.Dialect == DialectGNU {
		return formatSignedGNU(arg, present, spec, opts)
	}
	return formatSignedShell(arg, present, spec)
}

func formatSignedShell(arg string, present bool, spec *formatSpec) (string, string, string) {
	parsed := parseInteger(arg, present)
	value := int64(0)
	diag := parsed.diagnose
	if parsed.value != nil {
		switch {
		case parsed.value.Cmp(maxInt64Big) > 0:
			value = math.MaxInt64
			if diag == "" {
				diag = overflowDiagnostic(arg)
			}
		case parsed.value.Cmp(minInt64Big) < 0:
			value = math.MinInt64
			if diag == "" {
				diag = overflowDiagnostic(arg)
			}
		default:
			value = parsed.value.Int64()
		}
	}
	return fmt.Sprintf(buildNumericFormat(spec, 'd'), value), diag, ""
}

func formatSignedGNU(arg string, present bool, spec *formatSpec, opts Options) (string, string, string) {
	parsed := parseGNUInteger(arg, present, opts)
	value := int64(0)
	diag := parsed.diagnose
	if parsed.value != nil {
		switch {
		case parsed.value.Cmp(maxInt64Big) > 0:
			value = math.MaxInt64
			if diag == "" {
				diag = gnuOverflowDiagnostic(arg)
			}
		case parsed.value.Cmp(minInt64Big) < 0:
			value = math.MinInt64
			if diag == "" {
				diag = gnuOverflowDiagnostic(arg)
			}
		default:
			value = parsed.value.Int64()
		}
	}
	return fmt.Sprintf(buildNumericFormat(spec, 'd'), value), diag, parsed.warning
}

func formatUnsigned(arg string, present bool, spec *formatSpec, opts Options) (string, string, string) {
	if opts.Dialect == DialectGNU {
		return formatUnsignedGNU(arg, present, spec, opts)
	}
	return formatUnsignedShell(arg, present, spec)
}

func formatUnsignedShell(arg string, present bool, spec *formatSpec) (string, string, string) {
	parsed := parseInteger(arg, present)
	value := uint64(0)
	diag := parsed.diagnose
	if parsed.value != nil {
		switch {
		case parsed.value.Sign() >= 0 && parsed.value.Cmp(maxUint64Big) > 0:
			value = math.MaxUint64
			if diag == "" {
				diag = overflowDiagnostic(arg)
			}
		case parsed.value.Sign() < 0:
			abs := new(big.Int).Neg(parsed.value)
			if abs.Cmp(maxUint64Big) > 0 {
				value = math.MaxUint64
				if diag == "" {
					diag = overflowDiagnostic(arg)
				}
			} else {
				mod := new(big.Int).Mod(parsed.value, twoTo64Big)
				value = mod.Uint64()
			}
		default:
			value = parsed.value.Uint64()
		}
	}
	verb := spec.verb
	if verb == 'u' {
		verb = 'd'
	}
	unsignedSpec := *spec
	if value == 0 && unsignedSpec.alternate && (verb == 'x' || verb == 'X') {
		unsignedSpec.alternate = false
	}
	return fmt.Sprintf(buildNumericFormat(&unsignedSpec, verb), value), diag, ""
}

func formatUnsignedGNU(arg string, present bool, spec *formatSpec, opts Options) (string, string, string) {
	parsed := parseGNUInteger(arg, present, opts)
	value := uint64(0)
	diag := parsed.diagnose
	if parsed.value != nil {
		switch {
		case parsed.value.Sign() >= 0 && parsed.value.Cmp(maxUint64Big) > 0:
			value = math.MaxUint64
			if diag == "" {
				diag = gnuOverflowDiagnostic(arg)
			}
		case parsed.value.Sign() < 0:
			abs := new(big.Int).Neg(parsed.value)
			if abs.Cmp(maxUint64Big) > 0 {
				value = math.MaxUint64
				if diag == "" {
					diag = gnuOverflowDiagnostic(arg)
				}
			} else {
				mod := new(big.Int).Mod(parsed.value, twoTo64Big)
				value = mod.Uint64()
			}
		default:
			value = parsed.value.Uint64()
		}
	}
	verb := spec.verb
	if verb == 'u' {
		verb = 'd'
	}
	unsignedSpec := *spec
	if value == 0 && unsignedSpec.alternate && (verb == 'x' || verb == 'X') {
		unsignedSpec.alternate = false
	}
	return fmt.Sprintf(buildNumericFormat(&unsignedSpec, verb), value), diag, parsed.warning
}

func formatFloat(arg string, present bool, spec *formatSpec, opts Options) (string, string, string) {
	if opts.Dialect == DialectGNU {
		return formatFloatGNU(arg, present, spec, opts)
	}
	return formatFloatShell(arg, present, spec)
}

func formatFloatShell(arg string, present bool, spec *formatSpec) (string, string, string) {
	if present {
		if value, ok := parseQuotedCharArg(arg); ok {
			return fmt.Sprintf(buildNumericFormat(spec, spec.verb), float64(value)), "", ""
		}
	}
	if !present {
		return fmt.Sprintf(buildNumericFormat(spec, spec.verb), 0.0), "", ""
	}

	trimmed := strings.TrimLeft(arg, " \t\r\n\v\f")
	if trimmed == "" {
		return fmt.Sprintf(buildNumericFormat(spec, spec.verb), 0.0), fmt.Sprintf("%s: invalid number", arg), ""
	}
	prefix := floatPrefix(trimmed)
	if prefix == "" {
		return fmt.Sprintf(buildNumericFormat(spec, spec.verb), 0.0), fmt.Sprintf("%s: invalid number", arg), ""
	}
	value, err := strconv.ParseFloat(prefix, 64)
	diag := ""
	if err != nil || prefix != trimmed {
		diag = fmt.Sprintf("%s: invalid number", arg)
	}
	return fmt.Sprintf(buildNumericFormat(spec, spec.verb), value), diag, ""
}

func formatFloatGNU(arg string, present bool, spec *formatSpec, opts Options) (string, string, string) {
	if present {
		if parsed, ok := parseGNUCharacterConstant(arg, opts); ok {
			if parsed.valid {
				return fmt.Sprintf(buildNumericFormat(spec, spec.verb), float64(parsed.value)), "", parsed.warning
			}
			return fmt.Sprintf(buildNumericFormat(spec, spec.verb), 0.0), parsed.diagnose, ""
		}
	}
	if !present {
		return fmt.Sprintf(buildNumericFormat(spec, spec.verb), 0.0), "", ""
	}

	trimmed := strings.TrimLeft(arg, " \t\r\n\v\f")
	if trimmed == "" {
		return fmt.Sprintf(buildNumericFormat(spec, spec.verb), 0.0), fmt.Sprintf("%s: expected a numeric value", quoteGNUDiagnosticOperand(arg)), ""
	}
	prefix := floatPrefix(trimmed)
	if prefix == "" {
		return fmt.Sprintf(buildNumericFormat(spec, spec.verb), 0.0), fmt.Sprintf("%s: expected a numeric value", quoteGNUDiagnosticOperand(arg)), ""
	}
	value, err := strconv.ParseFloat(prefix, 64)
	diag := ""
	switch {
	case err != nil:
		diag = overflowDiagnostic(arg)
	case prefix != trimmed:
		diag = fmt.Sprintf("%s: value not completely converted", quoteGNUDiagnosticOperand(arg))
	}
	return fmt.Sprintf(buildNumericFormat(spec, spec.verb), value), diag, ""
}

func floatPrefix(s string) string {
	start := 0
	if s != "" && (s[0] == '+' || s[0] == '-') {
		start = 1
	}
	i := start
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return ""
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		expStart := i
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expDigits := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			expDigits++
		}
		if expDigits == 0 {
			i = expStart
		}
	}
	return s[:i]
}

func buildNumericFormat(spec *formatSpec, verb byte) string {
	var b strings.Builder
	b.WriteByte('%')
	if spec.alternate {
		b.WriteByte('#')
	}
	if spec.forceSign {
		b.WriteByte('+')
	}
	if spec.spaceSign {
		b.WriteByte(' ')
	}
	if spec.leftJustify {
		b.WriteByte('-')
	}
	if spec.zeroPad {
		b.WriteByte('0')
	}
	if spec.widthSet {
		b.WriteString(strconv.Itoa(spec.width))
	}
	if spec.precisionSet {
		b.WriteByte('.')
		b.WriteString(strconv.Itoa(spec.precision))
	}
	b.WriteByte(verb)
	return b.String()
}

func parseInteger(arg string, present bool) numericParse {
	if !present {
		return numericParse{value: big.NewInt(0)}
	}
	if value, ok := parseQuotedCharArg(arg); ok {
		return numericParse{value: big.NewInt(value)}
	}

	trimmed := strings.TrimLeft(arg, " \t\r\n\v\f")
	if trimmed == "" {
		return numericParse{
			value:    big.NewInt(0),
			diagnose: fmt.Sprintf("%s: invalid number", arg),
		}
	}

	sign := 1
	switch trimmed[0] {
	case '+':
		trimmed = trimmed[1:]
	case '-':
		sign = -1
		trimmed = trimmed[1:]
	}

	base := 10
	prefixLen := 0
	switch {
	case strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X"):
		base = 16
		prefixLen = 2
	case strings.HasPrefix(trimmed, "0"):
		base = 8
		prefixLen = 1
	}

	startDigits := trimmed[prefixLen:]
	endDigits := 0
	for endDigits < len(startDigits) && validDigit(startDigits[endDigits], base) {
		endDigits++
	}

	digits := startDigits[:endDigits]
	if prefixLen == 1 {
		digits = "0" + digits
	}

	rest := startDigits[endDigits:]
	if prefixLen == 2 && digits == "" {
		rest = trimmed[prefixLen:]
	}
	invalid := rest != ""
	if digits == "" {
		invalid = true
		digits = "0"
	}

	value := new(big.Int)
	value.SetString(digits, base)
	if sign < 0 {
		value.Neg(value)
	}
	if invalid {
		return numericParse{
			value:    value,
			diagnose: fmt.Sprintf("%s: invalid number", arg),
		}
	}
	return numericParse{value: value}
}

func parseGNUInteger(arg string, present bool, opts Options) gnuNumericParse {
	if !present {
		return gnuNumericParse{value: big.NewInt(0)}
	}
	if parsed, ok := parseGNUCharacterConstant(arg, opts); ok {
		if parsed.valid {
			return gnuNumericParse{
				value:   big.NewInt(parsed.value),
				warning: parsed.warning,
			}
		}
		return gnuNumericParse{
			value:    big.NewInt(0),
			diagnose: parsed.diagnose,
		}
	}

	trimmed := strings.TrimLeft(arg, " \t\r\n\v\f")
	if trimmed == "" {
		return gnuNumericParse{
			value:    big.NewInt(0),
			diagnose: fmt.Sprintf("%s: expected a numeric value", quoteGNUDiagnosticOperand(arg)),
		}
	}

	sign := 1
	switch trimmed[0] {
	case '+':
		trimmed = trimmed[1:]
	case '-':
		sign = -1
		trimmed = trimmed[1:]
	}

	base := 10
	prefixLen := 0
	switch {
	case strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X"):
		base = 16
		prefixLen = 2
	case strings.HasPrefix(trimmed, "0"):
		base = 8
		prefixLen = 1
	}

	startDigits := trimmed[prefixLen:]
	endDigits := 0
	for endDigits < len(startDigits) && validDigit(startDigits[endDigits], base) {
		endDigits++
	}

	digits := startDigits[:endDigits]
	if prefixLen == 1 {
		digits = "0" + digits
	}
	if prefixLen == 2 && digits == "" {
		digits = "0"
	}
	hadDigits := endDigits > 0 || prefixLen == 1

	value := new(big.Int)
	value.SetString(digits, base)
	if sign < 0 {
		value.Neg(value)
	}

	switch {
	case !hadDigits:
		return gnuNumericParse{
			value:    big.NewInt(0),
			diagnose: fmt.Sprintf("%s: expected a numeric value", quoteGNUDiagnosticOperand(arg)),
		}
	case endDigits != len(startDigits):
		return gnuNumericParse{
			value:    value,
			diagnose: fmt.Sprintf("%s: value not completely converted", quoteGNUDiagnosticOperand(arg)),
		}
	default:
		return gnuNumericParse{value: value}
	}
}

func validDigit(ch byte, base int) bool {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch-'0') < base
	case ch >= 'a' && ch <= 'f':
		return 10+int(ch-'a') < base
	case ch >= 'A' && ch <= 'F':
		return 10+int(ch-'A') < base
	default:
		return false
	}
}

func parseQuotedCharArg(arg string) (int64, bool) {
	if arg == "" {
		return 0, false
	}
	if arg[0] != '\'' && arg[0] != '"' {
		return 0, false
	}
	if len(arg) == 1 {
		return 0, true
	}
	return int64(decodeShellCharValue([]byte(arg[1:]))), true
}

type gnuCharacterConstant struct {
	value    int64
	warning  string
	diagnose string
	valid    bool
}

func parseGNUCharacterConstant(arg string, opts Options) (gnuCharacterConstant, bool) {
	if arg == "" || (arg[0] != '\'' && arg[0] != '"') {
		return gnuCharacterConstant{}, false
	}
	if len(arg) == 1 {
		return gnuCharacterConstant{
			diagnose: fmt.Sprintf("%s: expected a numeric value", quoteGNUDiagnosticOperand(arg)),
		}, true
	}
	value, consumed := decodeGNUQuotedCharValue([]byte(arg[1:]), localeUsesUTF8(opts))
	rest := arg[1+consumed:]
	warning := ""
	if rest != "" {
		warning = fmt.Sprintf("warning: %s: character(s) following character constant have been ignored", rest)
	}
	return gnuCharacterConstant{
		value:   int64(value),
		warning: warning,
		valid:   true,
	}, true
}

func decodeShellCharValue(data []byte) uint32 {
	if len(data) == 0 {
		return 0
	}
	if data[0] < 0x80 {
		return uint32(data[0])
	}
	if len(data) >= 2 && data[0] >= 0xc2 && data[0] <= 0xdf && isContinuation(data[1]) {
		value := uint32(data[0]&0x1f)<<6 | uint32(data[1]&0x3f)
		if value < 0x80 {
			return uint32(data[0])
		}
		return value
	}
	if len(data) >= 3 && data[0] >= 0xe0 && data[0] <= 0xef &&
		isContinuation(data[1]) && isContinuation(data[2]) {
		value := uint32(data[0]&0x0f)<<12 | uint32(data[1]&0x3f)<<6 | uint32(data[2]&0x3f)
		if value < 0x800 || (value >= 0xd800 && value <= 0xdfff) {
			return uint32(data[0])
		}
		return value
	}
	if len(data) >= 4 && data[0] >= 0xf0 && data[0] <= 0xf7 &&
		isContinuation(data[1]) && isContinuation(data[2]) && isContinuation(data[3]) {
		value := uint32(data[0]&0x07)<<18 | uint32(data[1]&0x3f)<<12 | uint32(data[2]&0x3f)<<6 | uint32(data[3]&0x3f)
		if value < 0x10000 || (value > 0x10ffff && runtime.GOOS != "linux") {
			return uint32(data[0])
		}
		return value
	}
	return uint32(data[0])
}

func decodeGNUQuotedCharValue(data []byte, utf8Locale bool) (uint32, int) {
	if len(data) == 0 {
		return 0, 0
	}
	if !utf8Locale || data[0] < 0x80 {
		return uint32(data[0]), 1
	}
	if len(data) >= 2 && data[0] >= 0xc2 && data[0] <= 0xdf && isContinuation(data[1]) {
		value := uint32(data[0]&0x1f)<<6 | uint32(data[1]&0x3f)
		if value >= 0x80 {
			return value, 2
		}
	}
	if len(data) >= 3 && data[0] >= 0xe0 && data[0] <= 0xef &&
		isContinuation(data[1]) && isContinuation(data[2]) {
		value := uint32(data[0]&0x0f)<<12 | uint32(data[1]&0x3f)<<6 | uint32(data[2]&0x3f)
		if value >= 0x800 && (value < 0xd800 || value > 0xdfff) {
			return value, 3
		}
	}
	if len(data) >= 4 && data[0] >= 0xf0 && data[0] <= 0xf7 &&
		isContinuation(data[1]) && isContinuation(data[2]) && isContinuation(data[3]) {
		value := uint32(data[0]&0x07)<<18 | uint32(data[1]&0x3f)<<12 | uint32(data[2]&0x3f)<<6 | uint32(data[3]&0x3f)
		if value >= 0x10000 && value <= 0x10ffff {
			return value, 4
		}
	}
	return uint32(data[0]), 1
}

func isContinuation(b byte) bool {
	return b&0xc0 == 0x80
}

func overflowDiagnostic(arg string) string {
	message := "Result too large"
	if runtime.GOOS == "linux" {
		message = "Numerical result out of range"
	}
	return fmt.Sprintf("%s: %s", arg, message)
}

func gnuOverflowDiagnostic(arg string) string {
	message := "Result too large"
	if runtime.GOOS == "linux" {
		message = "Numerical result out of range"
	}
	return fmt.Sprintf("%s: %s", quoteGNUDiagnosticOperand(arg), message)
}
