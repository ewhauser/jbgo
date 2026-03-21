package interp

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func runInterpScript(t *testing.T, src string) (string, string, error) {
	t.Helper()

	return runInterpScriptConfig(t, &RunnerConfig{Dir: "/tmp"}, src)
}

func runInterpScriptConfig(t *testing.T, cfg *RunnerConfig, src string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	if cfg == nil {
		cfg = &RunnerConfig{Dir: "/tmp"}
	}
	cfg = &RunnerConfig{
		StartupHome:      cfg.StartupHome,
		Env:              cfg.Env,
		Dir:              cfg.Dir,
		Params:           cfg.Params,
		Interactive:      cfg.Interactive,
		LegacyBashCompat: cfg.LegacyBashCompat,
		Stdout:           &stdout,
		Stderr:           &stderr,
		CallHandler:      cfg.CallHandler,
		ExecHandler:      cfg.ExecHandler,
		OpenHandler:      cfg.OpenHandler,
		ReadDirHandler:   cfg.ReadDirHandler,
		StatHandler:      cfg.StatHandler,
		RealpathHandler:  cfg.RealpathHandler,
		ProcSubstHandler: cfg.ProcSubstHandler,
	}
	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.runShellReader(context.Background(), strings.NewReader(src), "varref-test.sh", nil)
	return stdout.String(), stderr.String(), err
}

func runInterpCommandString(t *testing.T, src string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:                "/tmp",
		CommandString:      true,
		CommandStringValue: src,
		Stdout:             &stdout,
		Stderr:             &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(src), "dummy0", "", nil)
	return stdout.String(), stderr.String(), err
}

