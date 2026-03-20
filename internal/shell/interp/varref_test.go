package interp

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
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

func TestPrintfVarRef(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
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

func TestArrayAssignmentTildeUsesCurrentShellHome(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Env: expand.ListEnviron("HOME=/"),
		Dir: "/tmp",
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
