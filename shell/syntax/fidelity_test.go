package syntax

import (
	"strings"
	"testing"
)

func parseTestFile(t *testing.T, src string) *File {
	t.Helper()

	file, err := NewParser().Parse(strings.NewReader(src), "fidelity.sh")
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", src, err)
	}
	return file
}

func parseTestFileVariant(t *testing.T, variant LangVariant, src string) *File {
	t.Helper()

	file, err := NewParser(Variant(variant)).Parse(strings.NewReader(src), "fidelity.sh")
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", src, err)
	}
	return file
}

func TestPatternQuoteFidelityFromExtGlobRawParse(t *testing.T) {
	t.Parallel()

	src := "[[ $x == @(\"a\"|\\*|b*) ]]\n"
	file := parseTestFile(t, src)
	testClause, ok := file.Stmts[0].Cmd.(*TestClause)
	if !ok {
		t.Fatalf("Cmd = %T, want *TestClause", file.Stmts[0].Cmd)
	}
	binary, ok := testClause.X.(*CondBinary)
	if !ok {
		t.Fatalf("cond = %T, want *CondBinary", testClause.X)
	}
	root := binary.Y.(*CondPattern).Pattern
	if got := root.RawText(); got != "@(\"a\"|\\*|b*)" {
		t.Fatalf("root.RawText() = %q, want %q", got, "@(\"a\"|\\*|b*)")
	}

	extglob, ok := root.Parts[0].(*ExtGlob)
	if !ok {
		t.Fatalf("root.Parts[0] = %T, want *ExtGlob", root.Parts[0])
	}

	tests := []struct {
		pattern       *Pattern
		wantRaw       string
		wantUnquoted  string
		wantWasQuoted bool
	}{
		{pattern: extglob.Patterns[0], wantRaw: "\"a\"", wantUnquoted: "a", wantWasQuoted: true},
		{pattern: extglob.Patterns[1], wantRaw: "\\*", wantUnquoted: "*", wantWasQuoted: true},
		{pattern: extglob.Patterns[2], wantRaw: "b*", wantUnquoted: "b*", wantWasQuoted: false},
	}

	for i, tc := range tests {
		if got := tc.pattern.RawText(); got != tc.wantRaw {
			t.Fatalf("extglob.Patterns[%d].RawText() = %q, want %q", i, got, tc.wantRaw)
		}
		if got := tc.pattern.UnquotedText(); got != tc.wantUnquoted {
			t.Fatalf("extglob.Patterns[%d].UnquotedText() = %q, want %q", i, got, tc.wantUnquoted)
		}
		if got := tc.pattern.WasQuoted(); got != tc.wantWasQuoted {
			t.Fatalf("extglob.Patterns[%d].WasQuoted() = %v, want %v", i, got, tc.wantWasQuoted)
		}
	}
}

func TestPatternQuoteFidelityFromPatternGroupRawParse(t *testing.T) {
	t.Parallel()

	src := "[[ $x == (\"a\"|\\*|b*) ]]\n"
	file := parseTestFileVariant(t, LangZsh, src)
	testClause, ok := file.Stmts[0].Cmd.(*TestClause)
	if !ok {
		t.Fatalf("Cmd = %T, want *TestClause", file.Stmts[0].Cmd)
	}
	binary, ok := testClause.X.(*CondBinary)
	if !ok {
		t.Fatalf("cond = %T, want *CondBinary", testClause.X)
	}
	root := binary.Y.(*CondPattern).Pattern
	if got := root.RawText(); got != "(\"a\"|\\*|b*)" {
		t.Fatalf("root.RawText() = %q, want %q", got, "(\"a\"|\\*|b*)")
	}

	group, ok := root.Parts[0].(*PatternGroup)
	if !ok {
		t.Fatalf("root.Parts[0] = %T, want *PatternGroup", root.Parts[0])
	}

	tests := []struct {
		pattern       *Pattern
		wantRaw       string
		wantUnquoted  string
		wantWasQuoted bool
	}{
		{pattern: group.Patterns[0], wantRaw: "\"a\"", wantUnquoted: "a", wantWasQuoted: true},
		{pattern: group.Patterns[1], wantRaw: "\\*", wantUnquoted: "*", wantWasQuoted: true},
		{pattern: group.Patterns[2], wantRaw: "b*", wantUnquoted: "b*", wantWasQuoted: false},
	}

	for i, tc := range tests {
		if got := tc.pattern.RawText(); got != tc.wantRaw {
			t.Fatalf("group.Patterns[%d].RawText() = %q, want %q", i, got, tc.wantRaw)
		}
		if got := tc.pattern.UnquotedText(); got != tc.wantUnquoted {
			t.Fatalf("group.Patterns[%d].UnquotedText() = %q, want %q", i, got, tc.wantUnquoted)
		}
		if got := tc.pattern.WasQuoted(); got != tc.wantWasQuoted {
			t.Fatalf("group.Patterns[%d].WasQuoted() = %v, want %v", i, got, tc.wantWasQuoted)
		}
	}
}

func TestWordQuoteFidelityFromRegexRawParse(t *testing.T) {
	t.Parallel()

	src := "[[ $x =~ \"a b\" ]]\n"
	file := parseTestFile(t, src)
	testClause, ok := file.Stmts[0].Cmd.(*TestClause)
	if !ok {
		t.Fatalf("Cmd = %T, want *TestClause", file.Stmts[0].Cmd)
	}
	binary, ok := testClause.X.(*CondBinary)
	if !ok {
		t.Fatalf("cond = %T, want *CondBinary", testClause.X)
	}
	word := binary.Y.(*CondRegex).Word

	if got := word.RawText(); got != "\"a b\"" {
		t.Fatalf("regex word RawText() = %q, want %q", got, "\"a b\"")
	}
	if got := word.UnquotedText(); got != "a b" {
		t.Fatalf("regex word UnquotedText() = %q, want %q", got, "a b")
	}
	if !word.WasQuoted() {
		t.Fatalf("regex word WasQuoted() = false, want true")
	}
}
