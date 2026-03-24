package builtins

import (
	"math/big"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func parseDecimalBigInt(text string) (*big.Int, bool) {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, false
	}
	return value, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func usesByteLocale(locale string) bool {
	switch strings.ToUpper(strings.TrimSpace(locale)) {
	case "", "C", "POSIX":
		return true
	default:
		return false
	}
}

func normalizeLocaleTag(locale string) string {
	locale = strings.TrimSpace(locale)
	if idx := strings.IndexByte(locale, '@'); idx >= 0 {
		locale = locale[:idx]
	}
	if idx := strings.IndexByte(locale, '.'); idx >= 0 {
		locale = locale[:idx]
	}
	return strings.ReplaceAll(locale, "_", "-")
}

type builtinLocaleContext struct {
	byteLocale bool
	collator   *collate.Collator
}

func newBuiltinLocaleContext(env map[string]string) builtinLocaleContext {
	ctypeLocale := firstNonEmpty(strings.TrimSpace(env["LC_ALL"]), strings.TrimSpace(env["LC_CTYPE"]), strings.TrimSpace(env["LANG"]))
	collateLocale := firstNonEmpty(strings.TrimSpace(env["LC_ALL"]), strings.TrimSpace(env["LC_COLLATE"]), strings.TrimSpace(env["LANG"]))

	ctx := builtinLocaleContext{
		byteLocale: usesByteLocale(ctypeLocale),
	}
	if usesByteLocale(collateLocale) {
		return ctx
	}

	tag, err := language.Parse(normalizeLocaleTag(collateLocale))
	if err != nil {
		return ctx
	}
	ctx.collator = collate.New(tag)
	return ctx
}
