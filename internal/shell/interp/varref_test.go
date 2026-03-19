package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runInterpScript(t *testing.T, src string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
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
