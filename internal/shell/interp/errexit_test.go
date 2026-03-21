package interp

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func requireInterpExitStatus(t *testing.T, err error, want ExitStatus) {
	t.Helper()

	var status ExitStatus
	if !errors.As(err, &status) || status != want {
		t.Fatalf("Run error = %v, want exit status %d", err, want)
	}
}

func redirectOpenFailureConfig() *RunnerConfig {
	return &RunnerConfig{
		Dir: "/tmp",
		OpenHandler: func(_ context.Context, name string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			return nil, &os.PathError{Op: "open", Path: name, Err: errors.New("redirect target is a directory")}
		},
	}
}

func TestErrExitBraceGroupPreservesIgnoredAndStatus(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o errexit
{ test no = yes && echo hi; }
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stdout != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrExitNegatedFunctionCanEnableErrExitAndContinue(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
foo() {
  set -e
  false
  echo "should be executed"
}
! foo
echo "should be executed"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const wantStdout = "should be executed\nshould be executed\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrExitIgnoredSubshellConditionInheritsSuppression(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o errexit
if ( echo 1; false; echo 2; set -o errexit; echo 3; false; echo 4 ); then
  echo 5
fi
echo 6
false
echo 7
`)
	requireInterpExitStatus(t, err, 1)
	const wantStdout = "1\n2\n3\n4\n5\n6\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrExitTimeWritesTimingToStderr(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o errexit
time false
echo status=$?
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.HasPrefix(stderr, "\nreal\t") {
		t.Fatalf("stderr = %q, want leading timing output on stderr", stderr)
	}
	if !strings.Contains(stderr, "\nuser\t") || !strings.Contains(stderr, "\nsys\t") {
		t.Fatalf("stderr = %q, want user/sys timing output", stderr)
	}
}

func TestErrExitAliasRedirectFailureReturnsStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, redirectOpenFailureConfig(), `
shopt -s expand_aliases
set -o errexit
alias zz="{ echo 1; echo 2; }"
zz > /
echo alias status=$?
echo status=$?
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "open /: redirect target is a directory\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestErrExitDoubleBracketRedirectFailureReturnsStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, redirectOpenFailureConfig(), `
set -o errexit
[[ x = x ]] > /
echo dbracket status=$?
echo status=$?
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "open /: redirect target is a directory\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestErrExitArithmeticRedirectFailureReturnsStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, redirectOpenFailureConfig(), `
set -o errexit
(( 42 )) > /
echo dparen status=$?
echo status=$?
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "open /: redirect target is a directory\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestErrExitBuiltinRedirectFailureStillExits(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, redirectOpenFailureConfig(), `
set -o errexit
true > /
echo bad
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "open /: redirect target is a directory\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestErrExitAssignRedirectFailureStillExits(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, redirectOpenFailureConfig(), `
set -o errexit
assign=foo > /
echo bad
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "open /: redirect target is a directory\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestErrExitSubshellBoundaryStillExits(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o errexit
( false && true )
echo bad
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrExitCommandSubBoundaryStillExits(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o errexit
shopt -s inherit_errexit || true
x=$( false && true )
echo bad
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrTrapConditionalListsOnlyTrapOnOuterFailure(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo line=$LINENO' ERR

false || false || false
echo ok

false && false
echo ok
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const wantStdout = "line=4\nok\nok\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrTrapSkipsIgnoredErrExitContexts(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo line=$LINENO' ERR

if false; then
  echo if
fi

while false; do
  echo while
done

until false; do
  echo until
  break
done

false || false || false

false && false && false

false; false; false

echo ok
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const wantStdout = "until\nline=17\nline=21\nline=21\nline=21\nok\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
