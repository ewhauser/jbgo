package interp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/expand"
)

type testEachEntry struct {
	name string
	vr   expand.Variable
}

type testEachEnviron []testEachEntry

type mutableTestEnviron struct {
	values map[string]expand.Variable
	order  []string
}

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

func newMutableTestEnviron(pairs ...string) *mutableTestEnviron {
	env := &mutableTestEnviron{values: make(map[string]expand.Variable)}
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		env.Set(name, expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: value})
	}
	return env
}

func (e *mutableTestEnviron) Get(name string) expand.Variable {
	if vr, ok := e.values[name]; ok {
		return vr
	}
	return expand.Variable{}
}

func (e *mutableTestEnviron) Each() expand.VarSeq {
	return func(yield func(string, expand.Variable) bool) {
		for _, name := range e.order {
			if !yield(name, e.values[name]) {
				return
			}
		}
	}
}

func (e *mutableTestEnviron) Set(name string, vr expand.Variable) {
	if _, ok := e.values[name]; !ok {
		e.order = append(e.order, name)
	}
	e.values[name] = vr
}

func TestOverlayEnvironEachIsUniqueAcrossLayers(t *testing.T) {
	t.Parallel()

	base := expand.ListEnviron("KEEP=base", "SHADOW=parent")
	mid := newScopedOverlayEnviron(base, false)
	mustSetTestVar(t, mid, "SHADOW", expand.Variable{
		Set:      true,
		Exported: true,
		Kind:     expand.String,
		Str:      "mid",
	})

	top := newScopedOverlayEnviron(mid, false)
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
	overlay := newScopedOverlayEnviron(base, false)
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

func TestOverlayEnvironEachInvalidatesChildCacheAfterParentMutation(t *testing.T) {
	t.Parallel()

	parent := newScopedOverlayEnviron(expand.ListEnviron("FOO=parent"), false)
	child := newScopedOverlayEnviron(parent, false)

	if _, entries := collectEachEntries(child); entries["FOO"][0].String() != "parent" {
		t.Fatalf("initial child FOO = %q, want parent", entries["FOO"][0].String())
	}

	mustSetTestVar(t, parent, "FOO", expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  "updated",
	})

	_, entries := collectEachEntries(child)
	if got := entries["FOO"][0].String(); got != "updated" {
		t.Fatalf("child FOO after parent mutation = %q, want updated", got)
	}
}

func TestOverlayEnvironSnapshotKeepsIndependentVisibleCache(t *testing.T) {
	t.Parallel()

	parent := newScopedOverlayEnviron(expand.ListEnviron("FOO=parent"), false)
	snapshot := newOverlayEnviron(parent, true, false)

	if _, entries := collectEachEntries(snapshot); entries["FOO"][0].String() != "parent" {
		t.Fatalf("initial snapshot FOO = %q, want parent", entries["FOO"][0].String())
	}

	mustSetTestVar(t, parent, "FOO", expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  "updated",
	})

	_, snapshotEntries := collectEachEntries(snapshot)
	if got := snapshotEntries["FOO"][0].String(); got != "parent" {
		t.Fatalf("snapshot FOO after parent mutation = %q, want parent", got)
	}
	_, parentEntries := collectEachEntries(parent)
	if got := parentEntries["FOO"][0].String(); got != "updated" {
		t.Fatalf("parent FOO after mutation = %q, want updated", got)
	}
}

func TestPrintSetVarsInvalidatesCacheAndStaysSorted(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{
		Env: expand.ListEnviron("HOME=/sandbox"),
		Dir: "/sandbox",
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	var stdout bytes.Buffer
	runner.stdout = &stdout
	runner.Reset()
	runner.setVarString("ZETA", "1")
	runner.setVarString("ALPHA", "2")

	stdout.Reset()
	runner.printSetVars()
	first := stdout.String()
	if !strings.Contains(first, "ALPHA=2\n") {
		t.Fatalf("first set output missing ALPHA=2:\n%s", first)
	}
	if !strings.Contains(first, "ZETA=1\n") {
		t.Fatalf("first set output missing ZETA=1:\n%s", first)
	}
	assertSetOutputSorted(t, first)

	runner.setVarString("ALPHA", "3")
	runner.delVar("ZETA")

	stdout.Reset()
	runner.printSetVars()
	second := stdout.String()
	if !strings.Contains(second, "ALPHA=3\n") {
		t.Fatalf("second set output missing ALPHA=3:\n%s", second)
	}
	if strings.Contains(second, "ZETA=") {
		t.Fatalf("second set output still contains ZETA:\n%s", second)
	}
	assertSetOutputSorted(t, second)
}

func TestOverlayEnvironEachDoesNotCacheMutableBaseEnv(t *testing.T) {
	t.Parallel()

	base := newMutableTestEnviron("FOO=base")
	overlay := newScopedOverlayEnviron(base, false)
	child := newScopedOverlayEnviron(overlay, false)

	if _, entries := collectEachEntries(child); entries["FOO"][0].String() != "base" {
		t.Fatalf("initial child FOO = %q, want base", entries["FOO"][0].String())
	}

	base.Set("FOO", expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: "updated"})

	_, entries := collectEachEntries(child)
	if got := entries["FOO"][0].String(); got != "updated" {
		t.Fatalf("child FOO after mutable base update = %q, want updated", got)
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

func assertSetOutputSorted(t *testing.T, output string) {
	t.Helper()
	prev := ""
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		name, _, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("set output line missing '=': %q", line)
		}
		if prev != "" && prev > name {
			t.Fatalf("set output not sorted: %q before %q\n%s", prev, name, output)
		}
		prev = name
	}
}