func TestForLoopInvalidIdentifierMatchesBash(t *testing.T) {
	t.Parallel()

	t.Run("continues after invalid name", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpScript(t, "for i.j in a b c; do echo hi; done; echo done\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "done\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := stderr, "`i.j': not a valid identifier\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("standalone loop returns status one", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpScript(t, "for - in a b c; do echo hi; done\n")
		var status ExitStatus
		if !errors.As(err, &status) || status != 1 {
			t.Fatalf("Run error = %v, want exit status 1", err)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if got, want := stderr, "`-': not a valid identifier\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})
}

func TestRecoverableNestedArrayLiteralParseError(t *testing.T) {
	t.Parallel()

	t.Run("later lines continue", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpScript(t, "a=( inside=() )\necho len=${#a[@]}\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "len=0\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		const wantStderr = "varref-test.sh: line 1: syntax error near unexpected token `('\nvarref-test.sh: line 1: `a=( inside=() )'\n"
		if got := stderr; got != wantStderr {
			t.Fatalf("stderr = %q, want %q", got, wantStderr)
		}
	})

	t.Run("same line is discarded but next line runs", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpScript(t, "a=( inside=() ); echo first\necho second\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "second\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		const wantStderr = "varref-test.sh: line 1: syntax error near unexpected token `('\nvarref-test.sh: line 1: `a=( inside=() ); echo first'\n"
		if got := stderr; got != wantStderr {
			t.Fatalf("stderr = %q, want %q", got, wantStderr)
		}
	})
}

func TestPrintfVarRef(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
var=foo
printf -v $var %s 'hello there'
a=(a b c)
printf -v 'a[1]' %s 'foo'
declare -A assoc=([k]=v)
key=k
printf -v 'assoc[$key]' %s 'bar'
declare -n mapref=assoc
printf -v 'mapref[$key]' %s 'baz'
printf '%s\n' "$foo"
printf '%s\n' "${a[@]}"
printf '%s\n' "${assoc[k]}"
printf -v 'a[' %s 'foo'
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "hello there\na\nfoo\nc\nbaz\nstatus=2\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if got, want := stderr, "printf: `a[': not a valid identifier\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestVarRefNamerefAndTests(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
typeset -A assoc=([k]=v)
key=k
test -v 'assoc[$key]'
printf 'test=%d\n' "$?"
[[ -v assoc[$key] ]]
printf 'dbracket=%d\n' "$?"
[[ -v assoc[k]z ]]
printf 'junk=%d\n' "$?"
declare -n ref='assoc[$key]'
test -R ref
printf 'refvar=%d\n' "$?"
ref=x
printf '%s\n' "${assoc[k]}"
array=(X Y Z)
typeset -n whole=array
whole[0]=xx
printf '%s\n' "${array[*]}"
typeset -n elem='array[0]'
elem[0]=foo
printf 'nested=%d\n' "$?"
printf '%s\n' "${array[*]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "test=0\ndbracket=0\njunk=1\nrefvar=0\nx\nxx Y Z\nnested=1\nxx Y Z\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestAssociativeVarRefsSupportNonWordSubscripts(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
declare -A assoc
assoc[i]=one
assoc[i+1]=two
printf '%s\n' "${assoc[i]}"
printf '%s\n' "${assoc[i+1]}"
test -v 'assoc[i+1]'
printf 'isset=%d\n' "$?"
unset -v 'assoc[i]'
printf '%s\n' "${assoc[i]-missing}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "one\ntwo\nisset=0\nmissing\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestUnsetRevealsOuterScopedBinding(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
unlocal() { unset "$@"; }

level2() {
  local hello=yy
  echo level2=$hello
  unlocal hello
  echo level2=$hello
}

level1() {
  local hello=xx
  level2
  echo level1=$hello
  unlocal hello
  echo level1=$hello
  level2
}

hello=global
level1
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"level2=yy\n" +
		"level2=xx\n" +
		"level1=xx\n" +
		"level1=global\n" +
		"level2=yy\n" +
		"level2=global\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestUnsetCurrentScopeLocalPreservesShadow(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
x=global
f() {
  local x=local
  unset x
  printf 'value=%s\n' "${x-unset}"
  declare -p x
  local -p x
}
f
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "value=unset\ndeclare -- x\ndeclare -- x\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestUnsetWrongTypeMatchesBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
	declare undef
	unset -v 'undef[1]'
	echo undef1=$?
	unset -v 'undef["key"]'
	echo undef_key=$?
	`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "undef1=1\nundef_key=0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "unset: undef: not an array variable\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestAssociativeScalarAssignmentUsesZeroKey(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
typeset -A assoc=([k]=v)
assoc=99
printf '%s|%s\n' "${assoc[0]}" "${assoc[k]}"
assoc+=42
printf '%s|%s\n' "${assoc[0]}" "${assoc[k]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "99|v\n9942|v\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestInlineArrayBindingsReachCommandEnv(t *testing.T) {
	t.Parallel()

	file, err := syntax.NewParser().Parse(strings.NewReader("A=a B=(b b) C=([k]=v) external\n"), "inline-array-env.sh")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}

	var got string
	runner, err := NewRunner(&RunnerConfig{
		Dir: "/tmp",
		ExecHandler: func(ctx context.Context, args []string) error {
			hc, ok := LookupHandlerContext(ctx)
			if !ok {
				t.Fatal("missing handler context")
			}
			got = hc.Env.Get("A").String() + "|" + hc.Env.Get("B").String() + "|" + hc.Env.Get("C").String()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	if err := runner.Run(context.Background(), file); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got != "a|(b b)|([k]=v)" {
		t.Fatalf("env = %q, want %q", got, "a|(b b)|([k]=v)")
	}
}

func TestInlineArrayBindingsExpandBeforeCommandEnv(t *testing.T) {
	t.Parallel()

	var got string
	_, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		ExecHandler: func(ctx context.Context, args []string) error {
			hc, ok := LookupHandlerContext(ctx)
			if !ok {
				t.Fatal("missing handler context")
			}
			got = hc.Env.Get("A").String()
			return nil
		},
	}, `
A=($(echo side >&2; printf '%s\n' one two)) external
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got != "(one two)" {
		t.Fatalf("env = %q, want %q", got, "(one two)")
	}
	if stderr != "side\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "side\n")
	}
}
func TestInlineArrayBindingAppendOverwritesCommandEnvValue(t *testing.T) {
	t.Parallel()

	var got []string
	_, _, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		ExecHandler: func(ctx context.Context, args []string) error {
			hc, ok := LookupHandlerContext(ctx)
			if !ok {
				t.Fatal("missing handler context")
			}
			got = append(got, hc.Env.Get("A").String())
			return nil
		},
	}, `
A=foo A+=(bar baz) external
A=foo
A+=(bar baz) external
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	want := []string{"(bar baz)", "(bar baz)"}
	if len(got) != len(want) {
		t.Fatalf("env values = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestInlineAssocArrayBindingKeepsLiteralTildeKeys(t *testing.T) {
	t.Parallel()

	var got string
	_, _, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		Env: expand.ListEnviron("HOME=/home/live"),
		ExecHandler: func(ctx context.Context, args []string) error {
			hc, ok := LookupHandlerContext(ctx)
			if !ok {
				t.Fatal("missing handler context")
			}
			got = hc.Env.Get("A").String()
			return nil
		},
	}, `
A=([\~]=v [$HOME]=x [$(printf '%s' key)]=y) external
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got != "([~]=v [/home/live]=x [key]=y)" {
		t.Fatalf("env = %q, want %q", got, "([~]=v [/home/live]=x [key]=y)")
	}
}
func TestInlineArrayBindingPreservesLiteralArithmeticSubscripts(t *testing.T) {
	t.Parallel()

	var got string
	_, _, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		ExecHandler: func(ctx context.Context, args []string) error {
			hc, ok := LookupHandlerContext(ctx)
			if !ok {
				t.Fatal("missing handler context")
			}
			got = hc.Env.Get("A").String()
			return nil
		},
	}, `
A=([1+1]=x [ 1+2 ]=y) external
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got != "([1+1]=x [ 1+2 ]=y)" {
		t.Fatalf("env = %q, want %q", got, "([1+1]=x [ 1+2 ]=y)")
	}
}
func TestIndirectExpansionSupportsPositionalRefs(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
set -- one two
name=1
printf '%s\n' "${!name}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "one\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "one\n")
	}
}

func TestInvalidArrayParamExpansionsFailAsBadSubstitution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		stderr string
	}{
		{
			name: "multiple subscripts",
			src: `
a=('123' '456')
argv.sh "${a[0]}" "${a[0][0]}"
`,
			stderr: "${a[0][0]}: bad substitution\n",
		},
		{
			name: "length plus replacement",
			src: `
a=('123' '456')
echo "${#a[0]}" "${#a[0]/1/xxx}"
`,
			stderr: "${#a[0]/1/xxx}: bad substitution\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runInterpScript(t, tt.src)
			var status ExitStatus
			if !errors.As(err, &status) || status != 1 {
				t.Fatalf("status err = %v, want exit status 1", err)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if stderr != tt.stderr {
				t.Fatalf("stderr = %q, want %q", stderr, tt.stderr)
			}
		})
	}
}

func TestArrayMemberCompoundAssignmentFailsAtRuntime(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
a=(1 2)
a[0]=(3 4)
echo "status=$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\n")
	}
	if stderr != "a[0]: cannot assign list to array member\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "a[0]: cannot assign list to array member\n")
	}
}

