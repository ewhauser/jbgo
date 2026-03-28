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
			name: "escaped quote before closing backquote",
			src:  "echo `echo \\\\\"`\necho after\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(tc.src), "")
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tc.src, err)
			}
			if got := len(file.Stmts); got != 2 {
				t.Fatalf("len(Stmts) = %d, want 2", got)
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
			next := file.Stmts[1].Cmd.(*CallExpr)
			if got := next.Args[1].Lit(); got != "after" {
				t.Fatalf("second stmt arg = %q, want %q", got, "after")
			}
		})
	}
}
