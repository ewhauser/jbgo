package interp

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

func TestDbracketParseErrorAbortsScript(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "[[ -z ]]; echo after\n")
	if err == nil {
		t.Fatal("runShellReader error = nil, want parse error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("error = %T, want syntax.ParseError", err)
	}
	parseErr.Filename = ""
	if parseErr.SourceLine == "" {
		parseErr.SourceLine = "[[ -z ]]; echo after"
	}
	const want = "line 1: unexpected argument `]]' to conditional unary operator\nline 1: syntax error near `]]'\nline 1: `[[ -z ]]; echo after'"
	if got := parseErr.BashError(); got != want {
		t.Fatalf("BashError() = %q, want %q", got, want)
	}
}

func TestDbracketNoCaseMatchOption(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
shopt -s nocasematch
[[ FOO == foo ]]
echo glob=$?
[[ FOO =~ foo ]]
echo regex=$?
[[ FOO != foo ]]
echo no_match=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "glob=0\nregex=0\nno_match=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDbracketRegexBareStarReportsStatusTwo(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
[[ foo.py =~ * ]]
printf 'status=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "status=2 rematch=0\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	wantReason := "Invalid preceding regular expression"
	if runtime.GOOS == "darwin" {
		wantReason = "repetition-operator operand invalid"
	}
	if !strings.Contains(stderr, "invalid regular expression `*': "+wantReason) {
		t.Fatalf("stderr = %q, want bare-star regex diagnostic", stderr)
	}
}

func TestDbracketTildeBehaviorMatchesHostBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
HOME=/home/bob
[[ ~ == /home/bob ]]
echo status=$?
[[ ~ == */bob ]]
echo status=$?
[[ ~ == */z ]]
echo status=$?

[[ ~ ]]
echo unary_status=$?
HOME=''
[[ ~ ]]
echo empty_status=$?
[[ -n ~ ]]
echo unary_n=$?

[[ ~ == ~ ]]
echo self_match=$?
[[ $HOME == ~ ]]
echo rhs_match=$?
[[ ~ == $HOME ]]
echo lhs_match=$?

HOME=foo
[[ ~ =~ $HOME ]]
echo regex_lhs=$?
[[ $HOME =~ ~ ]]
echo regex_rhs=$?

HOME='^a$'
[[ ~ =~ $HOME ]]
echo regex_lhs_anchored=$?
[[ $HOME =~ ~ ]]
echo regex_rhs_anchored=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	wantStdout := "" +
		"status=0\n" +
		"status=0\n" +
		"status=1\n" +
		"unary_status=0\n" +
		"empty_status=1\n" +
		"unary_n=1\n" +
		"self_match=0\n" +
		"rhs_match=0\n" +
		"lhs_match=0\n" +
		"regex_lhs=0\n" +
		"regex_rhs=0\n" +
		"regex_lhs_anchored=1\n" +
		"regex_rhs_anchored=0\n"
	if runtime.GOOS == "darwin" {
		wantStdout = "" +
			"status=1\n" +
			"status=1\n" +
			"status=1\n" +
			"unary_status=0\n" +
			"empty_status=0\n" +
			"unary_n=0\n" +
			"self_match=0\n" +
			"rhs_match=1\n" +
			"lhs_match=1\n" +
			"regex_lhs=1\n" +
			"regex_rhs=1\n" +
			"regex_lhs_anchored=1\n" +
			"regex_rhs_anchored=1\n"
	}
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
}

func TestDbracketArrayArithmeticMatchesBashDiagnostics(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
a=('1 3' 5)
b=('1 3' 5)
c=('1' '3 5')
d=('1' '3 6')

(( a == b ))
echo status=$?
(( a == c ))
echo status=$?
(( a == d ))
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "status=1\nstatus=1\nstatus=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "" +
		"((: 1 3: arithmetic syntax error in expression (error token is \"3\")\n" +
		"((: 1 3: arithmetic syntax error in expression (error token is \"3\")\n" +
		"((: 1 3: arithmetic syntax error in expression (error token is \"3\")\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestEvalTildeExpansionMatchesBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
HOME=/home/bob
x="~"
eval y="$x"
test "$x" = "$y" || echo FALSE
[[ $x == /* ]] || echo FALSE
[[ $y == /* ]] && echo TRUE
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "FALSE\nFALSE\nTRUE\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