func TestSparseArrayPatternReplacementAnchors(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
a=(v{0..9})
unset -v 'a[2]' 'a[3]' 'a[4]' 'a[7]'
printf '[%s]\n' "${a[@]/#?}"
printf '[%s]\n' "${a[@]/%?}"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "[0]\n[1]\n[5]\n[6]\n[8]\n[9]\n[v]\n[v]\n[v]\n[v]\n[v]\n[v]\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestIndirectExpansionSupportsMultiDigitPositionalRefs(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
set -- zero one two three four five six seven eight nine ten eleven
name=10
printf '%s\n' "${10}"
printf '%s\n' "${!name}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "nine\nnine\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "nine\nnine\n")
	}
}

func TestIndirectExpansionPreservesQuotedAllElementsTargets(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
set -- 'a b' c
name='@'
printf '<%s>|<%s>\n' "${!name}"
name='*'
printf '<%s>\n' "${!name}"
arr=('x y' z)
name='arr[@]'
printf '<%s>|<%s>\n' "${!name}"
name='arr[*]'
printf '<%s>\n' "${!name}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "<a b>|<c>\n<a b c>\n<x y>|<z>\n<x y z>\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestEvalQuotedParamQRoundTrips(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
x="FOO'BAR spam\"eggs"
eval "new=${x@Q}"
test "$x" = "$new" && echo OK
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stdout != "OK\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "OK\n")
	}
}

