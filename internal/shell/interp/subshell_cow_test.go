package interp

import (
	"io"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
)

func TestSubshellEnvSnapshotIsolation(t *testing.T) {
	t.Parallel()

	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "background"}[background], func(t *testing.T) {
			t.Parallel()

			runner := newTestSubshellCOWRunner(t)
			runner.setVarString("SHARED_ENV", "parent")

			child := runner.subshell(background)

			runner.setVarString("PARENT_AFTER_FORK", "later")
			child.setVarString("SHARED_ENV", "child")
			child.setVarString("CHILD_ONLY", "child-only")

			if got, want := runner.lookupVar("SHARED_ENV").String(), "parent"; got != want {
				t.Fatalf("parent SHARED_ENV = %q, want %q", got, want)
			}
			if got := runner.lookupVar("CHILD_ONLY"); got.IsSet() {
				t.Fatalf("parent CHILD_ONLY = %#v, want unset", got)
			}
			if got, want := child.lookupVar("SHARED_ENV").String(), "child"; got != want {
				t.Fatalf("child SHARED_ENV = %q, want %q", got, want)
			}
			if got := child.lookupVar("PARENT_AFTER_FORK"); got.IsSet() {
				t.Fatalf("child PARENT_AFTER_FORK = %#v, want unset", got)
			}
		})
	}
}

func TestSubshellAliasIsolation(t *testing.T) {
	t.Parallel()

	runner := newTestSubshellCOWRunner(t)
	runner.ensureOwnAlias()
	runner.alias["shared"] = alias{value: "echo parent"}

	child := runner.subshell(true)

	runner.ensureOwnAlias()
	runner.alias["parent-after"] = alias{value: "echo later"}
	child.ensureOwnAlias()
	child.alias["shared"] = alias{value: "echo child"}
	child.alias["child-only"] = alias{value: "echo child-only"}

	if got, want := runner.alias["shared"].value, "echo parent"; got != want {
		t.Fatalf("parent shared alias = %q, want %q", got, want)
	}
	if _, ok := runner.alias["child-only"]; ok {
		t.Fatalf("parent child-only alias should be absent")
	}
	if got, want := child.alias["shared"].value, "echo child"; got != want {
		t.Fatalf("child shared alias = %q, want %q", got, want)
	}
	if _, ok := child.alias["parent-after"]; ok {
		t.Fatalf("child parent-after alias should be absent")
	}
}

func TestSubshellFunctionIsolation(t *testing.T) {
	t.Parallel()

	runner := newTestSubshellCOWRunner(t)
	runner.setFuncInfo("shared", funcInfo{definitionSource: "parent"})

	child := runner.subshell(true)

	runner.setFuncInfo("parent-after", funcInfo{definitionSource: "later"})
	child.setFuncInfo("shared", funcInfo{definitionSource: "child"})
	child.setFuncInfo("child-only", funcInfo{definitionSource: "child-only"})

	if got, want := runner.funcs["shared"].definitionSource, "parent"; got != want {
		t.Fatalf("parent shared func source = %q, want %q", got, want)
	}
	if _, ok := runner.funcs["child-only"]; ok {
		t.Fatalf("parent child-only function should be absent")
	}
	if got, want := child.funcs["shared"].definitionSource, "child"; got != want {
		t.Fatalf("child shared func source = %q, want %q", got, want)
	}
	if _, ok := child.funcs["parent-after"]; ok {
		t.Fatalf("child parent-after function should be absent")
	}
}

func TestSubshellCommandHashIsolation(t *testing.T) {
	t.Parallel()

	runner := newTestSubshellCOWRunner(t)
	runner.commandHashRemember("shared", "/bin/parent")

	child := runner.subshell(true)

	runner.commandHashRemember("parent-after", "/bin/later")
	child.commandHashRemember("shared", "/bin/child")
	child.commandHashRemember("child-only", "/bin/child-only")

	if got, want := runner.commandHash["shared"].path, "/bin/parent"; got != want {
		t.Fatalf("parent shared command hash = %q, want %q", got, want)
	}
	if _, ok := runner.commandHash["child-only"]; ok {
		t.Fatalf("parent child-only command hash should be absent")
	}
	if got, want := child.commandHash["shared"].path, "/bin/child"; got != want {
		t.Fatalf("child shared command hash = %q, want %q", got, want)
	}
	if _, ok := child.commandHash["parent-after"]; ok {
		t.Fatalf("child parent-after command hash should be absent")
	}
}

func TestSubshellNamedFDReleaseIsolation(t *testing.T) {
	t.Parallel()

	runner := newTestSubshellCOWRunner(t)
	runner.markNamedFDReleased("shared")

	child := runner.subshell(true)

	runner.markNamedFDReleased("parent-after")
	child.clearNamedFDReleased("shared")
	child.markNamedFDReleased("child-only")

	if !runner.namedFDReleased["shared"] {
		t.Fatalf("parent shared named FD release should remain set")
	}
	if runner.namedFDReleased["child-only"] {
		t.Fatalf("parent child-only named FD release should be absent")
	}
	if child.namedFDReleased["shared"] {
		t.Fatalf("child shared named FD release should be cleared")
	}
	if child.namedFDReleased["parent-after"] {
		t.Fatalf("child parent-after named FD release should be absent")
	}
}

