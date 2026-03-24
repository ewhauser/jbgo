package builtins

import "testing"

func TestExprRegexMatchesGNULeadingLiteralOperators(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		text    string
		pattern string
		want    string
	}{
		{name: "leading star", text: "*", pattern: "^*", want: "1"},
		{name: "leading brace quantifier literal", text: "{1}", pattern: "^\\{1\\}", want: "3"},
		{name: "escaped brace literal", text: "{1}", pattern: "\\{1\\}", want: "3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value, err := exprRegexMatch(tt.text, tt.pattern, builtinLocaleContext{})
			if err != nil {
				t.Fatalf("exprRegexMatch() error = %v", err)
			}
			if got := value.text; got != tt.want {
				t.Fatalf("exprRegexMatch() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExprRegexHandlesMultibyteAndInvalidUTF8(t *testing.T) {
	t.Parallel()

	utf8Locale := builtinLocaleContext{byteLocale: false}
	byteLocale := builtinLocaleContext{byteLocale: true}

	value, err := exprRegexMatch("\xce\xb1bc\xce\xb4ef", ".bc", utf8Locale)
	if err != nil {
		t.Fatalf("utf8 exprRegexMatch() error = %v", err)
	}
	if got := value.text; got != "3" {
		t.Fatalf("utf8 exprRegexMatch() = %q, want %q", got, "3")
	}

	value, err = exprRegexMatch("\xcebc\xce\xb4ef", `\(.b\)c`, utf8Locale)
	if err != nil {
		t.Fatalf("invalid UTF-8 exprRegexMatch() error = %v", err)
	}
	if got := value.text; got != "" {
		t.Fatalf("invalid UTF-8 exprRegexMatch() = %q, want empty capture", got)
	}

	value, err = exprRegexMatch("\xce\xb1bc\xce\xb4e", "\\([\xce\xb1]\\)", byteLocale)
	if err != nil {
		t.Fatalf("byte locale exprRegexMatch() error = %v", err)
	}
	if got := value.text; got != "\xce" {
		t.Fatalf("byte locale exprRegexMatch() = %q, want %q", got, "\xce")
	}
}

func TestExprLocaleStringHelpersTreatInvalidUTF8AsSingletons(t *testing.T) {
	t.Parallel()

	if got := exprLocaleLength("\xce\xb1bc\xce\xb4ef", false); got != 6 {
		t.Fatalf("exprLocaleLength(valid UTF-8) = %d, want 6", got)
	}
	if got := exprLocaleLength("\xb1aaa", false); got != 4 {
		t.Fatalf("exprLocaleLength(invalid UTF-8) = %d, want 4", got)
	}
	if got := exprLocaleIndex("\xce\xb1bc\xb4ef", "\xb4", false); got != 4 {
		t.Fatalf("exprLocaleIndex() = %d, want 4", got)
	}
	if got := exprLocaleSubstr("\xce\xb1bc\xb4ef", "3", "3", false); got != "c\xb4e" {
		t.Fatalf("exprLocaleSubstr() = %q, want %q", got, "c\xb4e")
	}
}