func TestEvalPrintfQRoundTrips(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
val='"quoted" with spaces and \'
printf -v foo %q "$val"
eval "bar=$foo"
test "$val" = "$bar" && echo OK
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stdout != "OK\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "OK\n")
	}
}

func TestPrintfTimeUsesStickyExportedTZOnLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("bash printf time-format timezone process semantics are Linux-specific in conformance")
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		Env: expand.ListEnviron("HOME=/tmp"),
	}, `
export TZ=Asia/Tokyo
printf '%(%Y-%m-%d)T\n' 1557978599
export TZ=US/Eastern
printf '%(%Y-%m-%d)T\n' 1557978599
unset TZ
printf '%(%Y-%m-%d)T\n' 1557978599
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if want := "2019-05-16\n2019-05-16\n2019-05-16\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestPrintfTimeStickyTZSurvivesCommandSubstitutionOnLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("bash printf time-format timezone process semantics are Linux-specific in conformance")
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		Env: expand.ListEnviron("HOME=/tmp"),
	}, `
export TZ=Portugal
tz=$(printf '%(%Y-%m-%d %H:%M:%S)T\n' 1557978599)
unset TZ
localtime=$(printf '%(%Y-%m-%d %H:%M:%S)T\n' 1557978599)
if ! test "$localtime" = "$tz"; then
  echo not-equal
fi
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

