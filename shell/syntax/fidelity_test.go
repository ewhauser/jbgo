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

func TestWordTestLikeSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		src                string
		wantLeftRaw        string
		wantLeftUnquoted   string
		wantLeftQuoted     bool
		wantOperator       string
		wantOperatorCol    uint
		wantOperatorEndCol uint
		wantRightRaw       string
		wantRightUnquoted  string
		wantRightQuoted    bool
		wantNil            bool
	}{
		{
			name:               "double-bracket equals",
			src:                "[[ foo=bar ]]\n",
			wantLeftRaw:        "foo",
			wantLeftUnquoted:   "foo",
			wantLeftQuoted:     false,
			wantOperator:       "=",
			wantOperatorCol:    7,
			wantOperatorEndCol: 8,
			wantRightRaw:       "bar",
			wantRightUnquoted:  "bar",
			wantRightQuoted:    false,
		},
		{
			name:               "double-bracket double equals",
			src:                "[[ foo==bar ]]\n",
			wantLeftRaw:        "foo",
			wantLeftUnquoted:   "foo",
			wantLeftQuoted:     false,
			wantOperator:       "==",
			wantOperatorCol:    7,
			wantOperatorEndCol: 9,
			wantRightRaw:       "bar",
			wantRightUnquoted:  "bar",
			wantRightQuoted:    false,
		},
		{
			name:               "double-bracket not equals",
			src:                "[[ foo!=bar ]]\n",
			wantLeftRaw:        "foo",
			wantLeftUnquoted:   "foo",
			wantLeftQuoted:     false,
			wantOperator:       "!=",
			wantOperatorCol:    7,
			wantOperatorEndCol: 9,
			wantRightRaw:       "bar",
			wantRightUnquoted:  "bar",
			wantRightQuoted:    false,
		},
		{
			name:               "double-bracket regex",
			src:                "[[ foo=~bar ]]\n",
			wantLeftRaw:        "foo",
			wantLeftUnquoted:   "foo",
			wantLeftQuoted:     false,
			wantOperator:       "=~",
			wantOperatorCol:    7,
			wantOperatorEndCol: 9,
			wantRightRaw:       "bar",
			wantRightUnquoted:  "bar",
			wantRightQuoted:    false,
		},
		{
			name:               "assignment-like with quoted rhs",
			src:                "[[ file=\"$file\\n\" ]]\n",
			wantLeftRaw:        "file",
			wantLeftUnquoted:   "file",
			wantLeftQuoted:     false,
			wantOperator:       "=",
			wantOperatorCol:    8,
			wantOperatorEndCol: 9,
			wantRightRaw:       "\"$file\\n\"",
			wantRightUnquoted:  "$file\\n",
			wantRightQuoted:    true,
		},
		{
			name:               "bracket-test quoted literal plus expansion",
			src:                "[ \"QT6=${QT6:-no}\" = \"yes\" ]\n",
			wantLeftRaw:        "\"QT6",
			wantLeftUnquoted:   "QT6",
			wantLeftQuoted:     true,
			wantOperator:       "=",
			wantOperatorCol:    7,
			wantOperatorEndCol: 8,
			wantRightRaw:       "${QT6:-no}\"",
			wantRightUnquoted:  "${QT6:-no}",
			wantRightQuoted:    true,
		},
		{
			name:    "plain word",
			src:     "[[ foo ]]\n",
			wantNil: true,
		},
		{
			name:    "expansion on left",
			src:     "[[ ${x}=y ]]\n",
			wantNil: true,
		},
		{
			name:    "operator only inside nested expansion",
			src:     "[[ \"${x:=y}\" ]]\n",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			word := parseFirstRecoveredTestWord(t, tc.src)
			split := word.TestLikeSplit()
			if tc.wantNil {
				if split != nil {
					t.Fatalf("TestLikeSplit() = %#v, want nil", split)
				}
				return
			}
			if split == nil {
				t.Fatal("TestLikeSplit() = nil, want split")
			}
			if got := split.Operator; got != tc.wantOperator {
				t.Fatalf("Operator = %q, want %q", got, tc.wantOperator)
			}
			if got := split.OperatorPos.Col(); got != tc.wantOperatorCol {
				t.Fatalf("OperatorPos.Col() = %d, want %d", got, tc.wantOperatorCol)
			}
			if got := split.OperatorEnd.Col(); got != tc.wantOperatorEndCol {
				t.Fatalf("OperatorEnd.Col() = %d, want %d", got, tc.wantOperatorEndCol)
			}
			if got := split.Left.RawText(); got != tc.wantLeftRaw {
				t.Fatalf("Left.RawText() = %q, want %q", got, tc.wantLeftRaw)
			}
			if got := split.Left.UnquotedText(); got != tc.wantLeftUnquoted {
				t.Fatalf("Left.UnquotedText() = %q, want %q", got, tc.wantLeftUnquoted)
			}
			if got := split.Left.WasQuoted(); got != tc.wantLeftQuoted {
				t.Fatalf("Left.WasQuoted() = %v, want %v", got, tc.wantLeftQuoted)
			}
			if got := split.Right.RawText(); got != tc.wantRightRaw {
				t.Fatalf("Right.RawText() = %q, want %q", got, tc.wantRightRaw)
			}
			if got := split.Right.UnquotedText(); got != tc.wantRightUnquoted {
				t.Fatalf("Right.UnquotedText() = %q, want %q", got, tc.wantRightUnquoted)
			}
			if got := split.Right.WasQuoted(); got != tc.wantRightQuoted {
				t.Fatalf("Right.WasQuoted() = %v, want %v", got, tc.wantRightQuoted)
			}
		})
	}
}

