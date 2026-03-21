package interp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func TestRunReaderWithMetadataRunsExitTrapOnLaterParseError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(`
trap 'echo cleanup' EXIT
echo work
(
`), "trap-parse.sh", "", nil)
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("Run error = %v, want syntax.ParseError", err)
	}
	if got, want := stdout.String(), "work\ncleanup\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunReaderWithMetadataExitTrapExitOverridesParseErrorStatus(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(`
trap 'echo FAILED; exit 0' EXIT
for
`), "trap-parse-exit.sh", "", nil)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout.String(), "FAILED\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); !strings.Contains(got, "syntax error near unexpected token `newline'\n") || !strings.Contains(got, "`for'\n") {
		t.Fatalf("stderr = %q, want parse error output", got)
	}
}

func TestExitTrapExitStatusDoesNotWriteStderr(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(`
trap 'false' EXIT
:
`), "trap-status.sh", "", nil)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestTrapBuiltinSupportsAliasesIgnoreAndResetPrinting(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo hup' HUP
trap '' USR1
trap 'echo term' 15
trap
echo ---
trap 0 HUP
trap - USR1
trap TERM
trap
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	linesBySignal := map[string]string{
		"SIGHUP":  "trap -- 'echo hup' SIGHUP\n",
		"SIGTERM": "trap -- 'echo term' SIGTERM\n",
		"SIGUSR1": "trap -- '' SIGUSR1\n",
	}
	var want strings.Builder
	for _, info := range trapSignalOrder {
		if line, ok := linesBySignal[info.name]; ok {
			want.WriteString(line)
		}
	}
	want.WriteString("---\n")
	if stdout != want.String() {
		t.Fatalf("stdout = %q, want %q", stdout, want.String())
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestExitTrapReportsInvalidHandlerSyntaxAtRuntime(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(`
trap 'echo <' EXIT
echo status=$?
trap
`), "trap-invalid.sh", "", nil)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	const wantStdout = "status=0\ntrap -- 'echo <' EXIT\n"
	if got := stdout.String(); got != wantStdout {
		t.Fatalf("stdout = %q, want %q", got, wantStdout)
	}
	const wantStderr = "syntax error near unexpected token `newline'\n`echo <'\n"
	if got := stderr.String(); got != wantStderr {
		t.Fatalf("stderr = %q, want %q", got, wantStderr)
	}
}

func TestTrapBuiltinRejectsSingleArgNonSignal(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'foo'
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if got, want := stdout, "status=2\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "trap: usage: trap [-Plp] [[action] signal_spec ...]\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestTrapBuiltinResetsSignalsLeftToRight(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap "echo int" INT
trap "echo e" EXIT
trap - int 0 3
trap
echo ---
trap "echo int" INT
trap "echo e" EXIT
trap - int 0 -99
echo status=$?
trap
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if got, want := stdout, "---\nstatus=1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "trap: -99: invalid signal specification\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestTrapBuiltinUnsignedResetForms(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap "echo EXIT" EXIT
echo ---
trap
echo ---
trap 0 2
trap
echo ---
trap "echo INT" INT
trap "echo EXIT" EXIT
trap 2 EXIT
trap
echo ===
trap "echo noprint" EXIT
trap 0 EXIT
echo ok0
echo ---
trap "echo noprint" EXIT
trap 07 EXIT
echo ok07
echo ---
trap "echo trap-exit" EXIT
trap -1 EXIT
echo status=$?
trap
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const wantStdout = `---
trap -- 'echo EXIT' EXIT
---
---
===
ok0
---
ok07
---
status=2
trap -- 'echo trap-exit' EXIT
`
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "trap: -1: invalid option\ntrap: usage: trap [-lp] [arg signal_spec ...]\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestTrapBuiltinTreatsSpacedNumericActionAsHandler(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap " 42 " EXIT
trap
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if got, want := stdout, "trap -- ' 42 ' EXIT\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestTrapBuiltinPrintsMultilineHandlersWithLiteralNewlines(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo 1
echo 2
echo 3' INT
trap
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "trap -- 'echo 1\necho 2\necho 3' SIGINT\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestTrapBuiltinPrintsInheritedTrapsInSubshells(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo bye' EXIT

echo '( )'
( trap )

echo '$(trap)'
echo $(trap)

echo 'trap | while read'
trap | while IFS= read -r line; do
  printf '%s\n' "$line"
done
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = `( )
trap -- 'echo bye' EXIT
$(trap)
trap -- 'echo bye' EXIT
trap | while read
trap -- 'echo bye' EXIT
`
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrTrapHonorsErrtraceInFunctions(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo root:$LINENO' ERR
f() { false; }
f
set -o errtrace
trap 'echo traced:$LINENO' ERR
f
`)
	const want = "root:4\ntraced:3\ntraced:3\ntraced:7\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	var status ExitStatus
	if !errors.As(err, &status) || status != 1 {
		t.Fatalf("Run error = %v, want exit status 1", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDebugAndReturnTrapInheritance(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo dbg:$LINENO' DEBUG
trap 'echo ret:$LINENO' RETURN
f() { echo body; }
f
set -o functrace
f
declare -ft f
set +o functrace
f
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if got := strings.Count(stdout, "body\n"); got != 3 {
		t.Fatalf("stdout = %q, want 3 function body executions", stdout)
	}
	if strings.Count(stdout, "ret:") != 2 {
		t.Fatalf("stdout = %q, want RETURN traps from functrace and declare -ft", stdout)
	}
	if !strings.Contains(stdout, "dbg:") {
		t.Fatalf("stdout = %q, want DEBUG output", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
