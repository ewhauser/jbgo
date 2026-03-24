package runtime

import (
	"fmt"
	"strings"
	"testing"
)

func FuzzExprCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	f.Add("12", "*", "4", "abcdef", "./tests/init.sh", ".*/\\(.*\\)$", "\xce\xb1bcdef")
	f.Add("0", "&", "1 / 0", "\xce\xb1bc\xce\xb4ef", "\xce\xb1bc\xce\xb4ef", "\\(.b\\)c", "\xcebc\xce\xb4ef")
	f.Add("value", "|", "fallback", "00", "abbccd", "a\\(\\([bc]\\)\\2\\)*d", "substr")

	f.Fuzz(func(t *testing.T, left, op, right, keywordArg, regexText, regexPattern, deepToken string) {
		session := newFuzzSession(t, rt)

		left = sanitizeExprFuzzToken(left)
		op = sanitizeExprFuzzToken(op)
		right = sanitizeExprFuzzToken(right)
		keywordArg = sanitizeExprFuzzToken(keywordArg)
		regexText = sanitizeExprFuzzToken(regexText)
		regexPattern = sanitizeExprFuzzToken(regexPattern)
		deepToken = sanitizeExprFuzzToken(deepToken)

		deepExpr := make([]string, 0, 17)
		for i := range 9 {
			deepExpr = append(deepExpr, shellQuote(deepToken))
			if i < 8 {
				deepExpr = append(deepExpr, "'+'")
			}
		}

		script := fmt.Appendf(nil,
			"expr %s %s %s >/tmp/expr-basic.out 2>/tmp/expr-basic.err || true\n"+
				"expr length %s >/tmp/expr-length.out 2>/tmp/expr-length.err || true\n"+
				"expr substr %s 1 2 >/tmp/expr-substr.out 2>/tmp/expr-substr.err || true\n"+
				"expr match %s %s >/tmp/expr-match.out 2>/tmp/expr-match.err || true\n"+
				"expr '(' %s %s %s ')' >/tmp/expr-group.out 2>/tmp/expr-group.err || true\n"+
				"expr %s >/tmp/expr-deep.out 2>/tmp/expr-deep.err || true\n",
			shellQuote(left),
			shellQuote(op),
			shellQuote(right),
			shellQuote(keywordArg),
			shellQuote(keywordArg),
			shellQuote(regexText),
			shellQuote(regexPattern),
			shellQuote(left),
			shellQuote(op),
			shellQuote(right),
			strings.Join(deepExpr, " "),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}

func sanitizeExprFuzzToken(raw string) string {
	raw = strings.ReplaceAll(raw, "\x00", "")
	if raw == "" {
		return "value"
	}
	if len(raw) > 32 {
		raw = raw[:32]
	}
	return raw
}
