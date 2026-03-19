package syntax

import (
	"strings"
	"testing"
)

func TestVarRefSubscriptKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		kind SubscriptKind
	}{
		{src: "a[1]", kind: SubscriptExpr},
		{src: "a[@]", kind: SubscriptAt},
		{src: "a[*]", kind: SubscriptStar},
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
