package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRedirectRegressionSupportsOverwriteAppendAndInputRedirection(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo first > log.txt\n echo second >> log.txt\n cat < log.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "first\nsecond\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRedirectRegressionDirectoryOpenFailureOnlyFailsTheCommand(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir dir\n echo foo > ./dir\n echo status=$?\n printf foo > ./dir\n echo status=$?\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\nstatus=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "./dir: Is a directory\n./dir: Is a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestPipelineRegressionChainsShellAndRegistryCommands(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'alpha\nbeta\nbeta\n' > words.txt\n cat words.txt | grep beta | head -n 1\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionCatStreamsCharacterDevicesThroughHead(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := session.Exec(ctx, &ExecutionRequest{
		Script: "cat /dev/zero | cat | head -c 5 | tr '\\0' 0; echo\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "00000\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestPipelineRegressionCatReportsSIGPIPEForVirtualUrandom(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := session.Exec(ctx, &ExecutionRequest{
		Script: "cat /dev/urandom | sleep 0.01\necho ${PIPESTATUS[@]}\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "141 0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestPipelineRegressionDoesNotLeakPipedLoopVariableMutations(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"count=0\n"+
		"printf '%s\\n' alpha beta gamma | while IFS= read -r _; do count=$((count + 1)); done\n"+
		"printf 'after-pipeline:%s\\n' \"$count\"\n"+
		"while IFS= read -r _; do count=$((count + 1)); done <<'EOF'\n"+
		"delta\n"+
		"epsilon\n"+
		"EOF\n"+
		"printf 'after-heredoc:%s\\n' \"$count\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "after-pipeline:0\nafter-heredoc:2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionDoesNotPersistFinalStageReadVariable(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"unset value\n"+
		"printf 'hello\\n' | read -r value\n"+
		"printf 'value:<%s>\\n' \"${value-}\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "value:<>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionLastpipePersistsFinalStageReadVariable(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"shopt -s lastpipe\n"+
		"unset value\n"+
		"printf 'hello\\n' | read -r value\n"+
		"printf 'value:<%s>\\n' \"${value-}\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "value:<hello>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionLastpipePersistsMapfileAndLoopState(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"shopt -s lastpipe\n"+
		"seq 2 | mapfile m\n"+
		"seq 3 | readarray r\n"+
		"i=0\n"+
		"seq 3 | while read -r _; do (( i++ )); done\n"+
		"printf 'm=%s r=%s i=%s\\n' \"${#m[@]}\" \"${#r[@]}\" \"$i\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "m=2 r=3 i=3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionLastpipeDoesNotUnwrapUserSubshellTail(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"shopt -s lastpipe\n"+
		"unset v\n"+
		"printf 'x\\n' | (read -r v; echo \"inner:$v\")\n"+
		"printf 'outer:<%s>\\n' \"${v-}\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "inner:x\nouter:<>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionNestedShellCommandsInheritHereDocFDs(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/bin/read_from_fd.sh", []byte("#!/bin/bash\n\nfor fd in \"$@\"; do\n  printf '%s: ' \"$fd\"\n  if ! eval \"cat <&$fd\"; then\n    printf 'FATAL: Error reading from fd %s\\n' \"$fd\" >&2\n    exit 1\n  fi\ndone\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/bin/read_from_fd.sh", 0o755); err != nil {
		t.Fatalf("Chmod(read_from_fd.sh) error = %v", err)
	}

	result := mustExecSession(t, session, ""+
		"read_from_fd.sh 3 3<<EOF3 | read_from_fd.sh 0 5 5<<EOF5\n"+
		"fd3\n"+
		"EOF3\n"+
		"fd5\n"+
		"EOF5\n"+
		"echo ok\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0: 3: fd3\n5: fd5\nok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q; stderr=%q", got, want, result.Stderr)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestErrTrapRegressionCommandStringLeadingBlankUsesSpecLineNumbers(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	if err := session.FileSystem().MkdirAll(context.Background(), "/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll(/dir) error = %v", err)
	}

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Interpreter:     "bash",
		PassthroughArgs: []string{"-c"},
		Script:          "\ntrap 'echo line=$LINENO' ERR\n\nfalse\n\n{ false \n  true\n} > /dir\necho ok\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "line=3\nline=7\nok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stderr, "/dir: Is a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestRedirectRegressionForLoopCommandSubstitutionKeepsRedirectedStdout(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"for i in $(seq 3); do\n"+
		"  echo \"$i\"\n"+
		"done > /tmp/redirect-for-loop.txt\n"+
		"cat /tmp/redirect-for-loop.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n2\n3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestSourceRegressionRespectsShoptSourcepath(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/pathbin/lib.sh", []byte("printf 'frompath\\n'\n"))

	result := mustExecSession(t, session, ""+
		"PATH=/tmp/pathbin\n"+
		"shopt -u sourcepath\n"+
		"if source lib.sh; then echo off=ok; else echo off=fail; fi\n"+
		"shopt -s sourcepath\n"+
		"if source lib.sh; then echo on=ok; else echo on=fail; fi\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "off=fail\nfrompath\non=ok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "lib.sh") {
		t.Fatalf("Stderr = %q, want source failure mentioning lib.sh", result.Stderr)
	}
}

func TestCommandSubstitutionRegressionFeedsExpandedArgs(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/note.txt", []byte("sandbox\n"))

	result := mustExecSession(t, session, "name=$(cat note.txt)\n echo \"hello $name\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello sandbox\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestConditionalRegressionSupportsBuiltinStringPredicates(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "status=ready\n if test \"$status\" = ready; then echo exists; else echo missing; fi\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "exists\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLoopRegressionSupportsForControlFlow(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "for name in alpha beta; do echo \"item:$name\"; done\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "item:alpha\nitem:beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionSupportsLiteralArithmeticExpressions(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"let y=4+5 z=6+7\n"+
		"printf '%s,%s\\n' \"$y\" \"$z\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "9,13\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDeclarationRegressionIgnoresPrefixEnvAssignments(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"f() {\n"+
		"  E=env local l=var\n"+
		"  E=env declare d=var\n"+
		"  E=env export x=var\n"+
		"  E=env readonly r=var\n"+
		"  E=env typeset t=var\n"+
		"  printf 'E:<%s> l:%s d:%s x:%s r:%s t:%s\\n' \"${E-}\" \"$l\" \"$d\" \"$x\" \"$r\" \"$t\"\n"+
		"}\n"+
		"f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "E:<> l:var d:var x:var r:var t:var\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestUnsetRegressionExcludesVariablesFromPrefixExpansion(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"foo=1\n"+
		"foobar=2\n"+
		"unset foo\n"+
		"star=${!foo*}\n"+
		"at=${!foo@}\n"+
		"printf 'star:%s\\nat:%s\\n' \"$star\" \"$at\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "star:foobar\nat:foobar\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestUnsetRegressionDoesNotLeakVarsIntoCommandEnv(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"FOO=bar\n"+
		"unset FOO\n"+
		"printenv FOO || echo missing\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "missing\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestProcessSubstitutionRegressionSupportsInputOutputAndPipePredicates(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"cat <(echo hello)\n"+
		"while IFS= read -r line; do printf 'loop:%s\\n' \"$line\"; done < <(printf 'a\\nb\\n')\n"+
		"printf 'hello-out\\n' > >(cat > /tmp/out)\n"+
		"while [ ! -s /tmp/out ]; do sleep 0.01; done\n"+
		"p=<(echo hi)\n"+
		"[ -p \"$p\" ] && cat \"$p\"\n"+
		"q=>(cat > /tmp/out2)\n"+
		"[ -p \"$q\" ] && printf 'write-side\\n' > \"$q\"\n"+
		"while [ ! -s /tmp/out2 ]; do sleep 0.01; done\n"+
		"cat /tmp/out\n"+
		"cat /tmp/out2\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello\nloop:a\nloop:b\nhi\nhello-out\nwrite-side\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
