package syntax

import (
	"strings"
	"testing"
)

func TestParseBackquoteInDoubleQuotesPreservesDBracketCloseToken(t *testing.T) {
	t.Parallel()

	src := "echo \"123 `[[ $(echo \\\\\" > \\\"$file\\\") ]]` 456\"\n"
	file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	call := file.Stmts[0].Cmd.(*CallExpr)
	if got := len(call.Args); got != 2 {
		t.Fatalf("len(Args) = %d, want 2", got)
	}
	dq, ok := call.Args[1].Parts[0].(*DblQuoted)
	if !ok {
		t.Fatalf("arg[1] part = %T, want *DblQuoted", call.Args[1].Parts[0])
	}
	foundCmdSubst := false
	for _, part := range dq.Parts {
		if _, ok := part.(*CmdSubst); ok {
			foundCmdSubst = true
			break
		}
	}
	if !foundCmdSubst {
		t.Fatalf("double-quoted parts = %#v, want backquote command substitution", dq.Parts)
	}
}

func TestParseConditionalWordStartingWithCloseTokenPrefix(t *testing.T) {
	t.Parallel()

	src := "x=sfx\n[[ ]]$x == ']]sfx' ]]\n"
	file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	testClause, ok := file.Stmts[1].Cmd.(*TestClause)
	if !ok {
		t.Fatalf("stmt[1].Cmd = %T, want *TestClause", file.Stmts[1].Cmd)
	}
	bin, ok := testClause.X.(*CondBinary)
	if !ok {
		t.Fatalf("test clause expr = %T, want *CondBinary", testClause.X)
	}
	left, ok := bin.X.(*CondWord)
	if !ok {
		t.Fatalf("left expr = %T, want *CondWord", bin.X)
	}
	if got := len(left.Word.Parts); got != 2 {
		t.Fatalf("len(left word parts) = %d, want 2", got)
	}
	lit, ok := left.Word.Parts[0].(*Lit)
	if !ok || lit.Value != "]]" {
		t.Fatalf("left word part[0] = %#v, want literal %q", left.Word.Parts[0], "]]")
	}
	if _, ok := left.Word.Parts[1].(*ParamExp); !ok {
		t.Fatalf("left word part[1] = %T, want *ParamExp", left.Word.Parts[1])
	}
}

func TestParseMalformedBackquoteRecoversOuterScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
	}{
		{
			name: "unclosed quote",
			src:  "echo `echo \"`\necho after\n",
		},
		{
			name: "unclosed quote at eof",
			src:  "echo `echo \"`",
		},
		{
			name: "escaped quote before closing backquote",
			src:  "echo `echo \\\\\"`\necho after\n",
		},
		{
			name: "escaped quote before closing backquote at eof",
			src:  "echo `echo \\\\\"`",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(tc.src), "")
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tc.src, err)
			}
			wantStmts := 2
			if !strings.HasSuffix(tc.src, "\n") {
				wantStmts = 1
			}
			if got := len(file.Stmts); got != wantStmts {
				t.Fatalf("len(Stmts) = %d, want %d", got, wantStmts)
			}
			call := file.Stmts[0].Cmd.(*CallExpr)
			if got := len(call.Args); got != 2 {
				t.Fatalf("len(Args) = %d, want 2", got)
			}
			cs, ok := call.Args[1].Parts[0].(*CmdSubst)
			if !ok {
				t.Fatalf("arg[1] part = %T, want *CmdSubst", call.Args[1].Parts[0])
			}
			if !cs.Backquotes {
				t.Fatalf("CmdSubst.Backquotes = false, want true")
			}
			if tc.name == "unclosed quote" && len(cs.Stmts) != 0 {
				t.Fatalf("len(CmdSubst.Stmts) = %d, want 0 deferred parse", len(cs.Stmts))
			}
			if wantStmts == 2 {
				next := file.Stmts[1].Cmd.(*CallExpr)
				if got := next.Args[1].Lit(); got != "after" {
					t.Fatalf("second stmt arg = %q, want %q", got, "after")
				}
			}
		})
	}
}

func TestParseMalformedBackquoteRecoversAtScriptStartWhenScriptContinues(t *testing.T) {
	t.Parallel()

	src := "`echo \"`\necho after\n"
	file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", src, err)
	}
	if got := len(file.Stmts); got != 2 {
		t.Fatalf("len(Stmts) = %d, want 2", got)
	}
	first := file.Stmts[0].Cmd.(*CallExpr)
	if got := len(first.Args); got != 1 {
		t.Fatalf("len(first.Args) = %d, want 1", got)
	}
	cs, ok := first.Args[0].Parts[0].(*CmdSubst)
	if !ok {
		t.Fatalf("first arg part = %T, want *CmdSubst", first.Args[0].Parts[0])
	}
	if !cs.Backquotes {
		t.Fatalf("CmdSubst.Backquotes = false, want true")
	}
	next := file.Stmts[1].Cmd.(*CallExpr)
	if got := next.Args[1].Lit(); got != "after" {
		t.Fatalf("second stmt arg = %q, want %q", got, "after")
	}
}

func TestParseMalformedBackquoteRecoveryKeepsUnreadTail(t *testing.T) {
	t.Parallel()

	fillerLine := "# filler filler filler filler filler filler filler filler filler filler\n"
	filler := strings.Repeat(fillerLine, bufSize/len(fillerLine)+4)
	src := "echo `echo \"`\n" + filler + "echo after\n"

	file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", src, err)
	}
	if got := len(file.Stmts); got != 2 {
		t.Fatalf("len(Stmts) = %d, want 2", got)
	}
	next := file.Stmts[1].Cmd.(*CallExpr)
	if got := next.Args[1].Lit(); got != "after" {
		t.Fatalf("second stmt arg = %q, want %q", got, "after")
	}
}

func TestParseErrRecoverableInBackquotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  ParseError
		want bool
	}{
		{
			name: "unclosed single quote at eof",
			err: ParseError{
				Kind:       ParseErrorKindUnclosed,
				Unexpected: ParseErrorSymbolEOF,
				Expected:   []ParseErrorSymbol{ParseErrorSymbolSingleQuote},
			},
			want: true,
		},
		{
			name: "unclosed brace at eof",
			err: ParseError{
				Kind:       ParseErrorKindUnclosed,
				Construct:  ParseErrorSymbolLeftBrace,
				Unexpected: ParseErrorSymbolEOF,
				Expected:   []ParseErrorSymbol{ParseErrorSymbolRightBrace},
			},
			want: true,
		},
		{
			name: "unmatched brace at backquote",
			err: ParseError{
				Kind:       ParseErrorKindUnmatched,
				Construct:  ParseErrorSymbolLeftBrace,
				Unexpected: ParseErrorSymbolBackquote,
				Expected:   []ParseErrorSymbol{ParseErrorSymbolRightBrace},
			},
			want: true,
		},
		{
			name: "missing fi at backquote",
			err: ParseError{
				Kind:       ParseErrorKindMissing,
				Construct:  ParseErrorSymbol("if"),
				Unexpected: ParseErrorSymbolBackquote,
				Expected:   []ParseErrorSymbol{ParseErrorSymbolFi},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseErrRecoverableInBackquotes(tc.err); got != tc.want {
				t.Fatalf("parseErrRecoverableInBackquotes(%+v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