func TestInvalidIndirectExpansionIsNonFatalInSimpleCommand(t *testing.T) {
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
echo before
x='a b'
echo ${!x}
echo status=$?
echo after
`), "varref-nonfatal-test.sh", nil)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout.String(), "before\nstatus=1\nafter\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "a b: invalid variable name\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if runner.exit.code != 0 {
		t.Fatalf("exit code = %d, want 0", runner.exit.code)
	}
	if runner.exit.exiting {
		t.Fatal("runner should not mark invalid indirect expansion as exiting here")
	}
}

func TestIndirectArrayIndexTildeReportsOperandExpected(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
a=(x y)
PWD=1
ref='a[~+]'
echo ${!ref}
`)
	if status, ok := err.(ExitStatus); !ok || status != 1 {
		t.Fatalf("Run error = %v, want exit status 1", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "~+: arithmetic syntax error: operand expected (error token is \"+\")\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestIndirectExpansionMalformedArrayRefsStayRuntimeErrors(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
f() {
  local result
  result="${!1}"
  printf 'unreachable\n'
}
a=(x y)
declare -A aa=([k]=r)
f 'a[0'
printf 'status=%d\n' "$?"
f 'aa[k'
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=1\nstatus=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\nstatus=1\n")
	}
	if stderr != "a[0: invalid variable name\naa[k: invalid variable name\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "a[0: invalid variable name\naa[k: invalid variable name\n")
	}
}

func TestInvalidIndirectExpansionInAssignmentReturnsFromFunction(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
x='a b'
echo before-top
v="${!x}"
echo "after-top status=$?"
f() {
  echo before-fn
  local v
  v="${!x}"
  echo "after-fn status=$?"
}
f
echo "after-call status=$?"
echo before-simple
echo ${!x}
echo "after-simple status=$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"before-top\n" +
		"after-top status=1\n" +
		"before-fn\n" +
		"after-call status=1\n" +
		"before-simple\n" +
		"after-simple status=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "" +
		"a b: invalid variable name\n" +
		"a b: invalid variable name\n" +
		"a b: invalid variable name\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestIndirectExpansionArraySubscriptCompatibility(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
f() {
  local val=$(echo "${!1}")
  if test "$val" = y; then
    echo "works: $1"
  fi
}
a=(x y)
f 'a[1]'
f 'a["1"]'
f 'a[{1,0}]'
f 'a[<(echo x)]'
aa="1 0"
f 'a[$aa]'
f 'a[b*]'
f 'a[1"]'
b=1
f 'a[$b]'
f 'a[${c:-1}]'
f 'a[$(echo 1)]'
f 'a[$(( 3 - 2 ))]'
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "works: a[1]\nworks: a[\"1\"]\nworks: a[$b]\nworks: a[${c:-1}]\nworks: a[$(echo 1)]\nworks: a[$(( 3 - 2 ))]\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "" +
		"{1,0}: arithmetic syntax error: operand expected (error token is \"{1,0}\")\n" +
		"<(echo x): arithmetic syntax error: operand expected (error token is \"<(echo x)\")\n" +
		"1 0: arithmetic syntax error in expression (error token is \"0\")\n" +
		"b*: arithmetic syntax error: operand expected (error token is \"*\")\n" +
		"a[1\"]: invalid variable name\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestIndirectQuotedArrayDefaultsMatchBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
test_hyphen() {
  ref='a[@]'
  echo "ref=a[@]: '${!ref-no-colon}' '${!ref:-with-colon}'"
  ref='a[*]'
  echo "ref=a[*]: '${!ref-no-colon}' '${!ref:-with-colon}'"
}

a=()
test_hyphen
a=("")
test_hyphen
a=("" "")
test_hyphen
IFS=
test_hyphen
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"ref=a[@]: 'no-colon' 'with-colon'\n" +
		"ref=a[*]: 'no-colon' 'with-colon'\n" +
		"ref=a[@]: '' ''\n" +
		"ref=a[*]: '' ''\n" +
		"ref=a[@]: ' ' ' '\n" +
		"ref=a[*]: ' ' ' '\n" +
		"ref=a[@]: ' ' ' '\n" +
		"ref=a[*]: '' ''\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestBashVarOpTransformsMatchCompatibilityCases(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
x='abc DEF'
echo "${x@u}"
echo "${x@U}"
echo "${x@L}"

empty=''
x=x
echo ${x@K} ${empty@K} ${undef@K} ${x@K}
echo ${x@k} ${empty@k} ${undef@k} ${x@k}
echo ${x@A} ${empty@A} ${undef@A} ${x@A}
declare -r x
echo ${x@a} ${empty@a} ${undef@a} ${x@a}
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const wantStdout = "Abc DEF\nABC DEF\nabc def\n'x' '' 'x'\n'x' '' 'x'\nx='x' empty='' x='x'\nr r\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestIndirectAttributeTransformMatchesBashEmptyAssocTarget(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -A A=(["x"]=y)
echo x=${!A[@]@a}
echo invalid=${!A@a}
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stdout != "x=\ninvalid=\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "x=\ninvalid=\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNounsetVarOpsReturnStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -u
(echo ${undef}); echo "stat: $?"
(echo ${undef@Q}); echo "stat: $?"
(echo ${undef@P}); echo "stat: $?"
(echo ${undef@a}); echo "stat: $?"
x=$(echo ${undef@Q}); echo "stat: $?"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const wantStdout = "stat: 1\nstat: 1\nstat: 1\nstat: 1\nstat: 1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "undef: unbound variable\nundef: unbound variable\nundef: unbound variable\nundef: unbound variable\nundef: unbound variable\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestTopLevelNounsetStillExits127(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -u
echo ${undef}
echo after
`)
	if status, ok := err.(ExitStatus); !ok || status != 127 {
		t.Fatalf("Run error = %v, want exit status 127", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "undef: unbound variable\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "undef: unbound variable\n")
	}
}

func TestPositionalAttributeTransformReturnsEmpty(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:    "/tmp",
		Params: []string{"a", "b", "c"},
	}, `
echo ${@@a}
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stdout != "\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunCallAssignsRestoresResolvedNameRefTargets(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
declare -n ref=foo
foo=old
ref=temp printf x >/dev/null
printf 'foo=%s ref=%s\n' "$foo" "$ref"
arr=(x y)
declare -n elem='arr[1]'
elem=temp printf x >/dev/null
printf 'arr=%s %s\n' "${arr[0]}" "${arr[1]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "foo=old ref=old\narr=x y\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestAllElementsVarRefsRespectAssociativeKeys(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
typeset -A assoc=([k]=v)
test -v 'assoc[@]'
printf 'test-before=%d\n' "$?"
[[ -v assoc[@] ]]
printf 'dbracket-before=%d\n' "$?"
assoc[@]=x
printf -v 'assoc[*]' %s y
test -v 'assoc[@]'
printf 'test-after=%d\n' "$?"
[[ -v assoc[*] ]]
printf 'dbracket-after=%d\n' "$?"
at=@
star='*'
printf 'assoc=%s|%s\n' "${assoc[$at]}" "${assoc[$star]}"
array=(a b)
printf -v 'array[@]' %s z
printf 'printf-status=%d array=%s|%s\n' "$?" "${array[0]}" "${array[1]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "test-before=1\ndbracket-before=1\ntest-after=0\ndbracket-after=0\nassoc=x|y\nprintf-status=2 array=a|b\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "printf: bad array subscript\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestAssociativeExpressionKeysRoundTrip(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -A from_decl[1+2]=x
printf 'decl=%s\n' "${from_decl[1+2]}"
declare -A assoc
assoc[3+4]=y
printf 'assign=%s\n' "${assoc[3+4]}"
printf -v 'assoc[5+6]' %s z
printf 'printf-status=%d value=%s\n' "$?" "${assoc[5+6]}"
[[ -v assoc[3+4] ]]
printf 'test=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "decl=x\nassign=y\nprintf-status=0 value=z\ntest=0\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want %q", stderr, "")
	}
}

func TestSpecialUnderscoreTracksExecutedSimpleCommands(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
name=world
echo "hi $name"
echo "after-echo [$_]"
s=bar
echo "after-assign [$_]"
declare s=bar
echo "after-declare [$_]"
declare a=(1 2)
echo "after-declare-array [$_]"
(( x = 1 + 2 ))
echo "after-arith [$_]"
[[ x -eq 3 ]]
echo "after-test [$_]"
echo hi && echo "after-and [$_]"
echo hi || echo "unreachable"
echo "after-or [$_]"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "hi world\nafter-echo [hi world]\nafter-assign []\nafter-declare [s=bar]\nafter-declare-array [a]\nafter-arith [after-declare-array [a]]\nafter-test [after-arith [after-declare-array [a]]]\nhi\nafter-and [hi]\nhi\nafter-or [hi]\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSpecialUnderscoreUsesExpandedLastField(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
x='with spaces'
set -- $x
echo "split [$_]"
echo one
for i in 1 2; do
  echo "loop [$_]"
done
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "split [spaces]\none\nloop [one]\nloop [loop [one]]\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNumericDbracketStopsAfterLeftOperandError(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
unset x
[[ 08 -eq x=1 ]]
printf 'status=%d x=%s\n' "$?" "${x-unset}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=1 x=unset\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "08: value too great for base (error token is \"08\")\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestIndexedAssignQuotedSubscriptIsFatal(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
a['2']=3
printf 'unreachable\n'
`)
	if err == nil {
		t.Fatal("Run error = nil, want fatal assignment failure")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr == "" {
		t.Fatal("stderr = empty, want arithmetic diagnostic")
	}
}

func TestLetStripsQuotedWords(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
let x=1
let y=x+2
let z=y*3
let z2='y*3'
echo $x $y $z $z2
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "1 3 9 9\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestLetAcceptsSpacedGrouping(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
let x=( 1 )
let y=( x + 2 )
let z=( y * 3 )
echo $x $y $z
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "1 3 9\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestCommandStringArithmeticErrorsUseShellNamePrefix(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:           "/tmp",
		Params:        []string{"\r42\r"},
		CommandString: true,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader("echo $(( $1 + 1 ))\n"), "dummy0", "", nil)
	if err == nil {
		t.Fatal("Run error = nil, want arithmetic failure")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	const wantStderr = "dummy0: \r42\r + 1 : syntax error: operand expected (error token is \"\r42\r + 1 \")\n"
	if got := stderr.String(); got != wantStderr {
		t.Fatalf("stderr = %q, want %q", got, wantStderr)
	}
}

func TestCommandStringDivisionByZeroFatality(t *testing.T) {
	t.Parallel()

	t.Run("command arg aborts shell", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpCommandString(t, "echo foo$(( 42 / 0 ))\necho inside=$?\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "inside=1\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		const wantStderr = "42 / 0 : division by 0 (error token is \" \")\n"
		if stderr != wantStderr {
			t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
		}
	})

	t.Run("if condition aborts before else", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpCommandString(t, "if test foo$(( 42 / 0 )) = foo; then\n  echo true\nelse\n  echo false\nfi\necho inside=$?\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "inside=1\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		const wantStderr = "42 / 0 : division by 0 (error token is \" \")\n"
		if stderr != wantStderr {
			t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
		}
	})

	t.Run("case word aborts before body", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpCommandString(t, "case $(( 42 / 0 )) in\n  (*) echo hi ;;\nesac\necho inside=$?\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "inside=1\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		const wantStderr = "42 / 0 : division by 0 (error token is \" \")\n"
		if stderr != wantStderr {
			t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
		}
	})

	t.Run("case pattern aborts before body", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpCommandString(t, "case foo in\n  ($(( 42 / 0 ))) echo hi ;;\nesac\necho inside=$?\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "inside=1\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		const wantStderr = "42 / 0 : division by 0 (error token is \" \")\n"
		if stderr != wantStderr {
			t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
		}
	})

	t.Run("redirect word stays non-fatal", func(t *testing.T) {
		t.Parallel()

		stdout, stderr, err := runInterpCommandString(t, "echo hi > file$(( 42 / 0 )) in\necho inside=$?\n")
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if got, want := stdout, "inside=1\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		const wantStderr = "42 / 0 : division by 0 (error token is \" \")\n"
		if stderr != wantStderr {
			t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
		}
	})
}

func TestIndexedAssignNestedSideEffects(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
echo assign=$(( z[0] = 42 ))

a[a[0]=1]=X
declare -p a

a[ a[2]=3 ]=Y
declare -p a

echo ---

a[ a[0]+=1 ]+=X
declare -p a
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "assign=42\ndeclare -a a=([0]=\"1\" [1]=\"X\")\ndeclare -a a=([0]=\"1\" [1]=\"X\" [2]=\"3\" [3]=\"Y\")\n---\ndeclare -a a=([0]=\"2\" [1]=\"X\" [2]=\"3X\" [3]=\"Y\")\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestIndexedAssignInvalidSubscriptDefersFailureToRuntime(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
a=x b[0+]=y c=z
echo "$a" "${b-unset}" "${c-unset}"
`)
	if status, ok := err.(ExitStatus); !ok || status != 1 {
		t.Fatalf("Run error = %v, want exit status 1", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "0+: arithmetic syntax error: operand expected (error token is \"+\")\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestArrayAssignmentTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/"),
		Dir:         "/tmp",
	}, `
a=(0 1 2)
b=(3 4 5)

HOME=/home/spec-test
a[0 + 1]=  b[2 + 0]=~/src

typeset -p a b
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "declare -a a=([0]=\"0\" [1]=\"\" [2]=\"2\")\ndeclare -a b=([0]=\"3\" [1]=\"4\" [2]=\"/home/spec-test/src\")\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestScalarAssignmentTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/spec-test"),
		Dir:         "/tmp",
	}, `
foo=~
echo "$foo"
foo='~'
echo "$foo"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "/home/spec-test\n~\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestReadonlyAssignmentTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/bob"),
		Dir:         "/tmp",
	}, `
readonly const=~/src
echo "$const"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "/home/bob/src\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAssignmentKeywordTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/bar"),
		Dir:         "/tmp",
	}, `
f() {
  local x=foo:~
  echo "$x"
}
f
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "foo:/home/bar\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestTempAssignmentTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/bar"),
		Dir:         "/tmp",
	}, `
show() {
  echo "$xx"
}
xx=~ show
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "/home/bar\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAssignmentLikeArgTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/bob"),
		Dir:         "/tmp",
	}, `
