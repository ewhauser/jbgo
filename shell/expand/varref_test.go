package expand

import (
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

func parseVarRefForTest(t *testing.T, src string) *syntax.VarRef {
	t.Helper()
	ref, err := syntax.NewParser().VarRef(strings.NewReader(src))
	if err != nil {
		t.Fatalf("VarRef(%q) error = %v", src, err)
	}
	return ref
}

func TestResolveRef(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"arr": {
			Set:  true,
			Kind: Indexed,
			List: []string{"x", "y"},
		},
		"whole": {
			Set:  true,
			Kind: NameRef,
			Str:  "arr",
		},
		"elem": {
			Set:  true,
			Kind: NameRef,
			Str:  "arr[0]",
		},
	}

	ref, vr, err := env.Get("whole").ResolveRef(env, parseVarRefForTest(t, "whole[1]"))
	if err != nil {
		t.Fatalf("ResolveRef whole error = %v", err)
	}
	if got := ref.Name.Value; got != "arr" {
		t.Fatalf("resolved name = %q, want arr", got)
	}
	if got := subscriptLit(ref.Index); got != "1" {
		t.Fatalf("resolved index = %q, want 1", got)
	}
	if got := ref.Index.Mode; got != syntax.SubscriptIndexed {
		t.Fatalf("resolved mode = %v, want %v", got, syntax.SubscriptIndexed)
	}
	if vr.Kind != Indexed {
		t.Fatalf("resolved kind = %v, want Indexed", vr.Kind)
	}

	_, _, err = env.Get("elem").ResolveRef(env, parseVarRefForTest(t, "elem[1]"))
	if err == nil {
		t.Fatal("ResolveRef elem[1] succeeded, want error")
	}
	if _, ok := err.(InvalidIdentifierError); !ok {
		t.Fatalf("ResolveRef elem[1] error = %T, want InvalidIdentifierError", err)
	}
}

func TestResolveRefPreservesContextAndAssociativeMode(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"assoc": {
			Set:  true,
			Kind: Associative,
			Map:  map[string]string{"k": "v"},
		},
		"ref": {
			Set:  true,
			Kind: NameRef,
			Str:  "assoc",
		},
	}

	ref, vr, err := env.Get("ref").ResolveRef(env, &syntax.VarRef{
		Name:    &syntax.Lit{Value: "ref"},
		Index:   &syntax.Subscript{Kind: syntax.SubscriptExpr, Mode: syntax.SubscriptAuto, Expr: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "k"}}}},
		Context: syntax.VarRefVarSet,
	})
	if err != nil {
		t.Fatalf("ResolveRef assoc error = %v", err)
	}
	if vr.Kind != Associative {
		t.Fatalf("resolved kind = %v, want Associative", vr.Kind)
	}
	if got := ref.Context; got != syntax.VarRefVarSet {
		t.Fatalf("resolved context = %v, want %v", got, syntax.VarRefVarSet)
	}
	if got := ref.Index.Mode; got != syntax.SubscriptAssociative {
		t.Fatalf("resolved mode = %v, want %v", got, syntax.SubscriptAssociative)
	}
}
