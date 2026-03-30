package syntax_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shell/syntax/typedjson"
)

func parseFile(t *testing.T, src string) *syntax.File {
	t.Helper()

	file, err := syntax.NewParser().Parse(strings.NewReader(src), "public.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return file
}

func parseFileVariant(t *testing.T, variant syntax.LangVariant, src string) *syntax.File {
	t.Helper()

	file, err := syntax.NewParser(syntax.Variant(variant)).Parse(strings.NewReader(src), "public.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return file
}

func encodeDecodeFile(t *testing.T, file *syntax.File) *syntax.File {
	t.Helper()

	var encoded bytes.Buffer
	if err := typedjson.Encode(&encoded, file); err != nil {
		t.Fatalf("typedjson.Encode() error = %v", err)
	}
	node, err := typedjson.Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("typedjson.Decode() error = %v", err)
	}
	decoded, ok := node.(*syntax.File)
	if !ok {
		t.Fatalf("Decode() returned %T, want *syntax.File", node)
	}
	return decoded
}

func TestPublicSyntaxParseAndTypedJSONRoundTrip(t *testing.T) {
	t.Parallel()

	src := "echo hi\n"
	file := parseFile(t, src)
	if got, want := len(file.Stmts), 1; got != want {
		t.Fatalf("len(Stmts) = %d, want %d", got, want)
	}

	decoded := encodeDecodeFile(t, file)

	var printed bytes.Buffer
	if err := syntax.NewPrinter().Print(&printed, decoded); err != nil {
		t.Fatalf("Print() error = %v", err)
	}
	if got, want := printed.String(), src; got != want {
		t.Fatalf("printed source = %q, want %q", got, want)
	}
}

func TestPublicSyntaxParseErrorMetadata(t *testing.T) {
	t.Parallel()

	_, err := syntax.NewParser().Parse(strings.NewReader("if foo\n"), "public.sh")
	if err == nil {
		t.Fatal("Parse() error = nil, want parse error")
	}
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("Parse() error = %T, want syntax.ParseError", err)
	}
	if got, want := parseErr.Kind, syntax.ParseErrorKindMissing; got != want {
		t.Fatalf("Kind = %q, want %q", got, want)
	}
	if got, want := parseErr.Construct, syntax.ParseErrorSymbol("if <cond>"); got != want {
		t.Fatalf("Construct = %q, want %q", got, want)
	}
	if got, want := parseErr.Unexpected, syntax.ParseErrorSymbolEOF; got != want {
		t.Fatalf("Unexpected = %q, want %q", got, want)
	}
	if got, want := parseErr.Expected, []syntax.ParseErrorSymbol{syntax.ParseErrorSymbolThen}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Expected = %v, want %v", got, want)
	}
}

func TestPublicSyntaxStrayCloserParseErrorMetadata(t *testing.T) {
	t.Parallel()

	_, err := syntax.NewParser().Parse(strings.NewReader("fi\n"), "public.sh")
	if err == nil {
		t.Fatal("Parse() error = nil, want parse error")
	}
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("Parse() error = %T, want syntax.ParseError", err)
	}
	if got, want := parseErr.Kind, syntax.ParseErrorKindUnexpected; got != want {
		t.Fatalf("Kind = %q, want %q", got, want)
	}
	if got, want := parseErr.Construct, syntax.ParseErrorSymbol("if"); got != want {
		t.Fatalf("Construct = %q, want %q", got, want)
	}
	if got, want := parseErr.Unexpected, syntax.ParseErrorSymbolFi; got != want {
		t.Fatalf("Unexpected = %q, want %q", got, want)
	}
}

func TestPublicSyntaxPatternParseErrorMetadata(t *testing.T) {
	t.Parallel()

	_, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader("[[ x == (foo|bar)* ]]\n"), "public.sh")
	if err == nil {
		t.Fatal("Parse() error = nil, want parse error")
	}
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("Parse() error = %T, want syntax.ParseError", err)
	}
	if got, want := parseErr.Kind, syntax.ParseErrorKindUnexpected; got != want {
		t.Fatalf("Kind = %q, want %q", got, want)
	}
	if got, want := parseErr.Construct, syntax.ParseErrorSymbolPattern; got != want {
		t.Fatalf("Construct = %q, want %q", got, want)
	}
	if got, want := parseErr.Unexpected, syntax.ParseErrorSymbolLeftParen; got != want {
		t.Fatalf("Unexpected = %q, want %q", got, want)
	}
}

func TestPublicWordQuoteFidelity(t *testing.T) {
	t.Parallel()

	src := "echo plain 'single value' \"double $x\" \\* \"$x\"\n"
	file := parseFile(t, src)
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		t.Fatalf("Cmd = %T, want *syntax.CallExpr", file.Stmts[0].Cmd)
	}

	tests := []struct {
		word         *syntax.Word
		wantRaw      string
		wantUnquoted string
		wantQuoted   bool
	}{
		{word: call.Args[1], wantRaw: "plain", wantUnquoted: "plain", wantQuoted: false},
		{word: call.Args[2], wantRaw: "'single value'", wantUnquoted: "single value", wantQuoted: true},
		{word: call.Args[3], wantRaw: "\"double $x\"", wantUnquoted: "double $x", wantQuoted: true},
		{word: call.Args[4], wantRaw: "\\*", wantUnquoted: "*", wantQuoted: true},
		{word: call.Args[5], wantRaw: "\"$x\"", wantUnquoted: "$x", wantQuoted: true},
	}

	for i, tc := range tests {
		if got := tc.word.RawText(); got != tc.wantRaw {
			t.Fatalf("arg[%d].RawText() = %q, want %q", i+1, got, tc.wantRaw)
		}
		if got := tc.word.UnquotedText(); got != tc.wantUnquoted {
			t.Fatalf("arg[%d].UnquotedText() = %q, want %q", i+1, got, tc.wantUnquoted)
		}
		if got := tc.word.WasQuoted(); got != tc.wantQuoted {
			t.Fatalf("arg[%d].WasQuoted() = %v, want %v", i+1, got, tc.wantQuoted)
		}
	}
}