func TestGluedComparisonWordsKeepParseShape(t *testing.T) {
	t.Parallel()

	t.Run("foo=bar stays condword", func(t *testing.T) {
		t.Parallel()

		file := parseTestFileVariant(t, LangBash, "[[ foo=bar ]]\n")
		testClause := file.Stmts[0].Cmd.(*TestClause)
		if _, ok := testClause.X.(*CondWord); !ok {
			t.Fatalf("testClause.X = %T, want *CondWord", testClause.X)
		}
	})

	t.Run("foo==bar stays condword", func(t *testing.T) {
		t.Parallel()

		file := parseTestFileVariant(t, LangBash, "[[ foo==bar ]]\n")
		testClause := file.Stmts[0].Cmd.(*TestClause)
		if _, ok := testClause.X.(*CondWord); !ok {
			t.Fatalf("testClause.X = %T, want *CondWord", testClause.X)
		}
	})

	t.Run("string operators stay binary operators", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			src    string
			wantOp BinTestOperator
		}{
			{src: "[[ b>a ]]\n", wantOp: TsAfter},
			{src: "[[ b<a ]]\n", wantOp: TsBefore},
		} {
			file := parseTestFileVariant(t, LangBash, tc.src)
			testClause := file.Stmts[0].Cmd.(*TestClause)
			bin, ok := testClause.X.(*CondBinary)
			if !ok {
				t.Fatalf("%q: testClause.X = %T, want *CondBinary", tc.src, testClause.X)
			}
			if got := bin.Op; got != tc.wantOp {
				t.Fatalf("%q: Op = %v, want %v", tc.src, got, tc.wantOp)
			}
		}
	})
}

func TestWordTestLikeSplitPreservesRawOperatorOffsets(t *testing.T) {
	t.Parallel()

	word := parseFirstRecoveredTestWord(t, "[[ foo\\\n=bar ]]\n")
	split := word.TestLikeSplit()
	if split == nil {
		t.Fatal("TestLikeSplit() = nil, want split")
	}
	if got, want := split.Left.RawText(), "foo\\\n"; got != want {
		t.Fatalf("Left.RawText() = %q, want %q", got, want)
	}
	if got, want := split.Left.UnquotedText(), "foo"; got != want {
		t.Fatalf("Left.UnquotedText() = %q, want %q", got, want)
	}
	if got, want := split.OperatorPos.Line(), uint(2); got != want {
		t.Fatalf("OperatorPos.Line() = %d, want %d", got, want)
	}
	if got, want := split.OperatorPos.Col(), uint(1); got != want {
		t.Fatalf("OperatorPos.Col() = %d, want %d", got, want)
	}
	if got, want := split.OperatorEnd.Line(), uint(2); got != want {
		t.Fatalf("OperatorEnd.Line() = %d, want %d", got, want)
	}
	if got, want := split.OperatorEnd.Col(), uint(2); got != want {
		t.Fatalf("OperatorEnd.Col() = %d, want %d", got, want)
	}
	if got, want := split.Right.RawText(), "bar"; got != want {
		t.Fatalf("Right.RawText() = %q, want %q", got, want)
	}
}

