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
printf '%s\n' "$foo"
printf '%s\n' "${a[@]}"
printf -v 'a[' %s 'foo'
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "hello there\na\nfoo\nc\nstatus=2\n"
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
