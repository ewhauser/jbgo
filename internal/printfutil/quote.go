package printfutil

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func quoteShell(s string, opts Options) string {
	if opts.Dialect == DialectGNU {
		return quoteShellGNU(s, localeUsesUTF8(opts))
	}
	return quoteShellBash(s)
}

func quoteShellBash(s string) string {
	if s == "" {
		return "''"
	}
	if needsANSIQuote(s) {
		return quoteANSI(s)
	}
	if isSafeRawWord(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if needsBackslashEscape(r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func quoteShellGNU(s string, utf8Locale bool) string {
	if s == "" {
		return "''"
	}
	reference := s

	quotes := byte('\'')
	mustQuote := false
	if gnuShellHasInitialQuotePressure(reference) {
		mustQuote = true
	} else if strings.ContainsRune(reference, '\'') {
		quotes = '"'
		mustQuote = true
	}

	var b strings.Builder
	inDollar := false
	enterDollar := func() {
		if inDollar {
			return
		}
		b.WriteString("'$'")
		inDollar = true
	}
	exitDollar := func() {
		if !inDollar {
			return
		}
		b.WriteString("''")
		inDollar = false
	}

	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == utf8.RuneError && size == 1:
			enterDollar()
			mustQuote = true
			fmt.Fprintf(&b, "\\%03o", s[0])
		case !utf8Locale && s[0] >= 0x80:
			enterDollar()
			mustQuote = true
			fmt.Fprintf(&b, "\\%03o", s[0])
			size = 1
		default:
			switch state, ch := classifyGNUShellChar(r, quotes, utf8Locale); state {
			case gnuEscapeChar:
				exitDollar()
				b.WriteRune(ch)
			case gnuEscapeForceQuote:
				exitDollar()
				mustQuote = true
				b.WriteRune(ch)
			case gnuEscapeQuotedSingle:
				mustQuote = true
				inDollar = false
				b.WriteString("'\\''")
			case gnuEscapeDollar:
				enterDollar()
				mustQuote = true
				b.WriteByte('\\')
				b.WriteRune(ch)
			case gnuEscapeOctal:
				enterDollar()
				mustQuote = true
				for _, raw := range []byte(s[:size]) {
					fmt.Fprintf(&b, "\\%03o", raw)
				}
			}
		}
		s = s[size:]
	}

	if !mustQuote && !gnuSpecialShellStart(reference) {
		return b.String()
	}
	var quoted strings.Builder
	quoted.Grow(b.Len() + 2)
	quoted.WriteByte(quotes)
	quoted.WriteString(b.String())
	quoted.WriteByte(quotes)
	return quoted.String()
}

func needsANSIQuote(s string) bool {
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == utf8.RuneError && size == 1:
			return true
		case r == '\n' || r == '\r' || r == '\t' || r == '\v' || r == '\f' || r == '\a' || r == '\b':
			return true
		case !unicode.IsPrint(r):
			return true
		}
		s = s[size:]
	}
	return false
}

func isSafeRawWord(s string) bool {
	if syntax.IsKeyword(s) {
		return false
	}
	for _, r := range s {
		if needsBackslashEscape(r) {
			return false
		}
	}
	return true
}

func needsBackslashEscape(r rune) bool {
	switch r {
	case ' ', '!', '"', '#', '$', '&', '\'', '(', ')', '*', ';', '<', '=', '>', '?', '[', '\\', ']', '`', '{', '|', '}', '~':
		return true
	default:
		return false
	}
}

func quoteANSI(s string) string {
	var b strings.Builder
	b.WriteString("$'")
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, "\\%03o", s[0])
		case r == '\'' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\a':
			b.WriteString(`\a`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\v':
			b.WriteString(`\v`)
		case !unicode.IsPrint(r):
			for _, raw := range []byte(s[:size]) {
				fmt.Fprintf(&b, "\\%03o", raw)
			}
		default:
			b.WriteRune(r)
		}
		s = s[size:]
	}
	b.WriteByte('\'')
	return b.String()
}

type gnuEscapeState uint8

const (
	gnuEscapeChar gnuEscapeState = iota
	gnuEscapeForceQuote
	gnuEscapeQuotedSingle
	gnuEscapeDollar
	gnuEscapeOctal
)

func gnuShellHasInitialQuotePressure(s string) bool {
	for _, b := range []byte(s) {
		if b == '"' || b == '`' || b == '$' || b == '\\' || b == '^' || b == '\n' || b == '\t' || b == '\r' || b == '=' || b < 0x20 || b == 0x7f {
			return true
		}
	}
	return false
}

func gnuSpecialShellStart(s string) bool {
	if s == "" {
		return false
	}
	return s[0] == '~' || s[0] == '#'
}

func classifyGNUShellChar(r rune, quotes byte, utf8Locale bool) (gnuEscapeState, rune) {
	switch r {
	case '\a':
		return gnuEscapeDollar, 'a'
	case '\b':
		return gnuEscapeDollar, 'b'
	case '\t':
		return gnuEscapeDollar, 't'
	case '\n':
		return gnuEscapeDollar, 'n'
	case '\v':
		return gnuEscapeDollar, 'v'
	case '\f':
		return gnuEscapeDollar, 'f'
	case '\r':
		return gnuEscapeDollar, 'r'
	case '\'':
		if quotes == '\'' {
			return gnuEscapeQuotedSingle, r
		}
		return gnuEscapeChar, r
	}
	if r < 0x20 || r == 0x7f {
		return gnuEscapeOctal, r
	}
	if utf8Locale && !unicode.IsPrint(r) {
		return gnuEscapeOctal, r
	}
	if strings.ContainsRune("`$&*()|[;\\'\"<>?! ", r) {
		return gnuEscapeForceQuote, r
	}
	return gnuEscapeChar, r
}

func localeUsesUTF8(opts Options) bool {
	if opts.LookupEnv == nil {
		return false
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value, ok := opts.LookupEnv(name)
		if !ok || value == "" {
			continue
		}
		upper := strings.ToUpper(value)
		return strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
	}
	return false
}