func TestSubshellFDTableIsolation(t *testing.T) {
	t.Parallel()

	runner := newTestSubshellCOWRunner(t)
	shared := newShellOutputFD(io.Discard)
	runner.setFD(10, shared)

	child := runner.subshell(true)

	childFD := newShellOutputFD(io.Discard)
	child.setFD(10, childFD)
	runner.setFD(11, newShellOutputFD(io.Discard))

	if got := runner.getFD(10); got != shared {
		t.Fatalf("parent fd 10 = %p, want %p", got, shared)
	}
	if got := child.getFD(10); got != childFD {
		t.Fatalf("child fd 10 = %p, want %p", got, childFD)
	}
	if got := child.getFD(11); got != nil {
		t.Fatalf("child fd 11 = %p, want nil", got)
	}
}

func TestSubshellFrameAndDirStackIsolation(t *testing.T) {
	t.Parallel()

	runner := newTestSubshellCOWRunner(t)
	restoreBase := runner.pushFrame(execFrame{kind: frameKindFunction, label: "parent"})
	defer restoreBase()
	runner.dirStack = append(runner.dirStack, "/tmp/shared")

	child := runner.subshell(true)

	restoreChild := child.pushFrame(execFrame{kind: frameKindFunction, label: "child"})
	defer restoreChild()
	child.ensureOwnDirStack()
	child.dirStack[1] = "/tmp/child"

	if got, want := len(runner.frames), 1; got != want {
		t.Fatalf("parent frame count = %d, want %d", got, want)
	}
	if got, want := len(child.frames), 2; got != want {
		t.Fatalf("child frame count = %d, want %d", got, want)
	}
	if got, want := runner.dirStack[1], "/tmp/shared"; got != want {
		t.Fatalf("parent dir stack[1] = %q, want %q", got, want)
	}
	if got, want := child.dirStack[1], "/tmp/child"; got != want {
		t.Fatalf("child dir stack[1] = %q, want %q", got, want)
	}

	restoreParent := runner.pushFrame(execFrame{kind: frameKindFunction, label: "parent-after"})
	defer restoreParent()
	runner.ensureOwnDirStack()
	runner.dirStack = append(runner.dirStack, "/tmp/parent-after")

	if got, want := len(runner.frames), 2; got != want {
		t.Fatalf("parent frame count after push = %d, want %d", got, want)
	}
	if got, want := len(child.frames), 2; got != want {
		t.Fatalf("child frame count after parent push = %d, want %d", got, want)
	}
	if got, want := len(child.dirStack), 2; got != want {
		t.Fatalf("child dir stack length = %d, want %d", got, want)
	}
}

func TestSubshellTempScopeUnsetIsolation(t *testing.T) {
	t.Parallel()

	runner := newTestSubshellCOWRunner(t)
	tempScope := &overlayEnviron{
		parent:    runner.writeEnv,
		tempScope: true,
		values: map[string]namedVariable{
			"TEMP": {
				Name: "TEMP",
				Variable: expand.Variable{
					Set:  true,
					Kind: expand.String,
					Str:  "parent",
				},
			},
		},
		tempUnset: map[string]bool{"OLD": true},
	}
	runner.writeEnv = tempScope

	child := runner.subshell(true)
	childTemp, ok := child.writeEnv.(*overlayEnviron)
	if !ok {
		t.Fatalf("child writeEnv type = %T, want *overlayEnviron", child.writeEnv)
	}
	childTemp, ok = childTemp.parent.(*overlayEnviron)
	if !ok {
		t.Fatalf("child temp scope type = %T, want *overlayEnviron", child.writeEnv)
	}

	if !deleteCurrentScopeVar(childTemp, "TEMP") {
		t.Fatalf("deleteCurrentScopeVar() = false, want true")
	}

	if _, ok := tempScope.values["TEMP"]; !ok {
		t.Fatalf("parent temp scope lost TEMP binding")
	}
	if tempScope.tempUnset["TEMP"] {
		t.Fatalf("parent tempUnset unexpectedly contains TEMP")
	}
	if !tempScope.tempUnset["OLD"] {
		t.Fatalf("parent tempUnset lost OLD marker")
	}
	if _, ok := childTemp.values["TEMP"]; ok {
		t.Fatalf("child temp scope still contains TEMP")
	}
	if !childTemp.tempUnset["TEMP"] {
		t.Fatalf("child tempUnset missing TEMP marker")
	}
}

func newTestSubshellCOWRunner(tb testing.TB) *Runner {
	tb.Helper()

	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		tb.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()
	return runner
}
