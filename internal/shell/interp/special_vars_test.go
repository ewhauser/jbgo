package interp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func runSpecialVarScript(t *testing.T, cfg *RunnerConfig, src string) (string, string, error) {
	t.Helper()

	if cfg == nil {
		cfg = &RunnerConfig{}
	}
	clone := *cfg
	if clone.Dir == "" {
		clone.Dir = "/tmp"
	}
	var stdout strings.Builder
	var stderr strings.Builder
	clone.Stdout = &stdout
	clone.Stderr = &stderr

	runner, err := NewRunner(&clone)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(src), "special-vars.sh", "", nil)
	return stdout.String(), stderr.String(), err
}

func TestBASHPIDAndPPIDTrackSubshells(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()

	rootPID := runner.lookupVar("$").String()
	rootBASHPID := runner.lookupVar("BASHPID").String()
	rootPPID := runner.lookupVar("PPID")
	if got, want := rootPID, "1"; got != want {
		t.Fatalf("$$ = %q, want %q", got, want)
	}
	if got, want := rootBASHPID, "1"; got != want {
		t.Fatalf("BASHPID = %q, want %q", got, want)
	}
	if got, want := rootPPID.String(), "0"; got != want {
		t.Fatalf("PPID = %q, want %q", got, want)
	}
	if !rootPPID.ReadOnly {
		t.Fatalf("PPID should be readonly: %#v", rootPPID)
	}

	subshell := runner.subshell(false)
	if got, want := subshell.lookupVar("$").String(), rootPID; got != want {
		t.Fatalf("subshell $$ = %q, want %q", got, want)
	}
	if got, want := subshell.lookupVar("PPID").String(), rootPPID.String(); got != want {
		t.Fatalf("subshell PPID = %q, want %q", got, want)
	}
	if got, wantNot := subshell.lookupVar("BASHPID").String(), rootBASHPID; got == wantNot {
		t.Fatalf("subshell BASHPID = %q, want it to differ from %q", got, wantNot)
	}
}

func TestPPIDIsReadonly(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, "PPID=7\n")
	if status, ok := err.(ExitStatus); !ok || status != 1 {
		t.Fatalf("Run() error = %v, want exit status 1", err)
	}
	if got := stdout; got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr, "PPID: readonly variable\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestPIPESTATUSTracksSimpleCommandsAndPipelines(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, ""+
		"false\n"+
		"echo one:${PIPESTATUS[@]}\n"+
		"! false\n"+
		"echo two:${PIPESTATUS[@]} status:$?\n"+
		"exit 55 | (exit 44)\n"+
		"echo three:${PIPESTATUS[@]}\n"+
		"shopt -s lastpipe\n"+
		"return1() { return 1; }\n"+
		"return2() { return 2; }\n"+
		"return3() { return 3; }\n"+
		"return1 | return2 | return3\n"+
		"echo four:${PIPESTATUS[@]}\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	const want = "" +
		"one:1\n" +
		"two:1 status:0\n" +
		"three:55 44\n" +
		"four:1 2 3\n"
	if got := stdout; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
	if got := stderr; got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestPIPESTATUSStartsEmpty(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, "printf '%s\\n' \"${PIPESTATUS[@]}\"\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	if got, want := stdout, "\n"; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
	if got := stderr; got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestPIPESTATUSResetsAfterRedirectionOnlyStatement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stdout, stderr, err := runSpecialVarScript(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			if !filepath.IsAbs(name) {
				name = filepath.Join(dir, name)
			}
			return os.OpenFile(name, flag, perm)
		},
	}, ""+
		"false\n"+
		">out\n"+
		"printf '%s\\n' \"${PIPESTATUS[@]}\"\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	if got, want := stdout, "0\n"; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
	if got := stderr; got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestSECONDSReportsElapsedTime(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()
	runner.startTime = time.Now().Add(-3500 * time.Millisecond)

	value, err := strconv.Atoi(runner.lookupVar("SECONDS").String())
	if err != nil {
		t.Fatalf("Atoi(SECONDS) error = %v", err)
	}
	if value < 3 || value > 4 {
		t.Fatalf("SECONDS = %d, want 3 or 4", value)
	}
}