HOME=/home/bob
echo x=~
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "x=/home/bob\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAssignmentParamDefaultTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/bar"),
		Dir:         "/tmp",
	}, `
HOME=/home/bar
x=~:${undef-~:~}
echo "$x"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "/home/bar:/home/bar:/home/bar\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNamedUserTildeUsesSandboxMapping(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Env: expand.ListEnviron(
			"HOME=/home/agent",
			"HOME root=/sandbox/root",
		),
		Dir: "/tmp",
	}, `
echo ~root
echo ~nonexistent
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "/sandbox/root\n~nonexistent\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNamedUserTildeUsesDeterministicRootFallback(t *testing.T) {
	t.Parallel()

	rootHome, ok := defaultNamedUserHome("root")
	if !ok {
		t.Fatal("defaultNamedUserHome(root) returned no fallback")
	}
	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Env: expand.ListEnviron("HOME=/home/agent"),
		Dir: "/tmp",
	}, `
echo ~root
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != rootHome+"\n" {
		t.Fatalf("stdout = %q, want %q", stdout, rootHome+"\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDbracketTildeUsesLiveHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/bob"),
		Dir:         "/tmp",
	}, `
[[ ~ == /home/bob ]]
echo status=$?

HOME=''
[[ ~ ]]
echo status=$?
[[ -n ~ ]]
echo unary=$?

[[ ~ == ~ ]]
echo status=$?

[[ $HOME == ~ ]]
echo fnmatch=$?
[[ ~ == $HOME ]]
echo fnmatch=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	wantStdout := "status=0\nstatus=1\nunary=1\nstatus=0\nfnmatch=0\nfnmatch=0\n"
	if runtime.GOOS == "darwin" {
		wantStdout = "status=1\nstatus=0\nunary=0\nstatus=0\nfnmatch=1\nfnmatch=1\n"
	}
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDbracketRegexTildeMatchesHostBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		StartupHome: "/startup",
		Env:         expand.ListEnviron("HOME=/home/bob"),
		Dir:         "/tmp",
	}, `
HOME=foo
[[ ~ =~ $HOME ]]
echo regex=$?
[[ $HOME =~ ~ ]]
echo regex=$?

HOME='^a$'
[[ ~ =~ $HOME ]]
echo regex=$?
[[ $HOME =~ ~ ]]
echo regex=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	wantStdout := "regex=0\nregex=0\nregex=1\nregex=0\n"
	if runtime.GOOS == "darwin" {
		wantStdout = "regex=1\nregex=1\nregex=1\nregex=1\n"
	}
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
