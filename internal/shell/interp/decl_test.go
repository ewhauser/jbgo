package interp

import (
	"bytes"
	"context"
	"io/fs"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func TestDeclOperands(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -a 'var=(1 2 3)'
printf 'array=%s\n' "${var[*]}"
typeset -A assoc=([k]=v)
key=k
declare -n ref='assoc[$key]'
ref=x
printf 'assoc=%s\n' "${assoc[k]}"
words='foo=1 bar=2'
declare $words
printf 'split=%s,%s\n' "$foo" "$bar"
prefix=side declare kept=ok
printf 'prefix=%s kept=%s\n' "${prefix-unset}" "$kept"
shopt -s expand_aliases
alias e=export
e alias_var=works
printf 'alias=%s\n' "$alias_var"
spec='literal=$HOME'
declare "$spec"
printf 'literal=%s\n' "$literal"
spec='spaced=1 2'
declare "$spec"
printf 'spaced=%s\n' "$spaced"
spec='quoted="1 2"'
declare "$spec"
printf 'quoted=%s\n' "$quoted"
spec='cmd=$(printf hacked)'
declare "$spec"
printf 'cmd=%s\n' "$cmd"
seed=home
spec='arr=($((1 + 2)) $(printf hacked) a$(printf bc)d "$seed"x)'
declare -a "$spec"
printf 'arr=%s|%s|%s|%s len=%s\n' "${arr[0]}" "${arr[1]}" "${arr[2]}" "${arr[3]}" "${#arr[@]}"
empty=''
declare "$empty"
printf 'empty=%s\n' "$?"
compound='compound_scalar=(1 2)'
declare "$compound"
printf 'scalar=%s\n' "$compound_scalar"
compound='compound_array=(1 2)'
declare -a "$compound"
printf 'flagged=%s\n' "${compound_array[*]}"
declare +a 'string_arr=(2 3)'
declare +A 'string_assoc=([k]=v)'
printf 'plus=%s|%s\n' "$string_arr" "$string_assoc"
declare -a direct_scalar=4
spec='dyn_scalar=5'
declare -a "$spec"
printf 'scalar-array=%s|%s|%s|%s\n' "${#direct_scalar[@]}" "${direct_scalar[0]}" "${#dyn_scalar[@]}" "${dyn_scalar[0]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "array=1 2 3\nassoc=x\nsplit=1,2\nprefix=unset kept=ok\nalias=works\nliteral=$HOME\nspaced=1 2\nquoted=\"1 2\"\ncmd=$(printf hacked)\narr=3|hacked|abcd|homex len=4\nempty=1\nscalar=(1 2)\nflagged=1 2\nplus=(2 3)|([k]=v)\nscalar-array=1|4|1|5\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "declare: `': not a valid identifier\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "declare: `': not a valid identifier\n")
	}
}

func TestDeclDynamicQuotedParamQRoundTrips(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
var=foo
val='"quoted" with spaces and \'
declare $var="${val@Q}"
printf '<%s>\n' "$foo"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if got, want := stdout, "<'\"quoted\" with spaces and \\'>\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclPrefixAssignValidationFailureSkipsBuiltin(t *testing.T) {
	t.Parallel()

	litWord := func(value string) *syntax.Word {
		return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: value}}}
	}

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}
	runner.Reset()

	call := &syntax.CallExpr{
		Assigns: []*syntax.Assign{{
			Ref: &syntax.VarRef{Name: &syntax.Lit{Value: "bad"}},
			Array: &syntax.ArrayExpr{
				Mode: syntax.ArrayExprAssociative,
				Elems: []*syntax.ArrayElem{
					{
						Kind:  syntax.ArrayElemKeyed,
						Index: literalSubscript(syntax.SubscriptExpr, syntax.SubscriptAssociative, "k"),
						Value: litWord("1"),
					},
					{
						Kind:  syntax.ArrayElemSequential,
						Value: litWord("2"),
					},
				},
			},
		}},
		Args: []*syntax.Word{
			litWord("declare"),
			litWord("kept=ok"),
		},
	}

	runner.cmd(context.Background(), call)

	if runner.exit.code != 1 {
		t.Fatalf("exit code = %d, want 1", runner.exit.code)
	}
	if got := runner.lookupVar("kept"); got.IsSet() {
		t.Fatalf("declare executed, kept = %#v", got)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	const wantStderr = "bad: 2: must use subscript when assigning associative array\n"
	if stderr.String() != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr.String(), wantStderr)
	}
}

func TestDeclareArrayIndexedSideEffects(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		ReadDirHandler: func(context.Context, string) ([]fs.DirEntry, error) {
			return nil, nil
		},
	}, `
declare a[a[0]=1]=X
declare -p a

declare a[ a[2]=3 ]=Y
declare -p a
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "declare -a a=([0]=\"1\" [1]=\"X\")\ndeclare -a a=([0]=\"1\" [1]=\"X\" [2]=\"3\")\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "declare: `a[': not a valid identifier\ndeclare: `]=Y': not a valid identifier\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestDeclarePrintsKindSpecificListings(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -a arr=(1 2)
declare -A assoc=([k]=v)
declare -A
declare -a
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "declare -A assoc=([k]=\"v\" )\ndeclare -a arr=([0]=\"1\" [1]=\"2\")\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclareRejectsIndexedAssociativeConversion(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -a a=(1 2 3 4)
eval 'declare -A a=([a]=x [b]=y [c]=z)'
echo status=$?
printf 'a=%s,%s,%s,%s\n' "${a[0]}" "${a[1]}" "${a[2]}" "${a[3]}"

declare -A A=([a]=x [b]=y [c]=z)
eval 'declare -a A=(1 2 3 4)'
echo status=$?
printf 'A=%s,%s,%s\n' "${A[a]}" "${A[b]}" "${A[c]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=1\na=1,2,3,4\nstatus=1\nA=x,y,z\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "" +
		"eval: a: cannot convert indexed to associative array\n" +
		"eval: A: cannot convert associative to indexed array\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestDeclareAssociativeAppendPreservesStringAtZeroKey(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
s1=hello
s2=world

eval 'declare -A s1=([a]=x [b]=y)'
echo status1=$?
printf 's1=%s,%s,%s\n' "${s1[0]-missing}" "${s1[a]}" "${s1[b]}"

eval 'declare -A s2+=([a]=x [b]=y)'
echo status2=$?
printf 's2=%s,%s,%s\n' "${s2[0]-missing}" "${s2[a]}" "${s2[b]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status1=0\ns1=missing,x,y\nstatus2=0\ns2=world,x,y\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