func TestSECONDSAssignmentResetsBaseline(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, ""+
		"SECONDS=5\n"+
		"x=$SECONDS\n"+
		"if (( x < 5 || x > 6 )); then\n"+
		"  echo bad:$x\n"+
		"else\n"+
		"  echo ok\n"+
		"fi\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	if got, want := stdout, "ok\n"; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
	if got := stderr; got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestSECONDSPrefixAssignmentRestoreKeepsElapsedTime(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()
	runner.fillExpandConfig(context.Background())
	runner.startTime = time.Now().Add(-time.Second)

	file, err := syntax.NewParser().Parse(strings.NewReader("SECONDS=5 external\n"), "seconds-prefix.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	cm, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		t.Fatalf("command = %T, want *syntax.CallExpr", file.Stmts[0].Cmd)
	}

	restores := runner.runCallAssigns(cm.Assigns)
	time.Sleep(1100 * time.Millisecond)
	runner.restoreCallAssigns(restores)

	value, err := strconv.Atoi(runner.lookupVar("SECONDS").String())
	if err != nil {
		t.Fatalf("Atoi(SECONDS) error = %v", err)
	}
	if value < 2 || value > 3 {
		t.Fatalf("SECONDS = %d, want 2 or 3", value)
	}
}

func TestSECONDSPrefixAssignmentRestoreIsLIFO(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()
	runner.fillExpandConfig(context.Background())
	runner.startTime = time.Now().Add(-time.Second)

	file, err := syntax.NewParser().Parse(strings.NewReader("SECONDS=5 SECONDS=7 external\n"), "seconds-prefix-lifo.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	cm, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		t.Fatalf("command = %T, want *syntax.CallExpr", file.Stmts[0].Cmd)
	}

	restores := runner.runCallAssigns(cm.Assigns)
	time.Sleep(1100 * time.Millisecond)
	runner.restoreCallAssigns(restores)

	value, err := strconv.Atoi(runner.lookupVar("SECONDS").String())
	if err != nil {
		t.Fatalf("Atoi(SECONDS) error = %v", err)
	}
	if value < 2 || value > 3 {
		t.Fatalf("SECONDS = %d, want 2 or 3", value)
	}
}

func TestSECONDSLocalAssignmentDoesNotLeak(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()
	runner.startTime = time.Now().Add(-time.Second)

	origEnv := runner.writeEnv
	runner.writeEnv = &overlayEnviron{parent: runner.writeEnv, funcScope: true}
	runner.setVar("SECONDS", expand.Variable{
		Set:   true,
		Local: true,
		Kind:  expand.String,
		Str:   "5",
	})
	inside, err := strconv.Atoi(runner.lookupVar("SECONDS").String())
	if err != nil {
		t.Fatalf("Atoi(inside SECONDS) error = %v", err)
	}
	if inside < 5 || inside > 6 {
		t.Fatalf("inside SECONDS = %d, want 5 or 6", inside)
	}

	runner.writeEnv = origEnv
	outside, err := strconv.Atoi(runner.lookupVar("SECONDS").String())
	if err != nil {
		t.Fatalf("Atoi(outside SECONDS) error = %v", err)
	}
	if outside < 1 || outside > 2 {
		t.Fatalf("outside SECONDS = %d, want 1 or 2", outside)
	}
}

func TestSubshellRandomIsReseeded(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()

	subshell := runner.subshell(false)
	if got, wantNot := subshell.random, runner.random; got == wantNot {
		t.Fatalf("subshell random state = %d, want it to differ from parent %d", got, wantNot)
	}
	if got, wantNot := subshell.origRandom, runner.origRandom; got == wantNot {
		t.Fatalf("subshell origRandom = %d, want it to differ from parent %d", got, wantNot)
	}
}

func TestRunnerStartupVarsOwnShellDefaults(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{
		Dir: "/tmp",
		Env: expand.ListEnviron(),
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()

	if got, want := runner.lookupVar("PATH").String(), defaultVirtualPath; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	if got, want := runner.lookupVar("SHELL").String(), defaultVirtualShell; got != want {
		t.Fatalf("SHELL = %q, want %q", got, want)
	}
	if got := runner.lookupVar("HOME"); got.IsSet() {
		t.Fatalf("HOME should be unset, got %#v", got)
	}
	if got := runner.lookupVar("PWD"); !got.Exported || got.String() != "/tmp" {
		t.Fatalf("PWD = %#v, want exported /tmp", got)
	}
	if got, want := runner.lookupVar("PS4").String(), "+ "; got != want {
		t.Fatalf("PS4 = %q, want %q", got, want)
	}
	if got, want := runner.lookupVar("IFS").String(), " \t\n"; got != want {
		t.Fatalf("IFS = %q, want %q", got, want)
	}
	if got := runner.lookupVar("SHELLOPTS"); !got.ReadOnly || got.String() != "braceexpand:hashall:interactive-comments" {
		t.Fatalf("SHELLOPTS = %#v, want readonly default value", got)
	}
}

func TestUnsetHostnameAndOSTYPEStayUnset(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, ""+
		"unset HOSTNAME OSTYPE\n"+
		"printf 'H=%s set=%s\\n' \"$HOSTNAME\" \"${HOSTNAME+set}\"\n"+
		"printf 'O=%s set=%s\\n' \"$OSTYPE\" \"${OSTYPE+set}\"\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	const want = "" +
		"H= set=\n" +
		"O= set=\n"
	if got := stdout; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
	if got := stderr; got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestHISTFILEOnlySetInteractively(t *testing.T) {
	t.Parallel()

	nonInteractive, err := NewRunner(&RunnerConfig{
		Dir: "/tmp",
		Env: expand.ListEnviron("HOME=/sandbox/home"),
	})
	if err != nil {
		t.Fatalf("NewRunner(nonInteractive) error = %v", err)
	}
	nonInteractive.Reset()
	if got := nonInteractive.lookupVar("HISTFILE"); got.IsSet() {
		t.Fatalf("non-interactive HISTFILE should be unset, got %#v", got)
	}

	interactive, err := NewRunner(&RunnerConfig{
		Dir:         "/tmp",
		Env:         expand.ListEnviron("HOME=/sandbox/home"),
		Interactive: true,
	})
	if err != nil {
		t.Fatalf("NewRunner(interactive) error = %v", err)
	}
	interactive.Reset()
	if got, want := interactive.lookupVar("HISTFILE").String(), "/sandbox/home/.bash_history"; got != want {
		t.Fatalf("interactive HISTFILE = %q, want %q", got, want)
	}
}

func TestLINENOInArithmeticUsesStatementLine(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, ""+
		"echo one\n"+
		"(( x = LINENO ))\n"+
		"echo $x\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	if got, want := stdout, "one\n2\n"; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
}

func TestLINENOInForLoopUsesStatementLine(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, ""+
		"echo one\n"+
		"for x in \\\n"+
		"  $LINENO zzz; do\n"+
		"  echo $x\n"+
		"done\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	if got, want := stdout, "one\n2\nzzz\n"; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
}

func TestPositionalZeroDefaultsToGBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSpecialVarScript(t, nil, ""+
		"fun() {\n"+
		"  case $0 in\n"+
		"    *sh) echo sh ;;\n"+
		"  esac\n"+
		"  echo $1 $2\n"+
		"}\n"+
		"fun a b\n")
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", err, stderr)
	}
	if got, want := stdout, "sh\na b\n"; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr)
	}
}
