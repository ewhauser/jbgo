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

func TestTrapBuiltinRejectsInvalidHandlerSyntax(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo ok' EXIT
trap 'echo <' EXIT
echo status=$?
trap
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "status=1\ntrap -- 'echo ok' EXIT\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
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
