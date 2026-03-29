package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSetPosixOptionUpdatesShellOpts(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o posix
echo "$SHELLOPTS"
set +o posix
echo "$SHELLOPTS"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "braceexpand:hashall:interactive-comments:posix\nbraceexpand:hashall:interactive-comments\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestPosixSpecialBuiltinPrefixAssignmentsPersist(t *testing.T) {
	t.Parallel()

	runner, stdout, stderr, err := runInterpScriptWithRunner(t, `
set -o posix
foo=bar :
z=Z builtin :
foo=bar readonly spam=eggs
printf 'foo=%s z=%s spam=%s\n' "$foo" "${z-unset}" "$spam"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "foo=bar z=unset spam=eggs\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	if got := runner.lookupVar("foo"); !got.IsSet() || got.String() != "bar" || !got.Exported {
		t.Fatalf("foo = %#v, want exported string value bar", got)
	}
	if got := runner.lookupVar("spam"); !got.IsSet() || got.String() != "eggs" || got.Exported || !got.ReadOnly {
		t.Fatalf("spam = %#v, want readonly non-exported string value eggs", got)
	}
	if got := runner.lookupVar("z"); got.IsSet() {
		t.Fatalf("z = %#v, want unset after builtin wrapper", got)
	}
}

func TestFunctionAndEvalPrefixAssignmentsUseDistinctTempScopes(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
unlocal() { unset -v "$1"; }
f() {
  local v
  printf 'local=%s\n' "${v-unset}"
  ( unlocal v; printf 'after=%s\n' "${v-unset}"; )
}
v=global
v=temp f
v=global
v=temp eval 'f'
printf 'outer=%s\n' "${v-unset}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"local=temp\n" +
		"after=global\n" +
		"local=temp\n" +
		"after=temp\n" +
		"outer=global\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEvalPrefixAssignmentsKeepLocalsInFunctionScope(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
v=x
f() {
  IFS= eval 'local   v=1'
  printf 'first=%s\n' "$v"
}
f
printf 'global=%s\n' "$v"

set -u
g() {
  IFS= eval "local v=\"\$*\""
  printf 'second=%s\n' "$v"
}
g h e l l o
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"first=1\n" +
		"global=x\n" +
		"second=hello\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestPosixSpecialBuiltinDispatchShadowsFunctions(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
type -t eval
eval() { echo "shell function: $1"; }
type -t eval
eval 'echo before posix'
set -o posix
type -t eval
eval 'echo after posix'
true() { echo 'true func'; }
true hi
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"builtin\n" +
		"function\n" +
		"shell function: echo before posix\n" +
		"builtin\n" +
		"after posix\n" +
		"true func\n" +
		"status=0\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestPosixRejectsSpecialBuiltinFunctionRedefinition(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o posix
eval 'echo hi'
eval() { echo 'sh func' "$@"; }
eval 'echo hi'
`)
	requireInterpExitStatus(t, err, 2)
	if got, want := stdout, "hi\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "`eval': is a special builtin\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestShiftOutOfRangeMatchesPosixModeHandling(t *testing.T) {
	t.Parallel()

	t.Run("non-posix-continues", func(t *testing.T) {
		stdout, stderr, err := runInterpScript(t, `
set -- a b
shift 3
echo status=$?
echo after
`)
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "status=1\nafter\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("posix-continues", func(t *testing.T) {
		stdout, stderr, err := runInterpScript(t, `
set -o posix
set -- a b
shift 3
echo status=$?
echo after
`)
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "status=1\nafter\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := stderr, "shift: 3: shift count out of range\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})
}

func TestSetInvalidOptionOnlyAbortsInPosixMode(t *testing.T) {
	t.Parallel()

	t.Run("non-posix-continues", func(t *testing.T) {
		stdout, stderr, err := runInterpScript(t, `
echo ok
set -o invalid_ || true
echo after
`)
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "ok\nafter\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := stderr, "set: invalid_: invalid option name\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("posix-continues", func(t *testing.T) {
		stdout, stderr, err := runInterpScript(t, `
set -o posix
echo ok
set -o invalid_ || true
echo after
`)
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "ok\nafter\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := stderr, "set: invalid_: invalid option name\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})
}

func TestReadonlyAssignmentAbortsShellExecution(t *testing.T) {
	t.Parallel()

	t.Run("non-posix-standalone-simple-list", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpScript(t, `
readonly x=1; x=2; echo hi
`)
		requireInterpExitStatus(t, err, 1)
		if got, want := stdout, ""; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := stderr, "x: readonly variable\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("non-posix-skips-rest-of-line-only", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpScript(t, `
readonly x=1; x=2; echo hi
echo next
`)
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "next\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := stderr, "x: readonly variable\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("posix-multiline", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpScript(t, `
set -o posix
readonly x=1
x=2
echo hi
`)
		requireInterpExitStatus(t, err, 127)
		if got, want := stdout, ""; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := stderr, "x: readonly variable\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})
}

func TestInteractivePosixReadonlyAssignmentDoesNotExitShell(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:         "/tmp",
		Interactive: true,
		Stdout:      &stdout,
		Stderr:      &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	runLine := func(src string) error {
		t.Helper()
		return runner.runShellReader(context.Background(), strings.NewReader(src), "interactive-readonly.sh", nil)
	}

	if err := runLine("set -o posix\n"); err != nil {
		t.Fatalf("set -o posix error = %v", err)
	}
	if err := runLine("readonly x=1\n"); err != nil {
		t.Fatalf("readonly error = %v", err)
	}
	if err := runLine("x=2\n"); err == nil {
		t.Fatalf("readonly assignment error = nil, want exit status 127")
	} else {
		requireInterpExitStatus(t, err, 127)
	}
	if runner.Exited() {
		t.Fatalf("runner should remain interactive after readonly assignment error")
	}
	if err := runLine("echo next\n"); err != nil {
		t.Fatalf("echo next error = %v", err)
	}
	if got, want := stdout.String(), "next\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "x: readonly variable\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
func TestReadonlyTempAssignmentDoesNotAbortCommand(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
readonly x=1
x=2 echo hi
echo status=$?
echo x=$x
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "hi\nstatus=0\nx=1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "x: readonly variable\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestIndexedTempAssignmentDoesNotAbortCommand(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
a=old
a[1]=x printf 'ran\n'
echo status=$?
echo a=$a
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "ran\nstatus=0\na=old\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "`a[1]': not a valid identifier\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
