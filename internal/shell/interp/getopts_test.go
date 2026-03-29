package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestGetoptsConsumesSmooshedArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		script     string
		wantStdout string
	}{
		{
			name: "single option with inline argument",
			script: `
set -- -c10
getopts "c:" opt
printf 'OPTIND=%s opt=%s OPTARG=%s\n' "$OPTIND" "$opt" "$OPTARG"
getopts "c:" opt
printf 'OPTIND=%s opt=%s OPTARG=%s\n' "$OPTIND" "$opt" "$OPTARG"
`,
			wantStdout: "OPTIND=2 opt=c OPTARG=10\nOPTIND=2 opt=? OPTARG=\n",
		},
		{
			name: "clustered option with trailing inline argument",
			script: `
set -- -abc10
getopts "abc:" opt
printf 'OPTIND=%s opt=%s OPTARG=%s\n' "$OPTIND" "$opt" "$OPTARG"
getopts "abc:" opt
printf 'OPTIND=%s opt=%s OPTARG=%s\n' "$OPTIND" "$opt" "$OPTARG"
getopts "abc:" opt
printf 'OPTIND=%s opt=%s OPTARG=%s\n' "$OPTIND" "$opt" "$OPTARG"
`,
			wantStdout: "OPTIND=1 opt=a OPTARG=\nOPTIND=1 opt=b OPTARG=\nOPTIND=2 opt=c OPTARG=10\n",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runInterpScript(t, tc.script)
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if stdout != tc.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout, tc.wantStdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestGetoptsConsumesDoubleDashOperands(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -- -a -- -c operand
while getopts "a" name; do
  printf '%s\n' "$name"
done
printf 'name=%s OPTIND=%s\n' "$name" "$OPTIND"
shift $((OPTIND - 1))
printf 'argc=%s argv=%s,%s\n' "$#" "$1" "$2"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "a\nname=? OPTIND=3\nargc=2 argv=-c,operand\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestGetoptsErrorReportingMatchesMode(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
OPTIND=1
set -- -Z
getopts ':a:' opt
printf 'silent-unknown status=%s opt=%s OPTARG=%s\n' "$?" "$opt" "$OPTARG"

OPTIND=1
set -- -a
getopts ':a:' opt
printf 'silent-missing status=%s opt=%s OPTARG=%s\n' "$?" "$opt" "$OPTARG"

OPTIND=1
set -- -Z
getopts 'a:' opt
printf 'normal-unknown status=%s opt=%s OPTARG=%s\n' "$?" "$opt" "$OPTARG"

OPTIND=1
set -- -a
getopts 'a:' opt
printf 'normal-missing status=%s opt=%s OPTARG=%s\n' "$?" "$opt" "$OPTARG"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"silent-unknown status=0 opt=? OPTARG=Z\n" +
		"silent-missing status=0 opt=: OPTARG=a\n" +
		"normal-unknown status=0 opt=? OPTARG=\n" +
		"normal-missing status=0 opt=? OPTARG=\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "" +
		"varref-test.sh: illegal option -- Z\n" +
		"varref-test.sh: option requires an argument -- a\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestGetoptsLeavesEmptyOPTARGSetForNoArgOptionUnderNounset(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -u
set -- -a
getopts "a" opt
printf 'opt=%s OPTARG=<%s>\n' "$opt" "$OPTARG"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "opt=a OPTARG=<>\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestGetoptsInvalidNameKeepsParseSideEffects(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -- -c foo -h
getopts 'hc:' opt-
printf 'status=%s OPTARG=%s OPTIND=%s\n' "$?" "$OPTARG" "$OPTIND"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=2 OPTARG=foo OPTIND=3\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "getopts: `opt-': not a valid identifier\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestGetoptsResetsOPTINDAfterSetParams(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
while getopts "hc:" opt; do
  echo '-'
done
echo OPTIND=$OPTIND

set -- -h -c foo x
while getopts "hc:" opt; do
  echo '-'
done
echo OPTIND=$OPTIND

set --
while getopts "hc:" opt; do
  echo '-'
done
echo OPTIND=$OPTIND

set -- -a
while getopts "ab:" opt; do
  echo '.'
done
echo OPTIND=$OPTIND

set -- -c -d -e foo
while getopts "cde:" opt; do
  echo '+'
done
echo OPTIND=$OPTIND

set -- -f
while getopts "f:" opt; do
  echo '_'
done
echo OPTIND=$OPTIND
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"OPTIND=1\n" +
		"-\n" +
		"-\n" +
		"OPTIND=4\n" +
		"OPTIND=1\n" +
		".\n" +
		"OPTIND=2\n" +
		"+\n" +
		"+\n" +
		"OPTIND=5\n" +
		"OPTIND=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestGetoptsPrefixesDiagnosticsWithScriptName(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}
	err = runner.runShellReader(context.Background(), strings.NewReader(`
set -- -Z -a
getopts 'a:' opt
getopts 'a:' opt
`), "/tmp/getopts-prefix.sh", nil)
	if err != nil {
		t.Fatalf("runShellReader() error = %v", err)
	}
	if got, want := stdout.String(), ""; got != want {
		t.Fatalf("stdout = %q, want empty", got)
	}
	const wantStderr = "" +
		"/tmp/getopts-prefix.sh: illegal option -- Z\n" +
		"/tmp/getopts-prefix.sh: option requires an argument -- a\n"
	if got := stderr.String(); got != wantStderr {
		t.Fatalf("stderr = %q, want %q", got, wantStderr)
	}
}

func TestGetoptsResetsClusterStateWhenOPTINDIsReset(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -- -ab
getopts "ab" opt
printf 'first=%s OPTIND=%s\n' "$opt" "$OPTIND"
OPTIND=1
getopts "ab" opt
printf 'second=%s OPTIND=%s\n' "$opt" "$OPTIND"
getopts "ab" opt
printf 'third=%s OPTIND=%s\n' "$opt" "$OPTIND"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "first=a OPTIND=1\nsecond=a OPTIND=1\nthird=b OPTIND=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestGetoptsLocalOPTINDDoesNotResetCallerState(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -- -ab
getopts "ab" opt
printf 'outer1=%s OPTIND=%s\n' "$opt" "$OPTIND"
helper() {
  local OPTIND=1
  getopts "ab" inner "$@"
  printf 'inner=%s OPTIND=%s\n' "$inner" "$OPTIND"
}
helper "$@"
getopts "ab" opt
printf 'outer2=%s OPTIND=%s\n' "$opt" "$OPTIND"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "outer1=a OPTIND=1\ninner=a OPTIND=1\nouter2=b OPTIND=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestGetoptsExportsOPTINDInAllexportMode(t *testing.T) {
	t.Parallel()

	runner, stdout, stderr, err := runInterpScriptWithRunner(t, `
set -a
set -- -a
getopts "a" opt
printf 'OPTIND=%s\n' "$OPTIND"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "OPTIND=2\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "OPTIND=2\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if got := runner.lookupVar("OPTIND"); !got.Exported || got.String() != "2" {
		t.Fatalf("OPTIND = %#v, want exported string 2", got)
	}
}

func TestGetoptsSubshellKeepsParentStateIsolated(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -- -ab
getopts "ab" opt
printf 'outer1=%s OPTIND=%s\n' "$opt" "$OPTIND"
(
  getopts "ab" inner
  printf 'sub=%s OPTIND=%s\n' "$inner" "$OPTIND"
)
getopts "ab" opt
printf 'outer2=%s OPTIND=%s\n' "$opt" "$OPTIND"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "outer1=a OPTIND=1\nsub=b OPTIND=2\nouter2=b OPTIND=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
