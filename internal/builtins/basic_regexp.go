package builtins

import "strings"

// translateBasicRegexp rewrites a POSIX BRE pattern into the closest RE2 form.
// It only translates the BRE operators this codebase relies on today.
func translateBasicRegexp(pattern string) string {
	var b strings.Builder
	escaped := false
	for _, r := range pattern {
		if escaped {
			switch r {
			case '(', ')', '{', '}', '+', '?', '|':
				b.WriteRune(r)
			default:
				b.WriteByte('\\')
				b.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '(', ')', '{', '}', '+', '?', '|':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteString(`\\`)
	}
	return b.String()
}
