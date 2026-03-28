package syntax

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestVarRefSubscriptKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		kind SubscriptKind
		mode SubscriptMode
	}{
		{src: "a[1]", kind: SubscriptExpr, mode: SubscriptAuto},
		{src: "a[@]", kind: SubscriptAt, mode: SubscriptAuto},
		{src: "a[*]", kind: SubscriptStar, mode: SubscriptAuto},
	}

	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			ref, err := NewParser().VarRef(strings.NewReader(tc.src))
			if err != nil {
				t.Fatalf("VarRef(%q) error = %v", tc.src, err)
			}
			if ref.Index == nil {
				t.Fatalf("VarRef(%q) index = nil", tc.src)
			}
			if ref.Index.Kind != tc.kind {
				t.Fatalf("VarRef(%q) kind = %v, want %v", tc.src, ref.Index.Kind, tc.kind)
			}
			if ref.Index.Mode != tc.mode {
				t.Fatalf("VarRef(%q) mode = %v, want %v", tc.src, ref.Index.Mode, tc.mode)
			}
		})
	}
}

func TestParseSubscriptKinds(t *testing.T) {
	t.Parallel()

	file, err := NewParser().Parse(strings.NewReader("echo ${foo[@]} ${foo[*]} ${foo[1]}\ndeclare foo[*]\n"), "")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}

	call := file.Stmts[0].Cmd.(*CallExpr)
	indexes := []SubscriptKind{
		call.Args[1].Parts[0].(*ParamExp).Index.Kind,
		call.Args[2].Parts[0].(*ParamExp).Index.Kind,
		call.Args[3].Parts[0].(*ParamExp).Index.Kind,
	}
	want := []SubscriptKind{SubscriptAt, SubscriptStar, SubscriptExpr}
	for i, got := range indexes {
		if got != want[i] {
			t.Fatalf("call arg %d kind = %v, want %v", i+1, got, want[i])
		}
	}

	decl := file.Stmts[1].Cmd.(*DeclClause)
	name, ok := decl.Operands[0].(*DeclName)
	if !ok {
		t.Fatalf("declare operand = %T, want *DeclName", decl.Operands[0])
	}
	if got := name.Ref.Index.Kind; got != SubscriptStar {
		t.Fatalf("declare subscript kind = %v, want %v", got, SubscriptStar)
	}
}

func TestParseSubscriptModesAndContexts(t *testing.T) {
	t.Parallel()

	src := strings.Join([]string{
		"declare -A foo=([a]=b)",
		"declare -A foo[a]=",
		"declare -a bar[1]=",
		"[[ -v assoc[$key] ]]",
		"[[ -R ref ]]",
	}, "\n")
	file, err := NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}

	declAssoc := file.Stmts[0].Cmd.(*DeclClause)
	as0 := declAssoc.Operands[1].(*DeclAssign).Assign
	if got := as0.Array.Elems[0].Index.Mode; got != SubscriptAssociative {
		t.Fatalf("declare -A array elem mode = %v, want %v", got, SubscriptAssociative)
	}

	declAssocRef := file.Stmts[1].Cmd.(*DeclClause)
	as1 := declAssocRef.Operands[1].(*DeclAssign).Assign
	if got := as1.Ref.Index.Mode; got != SubscriptAssociative {
		t.Fatalf("declare -A ref mode = %v, want %v", got, SubscriptAssociative)
	}

	declIndexedRef := file.Stmts[2].Cmd.(*DeclClause)
	as2 := declIndexedRef.Operands[1].(*DeclAssign).Assign
	if got := as2.Ref.Index.Mode; got != SubscriptIndexed {
		t.Fatalf("declare -a ref mode = %v, want %v", got, SubscriptIndexed)
	}

	testVarSet := file.Stmts[3].Cmd.(*TestClause)
	ref := testVarSet.X.(*CondUnary).X.(*CondVarRef).Ref
	if got := ref.Context; got != VarRefVarSet {
		t.Fatalf("[[ -v ]] ref context = %v, want %v", got, VarRefVarSet)
	}

	testRefVar := file.Stmts[4].Cmd.(*TestClause)
	ref = testRefVar.X.(*CondUnary).X.(*CondVarRef).Ref
	if got := ref.Context; got != VarRefDefault {
		t.Fatalf("[[ -R ]] ref context = %v, want %v", got, VarRefDefault)
	}
}

func TestParseArrayLikeFirstWordPreservesSpacing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want []string
	}{
		{src: "a[5 + 3]", want: []string{"a[5 + 3]"}},
		{src: "a[5 + 3]\n", want: []string{"a[5 + 3]"}},
		{src: "a[5 + 3]+", want: []string{"a[5 + 3]+"}},
		{src: "a[5 + 3]+\n", want: []string{"a[5 + 3]+"}},
		{src: "argv.sh a[3 + 4]=\n", want: []string{"argv.sh", "a[3", "+", "4]="}},
	}

	for _, tc := range tests {
		t.Run(strings.TrimSpace(tc.src), func(t *testing.T) {
			file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(tc.src), "")
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tc.src, err)
			}
			call := file.Stmts[0].Cmd.(*CallExpr)
			got := make([]string, len(call.Args))
			for i, arg := range call.Args {
				got[i] = arg.Lit()
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) args = %#v, want %#v", tc.src, got, tc.want)
			}
		})
	}
}

