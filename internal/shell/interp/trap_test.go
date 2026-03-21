package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func runPipelineTrapScript(t *testing.T, src string) (string, string, error) {
	t.Helper()

	return runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		ExecHandler: func(ctx context.Context, args []string) error {
			hc := mustHandlerCtx(ctx)
			switch {
			case len(args) == 1 && args[0] == "cat":
				_, err := io.Copy(hc.Stdout, hc.Stdin)
				return err
			case len(args) == 2 && args[0] == "wc" && args[1] == "-l":
				data, err := io.ReadAll(hc.Stdin)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(hc.Stdout, "%8d\n", bytes.Count(data, []byte{'\n'}))
				return err
			default:
				fmt.Fprintf(hc.Stderr, "%q: executable file not found in $PATH\n", args[0])
				return ExitStatus(127)
			}
		},
	}, src)
}

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
	wantStderr := "trap: -1: invalid option\ntrap: usage: trap [-lp] [[arg] signal_spec ...]\n"
	if runtime.GOOS == "darwin" {
		wantStderr = "trap: -1: invalid option\ntrap: usage: trap [-lp] [arg signal_spec ...]\n"
	}
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

echo 'trap -p | while read'
trap -p | while IFS= read -r line; do
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
trap -p | while read
trap -- 'echo bye' EXIT
`
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestTrapBuiltinDropsInheritedDisplayTrapsAfterSubshellMutation(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo parent' EXIT

( trap - INT; trap )
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunDebugTrapTreatsParseErrorAsFailureWithExtdebug(t *testing.T) {
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
	runner.opts[optExtDebug] = true
	runner.setTrapAction(trapIDDebug, trapAction{kind: trapActionCommand, command: "echo <"})

	if got := runner.runDebugTrap(context.Background(), 12); !got {
		t.Fatalf("runDebugTrap() = false, want true")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	const wantStderr = "syntax error near unexpected token `newline'\n`echo <'\n"
	if got := stderr.String(); got != wantStderr {
		t.Fatalf("stderr = %q, want %q", got, wantStderr)
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
	const want = "root:4\ntraced:3\ntraced:7\n"
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

func TestErrTrapErrtraceAsyncListSkipsTopLevelWrapper(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "set -o errtrace\n"+
		"trap 'echo line=$LINENO' ERR\n"+
		"false & wait\n"+
		"{ false; echo async; } & wait\n")
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "line=4\nasync\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrTrapCompoundWrappersOnlyTrapInnerFailures(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "trap 'echo line=$LINENO' ERR\n"+
		"for y in 1 2; do\n"+
		"  false\n"+
		"done\n"+
		"case x in\n"+
		"  x) false ;;\n"+
		"  *) false ;;\n"+
		"esac\n"+
		"{ false; false; false; }\n"+
		"echo ok\n")
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "line=3\nline=3\nline=6\nline=9\nline=9\nline=9\nok\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrTrapRedirectFailureUsesPreviousStmtLine(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		OpenHandler: func(_ context.Context, name string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			return nil, &os.PathError{Op: "open", Path: name, Err: syscall.EROFS}
		},
	}, "trap 'echo line=$LINENO' ERR\n"+
		"false\n"+
		"{ false\n"+
		"  true\n"+
		"} > /zz\n"+
		"echo ok\n")
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const wantStdout = "line=2\nline=2\nok\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "open /zz: read-only file system\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestErrTrapPipelinesFireOncePerPipeline(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
trap 'echo first' ERR
false | false | false

trap 'echo second' ERR
{ false; } | false | false

trap 'echo assign' ERR
a=$(false) | a=$(false) | a=$(false)

trap 'echo dparen' ERR
(( 0 )) | (( 0 )) | (( 0 ))

trap 'echo dbracket' ERR
[[ a = b ]] | [[ a = b ]] | [[ a = b ]]

trap 'echo subshell' ERR
(false) | (false) | (false) | (false)

trap 'echo subshell2' ERR
(false) | (false) | (false) | (false; false)

trap 'echo group' ERR
{ false; } | { false; } | { false; }

trap 'echo group2' ERR
{ false; } | { false; } | { false; false; }

echo ok
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "" +
		"first\n" +
		"second\n" +
		"assign\n" +
		"dparen\n" +
		"dbracket\n" +
		"subshell\n" +
		"subshell2\n" +
		"group\n" +
		"group2\n" +
		"ok\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestErrTrapPipelinesObserveFinalPIPESTATUS(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
err() {
  echo "err [$@] status=$? [${PIPESTATUS[@]}]"
}
trap 'err' ERR

echo C | false
echo D | false | :

set -o pipefail
echo E | false | :

echo ok
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "" +
		"err [] status=1 [0 1]\n" +
		"err [] status=1 [0 1 0]\n" +
		"ok\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestTrapDebugVsErrLineNumbersMatchBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "trap 'echo dbg $LINENO' DEBUG\n"+
		"\n"+
		"false | false | false\n"+
		"\n"+
		"false || false || false\n"+
		"\n"+
		"! true\n"+
		"\n"+
		"trap - DEBUG\n"+
		"\n"+
		"trap 'echo err $LINENO' ERR\n"+
		"\n"+
		"false | false | false\n"+
		"\n"+
		"false || false || false\n"+
		"\n"+
		"! true\n"+
		"echo ok\n")
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "" +
		"dbg 3\n" +
		"dbg 3\n" +
		"dbg 3\n" +
		"dbg 5\n" +
		"dbg 5\n" +
		"dbg 5\n" +
		"dbg 7\n" +
		"dbg 9\n" +
		"err 13\n" +
		"err 15\n" +
		"ok\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSignalTrapLINENOUsesTrapBodyLine(t *testing.T) {
	t.Parallel()

	usr1, err := resolveTrapID("USR1")
	if err != nil {
		t.Fatalf("resolveTrapID(USR1) error = %v", err)
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		ExecHandler: func(ctx context.Context, args []string) error {
			if len(args) == 1 && args[0] == "emit-usr1" {
				hc := mustHandlerCtx(ctx)
				hc.runner.queueSignalTrap(usr1)
				return nil
			}
			return ExitStatus(127)
		},
	}, "trap 'false; echo $LINENO usr1' USR1\n"+
		"trap 'false; echo $LINENO err' ERR\n"+
		"\n"+
		"emit-usr1\n"+
		"echo after=$?\n")
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "1 err\n1 usr1\nafter=0\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDebugAndErrTrapShareTriggerLineNumbers(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "trap 'false; echo $LINENO err' ERR\n"+
		"trap 'false; echo $LINENO debug' DEBUG\n"+
		"\n"+
		"false\n"+
		"echo after=$?\n")
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "" +
		"4 err\n" +
		"4 debug\n" +
		"4 debug\n" +
		"4 debug\n" +
		"4 err\n" +
		"5 err\n" +
		"5 debug\n" +
		"after=1\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSourceReturnTrapRunsWithoutFunctrace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	helperPath := filepath.Join(dir, "return-helper.sh")
	if err := os.WriteFile(helperPath, []byte("echo return-helper.sh\nreturn 42\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", helperPath, err)
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:         dir,
		OpenHandler: sourceTestOpenHandler,
	}, "profile() {\n"+
		"  echo \"profile [$@]\"\n"+
		"}\n"+
		"g() {\n"+
		"  echo --\n"+
		"  echo g\n"+
		"  echo --\n"+
		"  return\n"+
		"}\n"+
		"f() {\n"+
		"  echo --\n"+
		"  echo f\n"+
		"  echo --\n"+
		"  g\n"+
		"}\n"+
		"trap 'profile x y' RETURN\n"+
		"f\n"+
		fmt.Sprintf(". %q\n", helperPath))
	var status ExitStatus
	if !errors.As(err, &status) || status != 42 {
		t.Fatalf("Run error = %v, want exit status 42", err)
	}
	const want = "" +
		"--\n" +
		"f\n" +
		"--\n" +
		"--\n" +
		"g\n" +
		"--\n" +
		"return-helper.sh\n" +
		"profile [x y]\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
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

