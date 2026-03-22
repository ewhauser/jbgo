package interp

import (
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
)

type testEachEntry struct {
	name string
	vr   expand.Variable
}

type testEachEnviron []testEachEntry

func (e testEachEnviron) Get(name string) expand.Variable {
	for i := len(e) - 1; i >= 0; i-- {
		if e[i].name == name {
			return e[i].vr
		}
	}
	return expand.Variable{}
}

func (e testEachEnviron) Each() expand.VarSeq {
	return func(yield func(string, expand.Variable) bool) {
		for _, entry := range e {
			if !yield(entry.name, entry.vr) {
				return
			}
		}
	}
}

func (e testEachEnviron) Set(name string, vr expand.Variable) error {
	panic("unexpected Set on testEachEnviron")
}

func TestOverlayEnvironEachIsUniqueAcrossLayers(t *testing.T) {
	t.Parallel()

	base := expand.ListEnviron("KEEP=base", "SHADOW=parent")
	mid := &overlayEnviron{parent: base}
	mustSetTestVar(t, mid, "SHADOW", expand.Variable{
		Set:      true,
		Exported: true,
		Kind:     expand.String,
		Str:      "mid",
	})

	top := &overlayEnviron{parent: mid}
	mustSetTestVar(t, top, "SHADOW", expand.Variable{
		Set:      true,
		Exported: true,
		Kind:     expand.String,
		Str:      "top",
	})
	mustSetTestVar(t, top, "LOCAL", expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  "local",
	})

	total, entries := collectEachEntries(top)
	if total != 3 {
		t.Fatalf("Each() yielded %d entries, want 3", total)
	}
	if got := len(entries["SHADOW"]); got != 1 {
		t.Fatalf("SHADOW yielded %d entries, want 1", got)
	}
	if got := entries["SHADOW"][0].String(); got != "top" {
		t.Fatalf("SHADOW = %q, want %q", got, "top")
	}
	if got := entries["KEEP"][0].String(); got != "base" {
		t.Fatalf("KEEP = %q, want %q", got, "base")
	}
	if got := entries["LOCAL"][0].String(); got != "local" {
		t.Fatalf("LOCAL = %q, want %q", got, "local")
	}
}

func TestOverlayEnvironEachUnsetSuppressesParentBinding(t *testing.T) {
	t.Parallel()

	base := expand.ListEnviron("KEEP=base", "REMOVE=parent")
	overlay := &overlayEnviron{parent: base}
	mustSetTestVar(t, overlay, "REMOVE", expand.Variable{})

	total, entries := collectEachEntries(overlay)
	if total != 2 {
		t.Fatalf("Each() yielded %d entries, want 2", total)
	}
	if got := len(entries["REMOVE"]); got != 1 {
		t.Fatalf("REMOVE yielded %d entries, want 1", got)
	}
	if entries["REMOVE"][0].IsSet() {
		t.Fatalf("REMOVE is set, want unset shadow entry")
	}
	if got := entries["KEEP"][0].String(); got != "base" {
		t.Fatalf("KEEP = %q, want %q", got, "base")
	}
}

func TestShadowWriteEnvironEachYieldsShadowOnce(t *testing.T) {
	t.Parallel()

	parent := testEachEnviron{
		{name: "SHADOW", vr: expand.Variable{Set: true, Kind: expand.String, Str: "first"}},
		{name: "KEEP", vr: expand.Variable{Set: true, Kind: expand.String, Str: "keep"}},
		{name: "SHADOW", vr: expand.Variable{Set: true, Kind: expand.String, Str: "second"}},
	}
	env := &shadowWriteEnviron{
		parent:     parent,
		shadowName: "SHADOW",
		shadowSet:  true,
		shadow: expand.Variable{
			Set:  true,
			Kind: expand.String,
			Str:  "replacement",
		},
	}

	total, entries := collectEachEntries(env)
	if total != 2 {
		t.Fatalf("Each() yielded %d entries, want 2", total)
	}
	if got := len(entries["SHADOW"]); got != 1 {
		t.Fatalf("SHADOW yielded %d entries, want 1", got)
	}
	if got := entries["SHADOW"][0].String(); got != "replacement" {
		t.Fatalf("SHADOW = %q, want %q", got, "replacement")
	}
	if got := entries["KEEP"][0].String(); got != "keep" {
		t.Fatalf("KEEP = %q, want %q", got, "keep")
	}
}

func mustSetTestVar(tb testing.TB, env expand.WriteEnviron, name string, vr expand.Variable) {
	tb.Helper()
	if err := env.Set(name, vr); err != nil {
		tb.Fatalf("Set(%q): %v", name, err)
	}
}

func collectEachEntries(env expand.Environ) (int, map[string][]expand.Variable) {
	entries := make(map[string][]expand.Variable)
	total := 0
	for name, vr := range env.Each() {
		total++
		entries[name] = append(entries[name], vr)
	}
	return total, entries
}
