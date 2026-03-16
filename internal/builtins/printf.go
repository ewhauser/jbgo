package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Printf struct{}

func NewPrintf() *Printf {
	return &Printf{}
}

func (c *Printf) Name() string {
	return "printf"
}

func (c *Printf) Run(_ context.Context, inv *Invocation) error {
	if len(inv.Args) == 0 {
		return exitf(inv, 1, "printf: missing format")
	}
	out, err := shellPrintf(inv.Args[0], inv.Args[1:])
	if err != nil {
		return exitf(inv, 1, "printf: %v", err)
	}
	if _, err := fmt.Fprint(inv.Stdout, out); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func shellPrintf(format string, args []string) (string, error) {
	var b strings.Builder
	index := 0
	verbs := countPrintfVerbs(format)
	if verbs == 0 {
		literal, _, err := decodeEscapes(format)
		return literal, err
	}
	if len(args) == 0 {
		args = []string{}
	}
	for first := true; first || index < len(args); first = false {
		chunk, consumed, stop, err := formatPrintfOnce(format, args[index:])
		if err != nil {
			return "", err
		}
		b.WriteString(chunk)
		if stop {
			break
		}
		if consumed == 0 {
			break
		}
		index += consumed
	}
	return b.String(), nil
}

func countPrintfVerbs(format string) int {
	count := 0
	for i := 0; i < len(format); i++ {
		switch format[i] {
		case '\\':
			if i+1 < len(format) {
				i++
			}
		case '%':
			if i+1 < len(format) && format[i+1] == '%' {
				i++
				continue
			}
			count++
			for i+1 < len(format) && !isPrintfVerb(format[i+1]) {
				i++
			}
			if i+1 < len(format) {
				i++
			}
		}
	}
	return count
}

func formatPrintfOnce(format string, args []string) (chunk string, consumed int, stop bool, err error) {
	var (
		b strings.Builder
	)
	for i := 0; i < len(format); i++ {
		switch format[i] {
		case '\\':
			decoded, advance, stop, err := decodeEscapeAtWithStop(format, i)
			if err != nil {
				return "", 0, false, err
			}
			b.WriteString(decoded)
			i = advance
			if stop {
				return b.String(), consumed, true, nil
			}
		case '%':
			if i+1 < len(format) && format[i+1] == '%' {
				b.WriteByte('%')
				i++
				continue
			}
			start := i
			for i+1 < len(format) && !isPrintfVerb(format[i+1]) {
				i++
			}
			if i+1 >= len(format) {
				return "", 0, false, fmt.Errorf("invalid format string")
			}
			i++
			spec := format[start : i+1]
			verb := format[i]
			var arg string
			if consumed < len(args) {
				arg = args[consumed]
			}
			consumed++
			formatted, stop, err := applyPrintfSpec(spec, verb, arg)
			if err != nil {
				return "", 0, false, err
			}
			b.WriteString(formatted)
			if stop {
				return b.String(), consumed, true, nil
			}
		default:
			b.WriteByte(format[i])
		}
	}
	return b.String(), consumed, false, nil
}

func applyPrintfSpec(spec string, verb byte, arg string) (formatted string, stop bool, err error) {
	switch verb {
	case 'b':
		decoded, stop, err := decodeEscapes(arg)
		if err != nil {
			return "", false, err
		}
		if stop {
			return decoded, true, nil
		}
		return fmt.Sprintf(spec[:len(spec)-1]+"s", decoded), false, nil
	case 's', 'q':
		return fmt.Sprintf(spec, arg), false, nil
	case 'c':
		return fmt.Sprintf(spec, printfCharArg(arg)), false, nil
	case 'd', 'i', 'o', 'u', 'x', 'X':
		value := parsePrintfIntArg(arg)
		if verb == 'i' {
			return fmt.Sprintf(spec[:len(spec)-1]+"d", value), false, nil
		}
		if verb == 'u' {
			return fmt.Sprintf(spec[:len(spec)-1]+"d", uint64(value)), false, nil
		}
		return fmt.Sprintf(spec, value), false, nil
	case 'e', 'E', 'f', 'F', 'g', 'G':
		value := parsePrintfFloatArg(arg)
		return fmt.Sprintf(spec, value), false, nil
	default:
		return "", false, fmt.Errorf("unsupported format verb %%%c", verb)
	}
}

func printfCharArg(arg string) rune {
	if arg == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(arg)
	if r == utf8.RuneError && size == 1 {
		return rune(arg[0])
	}
	return r
}

func parsePrintfIntArg(arg string) int64 {
	if value, ok := parsePrintfQuotedCharArg(arg); ok {
		return value
	}
	value, _ := strconv.ParseInt(arg, 0, 64)
	return value
}

func parsePrintfFloatArg(arg string) float64 {
	if value, ok := parsePrintfQuotedCharArg(arg); ok {
		return float64(value)
	}
	value, _ := strconv.ParseFloat(arg, 64)
	return value
}

func parsePrintfQuotedCharArg(arg string) (int64, bool) {
	if len(arg) < 2 {
		return 0, false
	}
	if arg[0] != '\'' && arg[0] != '"' {
		return 0, false
	}
	r, size := utf8.DecodeRuneInString(arg[1:])
	if r == utf8.RuneError && size == 1 {
		return int64(arg[1]), true
	}
	if size == 0 {
		return 0, false
	}
	return int64(r), true
}

func isPrintfVerb(ch byte) bool {
	return strings.ContainsRune("bqcsdiouxXefFgG", rune(ch))
}

func decodeEscapes(s string) (decoded string, stop bool, err error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		decoded, advance, stop, err := decodeEscapeAtWithStop(s, i)
		if err != nil {
			return "", false, err
		}
		b.WriteString(decoded)
		i = advance
		if stop {
			return b.String(), true, nil
		}
	}
	return b.String(), false, nil
}