func TestDebugTrapPipelinesRunInParent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name: "grouped left segment",
			script: "debuglog() { echo \"dbg:$1\"; }\n" +
				"trap 'debuglog $LINENO' DEBUG\n" +
				"{ echo pipe1;\n" +
				"  echo pipe2; } | cat\n" +
				"echo ok\n",
			want: "dbg:4\npipe1\npipe2\ndbg:5\nok\n",
		},
		{
			name: "simple two stage pipeline",
			script: "debuglog() { echo \"dbg:$1\"; }\n" +
				"trap 'debuglog $LINENO' DEBUG\n" +
				"echo pipeline | cat\n" +
				"echo ok\n",
			want: "dbg:3\ndbg:3\npipeline\ndbg:4\nok\n",
		},
		{
			name: "grouped segment before wc",
			script: "debuglog() { echo \"dbg:$1\"; }\n" +
				"trap 'debuglog $LINENO' DEBUG\n" +
				"{ echo x; echo y; } | wc -l\n",
			want: "dbg:3\n       2\n",
		},
		{
			name: "three stage pipeline",
			script: "debuglog() { echo \"dbg:$1\"; }\n" +
				"trap 'debuglog $LINENO' DEBUG\n" +
				"printf \"x\\n\" | cat | cat\n",
			want: "dbg:3\ndbg:3\ndbg:3\nx\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runPipelineTrapScript(t, tt.script)
			if err != nil {
				t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
			}
			if stdout != tt.want {
				t.Fatalf("stdout = %q, want %q", stdout, tt.want)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestDebugTrapFunctracePipelines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name: "simple stages stay parent scoped",
			script: "debuglog() { echo \"dbg:$1\"; }\n" +
				"set -o functrace\n" +
				"trap 'debuglog $LINENO' DEBUG\n" +
				"printf \"x\\n\" | cat | cat\n",
			want: "dbg:4\ndbg:4\ndbg:4\nx\n",
		},
		{
			name: "grouped left segment keeps child debug",
			script: "debuglog() { echo \"dbg:$1\"; }\n" +
				"set -o functrace\n" +
				"trap 'debuglog $LINENO' DEBUG\n" +
				"{ echo pipe1;\n" +
				"  echo pipe2; } | cat\n" +
				"echo ok\n",
			want: "dbg:5\ndbg:4\npipe1\ndbg:5\npipe2\ndbg:6\nok\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runPipelineTrapScript(t, tt.script)
			if err != nil {
				t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
			}
			if stdout != tt.want {
				t.Fatalf("stdout = %q, want %q", stdout, tt.want)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}