func TestWordTestLikeSplitPreservesDollarSglQuotedOperatorOffsets(t *testing.T) {
	t.Parallel()

	word := parseFirstRecoveredTestWord(t, "[[ $'foo\n=bar' ]]\n")
	split := word.TestLikeSplit()
	if split == nil {
		t.Fatal("TestLikeSplit() = nil, want split")
	}
	if got, want := split.Left.RawText(), "$'foo\n"; got != want {
		t.Fatalf("Left.RawText() = %q, want %q", got, want)
	}
	if got, want := split.Left.UnquotedText(), "foo\n"; got != want {
		t.Fatalf("Left.UnquotedText() = %q, want %q", got, want)
	}
	if got, want := split.OperatorPos.Line(), uint(2); got != want {
		t.Fatalf("OperatorPos.Line() = %d, want %d", got, want)
	}
	if got, want := split.OperatorPos.Col(), uint(1); got != want {
		t.Fatalf("OperatorPos.Col() = %d, want %d", got, want)
	}
	if got, want := split.OperatorEnd.Line(), uint(2); got != want {
		t.Fatalf("OperatorEnd.Line() = %d, want %d", got, want)
	}
	if got, want := split.OperatorEnd.Col(), uint(2); got != want {
		t.Fatalf("OperatorEnd.Col() = %d, want %d", got, want)
	}
	if got, want := split.Right.RawText(), "bar'"; got != want {
		t.Fatalf("Right.RawText() = %q, want %q", got, want)
	}
	if got, want := split.Right.UnquotedText(), "bar"; got != want {
		t.Fatalf("Right.UnquotedText() = %q, want %q", got, want)
	}
}

func TestWordTestLikeSplitAlignsRightRawFragment(t *testing.T) {
	t.Parallel()

	word := parseFirstRecoveredTestWord(t, "[[ \"foo=\"$(bar) ]]\n")
	split := word.TestLikeSplit()
	if split == nil {
		t.Fatal("TestLikeSplit() = nil, want split")
	}
	if got, want := split.Left.RawText(), "\"foo"; got != want {
		t.Fatalf("Left.RawText() = %q, want %q", got, want)
	}
	if got, want := split.Left.UnquotedText(), "foo"; got != want {
		t.Fatalf("Left.UnquotedText() = %q, want %q", got, want)
	}
	if got, want := split.Right.RawText(), "$(bar)"; got != want {
		t.Fatalf("Right.RawText() = %q, want %q", got, want)
	}
	if got, want := split.Right.UnquotedText(), "$(bar)"; got != want {
		t.Fatalf("Right.UnquotedText() = %q, want %q", got, want)
	}
	if got := split.Right.WasQuoted(); got {
		t.Fatalf("Right.WasQuoted() = %v, want false", got)
	}
}

func parseFirstRecoveredTestWord(t *testing.T, src string) *Word {
	t.Helper()

	file := parseTestFileVariant(t, LangBash, src)
	switch cmd := file.Stmts[0].Cmd.(type) {
	case *TestClause:
		word, ok := cmd.X.(*CondWord)
		if !ok {
			t.Fatalf("cmd.X = %T, want *CondWord", cmd.X)
		}
		return word.Word
	case *CallExpr:
		if len(cmd.Args) < 2 {
			t.Fatalf("len(cmd.Args) = %d, want at least 2", len(cmd.Args))
		}
		return cmd.Args[1]
	default:
		t.Fatalf("Cmd = %T, want *TestClause or *CallExpr", file.Stmts[0].Cmd)
		return nil
	}
}