func decodeEscapeAtWithStop(s string, i int) (decoded string, advance int, stop bool, err error) {
	if i+1 >= len(s) {
		return "\\", i, false, nil
	}
	switch s[i+1] {
	case 'a':
		return "\a", i + 1, false, nil
	case 'b':
		return "\b", i + 1, false, nil
	case 'c':
		return "", i + 1, true, nil
	case 'f':
		return "\f", i + 1, false, nil
	case 'n':
		return "\n", i + 1, false, nil
	case 'r':
		return "\r", i + 1, false, nil
	case 't':
		return "\t", i + 1, false, nil
	case 'v':
		return "\v", i + 1, false, nil
	case '\\':
		return "\\", i + 1, false, nil
	case 'x':
		end := i + 2
		for end < len(s) && end < i+4 && isHexDigit(s[end]) {
			end++
		}
		if end == i+2 {
			return "", i, false, fmt.Errorf("invalid hex escape")
		}
		value, err := strconv.ParseUint(s[i+2:end], 16, 8)
		if err != nil {
			return "", i, false, err
		}
		return string([]byte{byte(value)}), end - 1, false, nil
	case '1', '2', '3', '4', '5', '6', '7':
		end := i + 1
		for end < len(s) && end < i+4 && s[end] >= '0' && s[end] <= '7' {
			end++
		}
		value, err := strconv.ParseUint(s[i+1:end], 8, 8)
		if err != nil {
			return "", i, false, err
		}
		return string([]byte{byte(value)}), end - 1, false, nil
	case '0':
		end := i + 2
		for end < len(s) && end < i+5 && s[end] >= '0' && s[end] <= '7' {
			end++
		}
		value, err := strconv.ParseUint(s[i+1:end], 8, 8)
		if err != nil {
			return "", i, false, err
		}
		return string([]byte{byte(value)}), end - 1, false, nil
	default:
		return string(s[i+1]), i + 1, false, nil
	}
}

func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

var _ Command = (*Printf)(nil)