func TestPublicPatternQuoteFidelity(t *testing.T) {
	t.Parallel()

	src := "case $x in \\*|\"*\"|*) ;; esac\n"
	file := parseFile(t, src)
	cc, ok := file.Stmts[0].Cmd.(*syntax.CaseClause)
	if !ok {
		t.Fatalf("Cmd = %T, want *syntax.CaseClause", file.Stmts[0].Cmd)
	}
	patterns := cc.Items[0].Patterns

	tests := []struct {
		pattern       *syntax.Pattern
		wantRaw       string
		wantUnquoted  string
		wantWasQuoted bool
	}{
		{pattern: patterns[0], wantRaw: "\\*", wantUnquoted: "*", wantWasQuoted: true},
		{pattern: patterns[1], wantRaw: "\"*\"", wantUnquoted: "*", wantWasQuoted: true},
		{pattern: patterns[2], wantRaw: "*", wantUnquoted: "*", wantWasQuoted: false},
	}

	for i, tc := range tests {
		if got := tc.pattern.RawText(); got != tc.wantRaw {
			t.Fatalf("pattern[%d].RawText() = %q, want %q", i, got, tc.wantRaw)
		}
		if got := tc.pattern.UnquotedText(); got != tc.wantUnquoted {
			t.Fatalf("pattern[%d].UnquotedText() = %q, want %q", i, got, tc.wantUnquoted)
		}
		if got := tc.pattern.WasQuoted(); got != tc.wantWasQuoted {
			t.Fatalf("pattern[%d].WasQuoted() = %v, want %v", i, got, tc.wantWasQuoted)
		}
	}
}

func TestPublicTypedJSONDecodedQuoteFidelity(t *testing.T) {
	t.Parallel()

	src := "case $x in \"*\") echo 'quoted';; esac\n"
	decoded := encodeDecodeFile(t, parseFile(t, src))
	cc := decoded.Stmts[0].Cmd.(*syntax.CaseClause)
	pattern := cc.Items[0].Patterns[0]
	word := cc.Items[0].Stmts[0].Cmd.(*syntax.CallExpr).Args[1]

	if got := pattern.RawText(); got != "" {
		t.Fatalf("decoded pattern RawText() = %q, want empty", got)
	}
	if got := pattern.UnquotedText(); got != "*" {
		t.Fatalf("decoded pattern UnquotedText() = %q, want %q", got, "*")
	}
	if !pattern.WasQuoted() {
		t.Fatalf("decoded pattern WasQuoted() = false, want true")
	}

	if got := word.RawText(); got != "" {
		t.Fatalf("decoded word RawText() = %q, want empty", got)
	}
	if got := word.UnquotedText(); got != "quoted" {
		t.Fatalf("decoded word UnquotedText() = %q, want %q", got, "quoted")
	}
	if !word.WasQuoted() {
		t.Fatalf("decoded word WasQuoted() = false, want true")
	}
}

func TestPublicPatternGroupRoundTrip(t *testing.T) {
	t.Parallel()

	src := "[[ a == (b|c)* ]]\n"
	file := parseFileVariant(t, syntax.LangZsh, src)
	decoded := encodeDecodeFile(t, file)

	testClause := decoded.Stmts[0].Cmd.(*syntax.TestClause)
	cond := testClause.X.(*syntax.CondBinary)
	pat := cond.Y.(*syntax.CondPattern).Pattern
	group, ok := pat.Parts[0].(*syntax.PatternGroup)
	if !ok {
		t.Fatalf("pat.Parts[0] = %T, want *syntax.PatternGroup", pat.Parts[0])
	}
	if got, want := len(group.Patterns), 2; got != want {
		t.Fatalf("len(group.Patterns) = %d, want %d", got, want)
	}

	var printed bytes.Buffer
	if err := syntax.NewPrinter().Print(&printed, decoded); err != nil {
		t.Fatalf("Print() error = %v", err)
	}
	if got, want := printed.String(), src; got != want {
		t.Fatalf("printed source = %q, want %q", got, want)
	}
}

func TestPublicSyntheticQuoteFidelity(t *testing.T) {
	t.Parallel()

	word := &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.SglQuoted{Value: "quoted"},
		},
	}
	pattern := &syntax.Pattern{
		Parts: []syntax.PatternPart{
			&syntax.DblQuoted{
				Parts: []syntax.WordPart{
					&syntax.Lit{Value: "*"},
				},
			},
		},
	}

	if got := word.RawText(); got != "" {
		t.Fatalf("synthetic word RawText() = %q, want empty", got)
	}
	if got := word.UnquotedText(); got != "quoted" {
		t.Fatalf("synthetic word UnquotedText() = %q, want %q", got, "quoted")
	}
	if !word.WasQuoted() {
		t.Fatalf("synthetic word WasQuoted() = false, want true")
	}

	if got := pattern.RawText(); got != "" {
		t.Fatalf("synthetic pattern RawText() = %q, want empty", got)
	}
	if got := pattern.UnquotedText(); got != "*" {
		t.Fatalf("synthetic pattern UnquotedText() = %q, want %q", got, "*")
	}
	if !pattern.WasQuoted() {
		t.Fatalf("synthetic pattern WasQuoted() = false, want true")
	}
}