func TestParseCommandPrefixArrayAssignments(t *testing.T) {
	t.Parallel()

	file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader("A=a B=(b b) C=([k]=v) envprobe\n"), "")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	call := file.Stmts[0].Cmd.(*CallExpr)
	if got := len(call.Assigns); got != 3 {
		t.Fatalf("len(Assigns) = %d, want 3", got)
	}
	if call.Assigns[0].Value == nil || call.Assigns[0].Array != nil {
		t.Fatalf("assign 0 = %#v, want scalar assignment", call.Assigns[0])
	}
	if call.Assigns[1].Array == nil {
		t.Fatalf("assign 1 = %#v, want indexed array assignment", call.Assigns[1])
	}
	if call.Assigns[2].Array == nil {
		t.Fatalf("assign 2 = %#v, want associative array assignment", call.Assigns[2])
	}
	if got := call.Args[0].Lit(); got != "envprobe" {
		t.Fatalf("command = %q, want envprobe", got)
	}
}

func TestParseArrayMemberCompoundAssignment(t *testing.T) {
	t.Parallel()

	file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader("a[0]=(3 4)\n"), "")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	call := file.Stmts[0].Cmd.(*CallExpr)
	if got := len(call.Assigns); got != 1 {
		t.Fatalf("len(Assigns) = %d, want 1", got)
	}
	as := call.Assigns[0]
	if as.Ref == nil || as.Ref.Index == nil {
		t.Fatalf("assign ref = %#v, want indexed ref", as.Ref)
	}
	if as.Array == nil {
		t.Fatalf("assign = %#v, want compound array value", as)
	}
}

func TestTryAssignCandidateRecordsPendingArrayWordAtEOF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want string
	}{
		{src: "a[5 + 3]", want: "a[5 + 3]"},
		{src: "a[5 + 3]+", want: "a[5 + 3]+"},
	}

	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			p := NewParser(Variant(LangBash))
			p.reset()
			p.f = &File{}
			p.src = strings.NewReader(tc.src)
			p.rune()
			p.next()
			if !p.hasValidIdent() {
				t.Fatalf("hasValidIdent() = false for %q", tc.src)
			}
			if as, ok := p.tryAssignCandidate(false); ok || as != nil {
				t.Fatalf("tryAssignCandidate(%q) = (%v, %v), want no assignment", tc.src, as, ok)
			}
			if got := p.pendingArrayWord; got != tc.want {
				t.Fatalf("pendingArrayWord = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseArrayLikeAssignmentRawText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src        string
		wantRefRaw string
		wantSubRaw string
	}{
		{src: "a[5 + 3]=\n", wantRefRaw: "a[5 + 3]", wantSubRaw: "5 + 3"},
		{src: "a[5 # 1]=\n", wantRefRaw: "a[5 # 1]", wantSubRaw: "5 # 1"},
		{src: "a[0+]=\n", wantRefRaw: "a[0+]", wantSubRaw: "0+"},
	}

	for _, tc := range tests {
		t.Run(strings.TrimSpace(tc.src), func(t *testing.T) {
			file, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(tc.src), "")
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tc.src, err)
			}
			call := file.Stmts[0].Cmd.(*CallExpr)
			if len(call.Assigns) != 1 {
				t.Fatalf("Parse(%q) assigns = %d, want 1", tc.src, len(call.Assigns))
			}
			ref := call.Assigns[0].Ref
			if got := ref.RawText(); got != tc.wantRefRaw {
				t.Fatalf("Parse(%q) ref raw = %q, want %q", tc.src, got, tc.wantRefRaw)
			}
			if got := ref.Index.RawText(); got != tc.wantSubRaw {
				t.Fatalf("Parse(%q) subscript raw = %q, want %q", tc.src, got, tc.wantSubRaw)
			}
		})
	}
}

func TestParseArrayLikeEOFBashErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want string
	}{
		{
			src:  "a[",
			want: "line 1: unexpected EOF while looking for matching `]'\nline 1: syntax error: unexpected end of file",
		},
		{
			src:  "a[5",
			want: "line 1: unexpected EOF while looking for matching `]'\nline 1: syntax error: unexpected end of file",
		},
		{
			src:  "a[5 +",
			want: "line 1: unexpected EOF while looking for matching `]'\nline 1: syntax error: unexpected end of file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			_, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(tc.src), "")
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want parse error", tc.src)
			}
			var parseErr ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("Parse(%q) error = %T, want ParseError", tc.src, err)
			}
			want := tc.want
			if runtime.GOOS != "darwin" {
				want = strings.Split(tc.want, "\n")[0]
			}
			if got := parseErr.BashError(); got != want {
				t.Fatalf("Parse(%q) BashError() = %q, want %q", tc.src, got, want)
			}
		})
	}
}
