package interp

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strings"
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

func TestDeclInvalidOptionMatchesBashUsage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		script     string
		wantStderr string
	}{
		{
			name:   "declare",
			script: "declare -@\n",
			wantStderr: "" +
				"declare: -@: invalid option\n" +
				"declare: usage: declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]\n",
		},
		{
			name:   "typeset",
			script: "typeset -@\n",
			wantStderr: "" +
				"typeset: -@: invalid option\n" +
				"typeset: usage: typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runInterpScript(t, tc.script)
			if err == nil {
				t.Fatal("Run error = nil, want exit status 2")
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if stderr != tc.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr, tc.wantStderr)
			}
		})
	}
}

func TestExportInvalidAssignmentLikeIdentifierMatchesBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "export FOO-BAR=foo\n")
	var status ExitStatus
	if !errors.As(err, &status) {
		t.Fatalf("Run error = %v, want exit status", err)
	}
	if status != 1 {
		t.Fatalf("exit status = %d, want 1", status)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if got, want := stderr, "export: `FOO-BAR=foo': not a valid identifier\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestLocalInvalidAssignmentLikeIdentifierMatchesBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "f() {\n  local FOO-BAR=foo\n}\nf\n")
	var status ExitStatus
	if !errors.As(err, &status) {
		t.Fatalf("Run error = %v, want exit status", err)
	}
	if status != 1 {
		t.Fatalf("exit status = %d, want 1", status)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if got, want := stderr, "local: `FOO-BAR=foo': not a valid identifier\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestWrappedDeclarationBuiltins(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
f() {
  builtin local local_var=wrapped
  'builtin' local quoted_local=quoted
  \command readonly escaped_ro=locked
  command local via_command=seven
  spaced='one two'
  builtin -- local dashed_local="$spaced"
  command -- local dashed_command="$spaced"
  printf 'locals=%s|%s|%s|%s|<%s>|<%s>\n' "$local_var" "$quoted_local" "$escaped_ro" "$via_command" "$dashed_local" "$dashed_command"
}
f

builtin export export_var=one
'builtin' export quoted_export=two
b=builtin
c=command
x='a b'
$b $c export dyn_export=three
$b $c declare dyn_assign=$x
command builtin readonly ro_var=four
printf 'exports=%s|%s|%s|%s|<%s>\n' "$export_var" "$quoted_export" "$dyn_export" "$ro_var" "$dyn_assign"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "" +
		"locals=wrapped|quoted|locked|seven|<one two>|<one two>\n" +
		"exports=one|two|three|four|<a>\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestBuiltinDoubleDash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
builtin
printf 'plain=%d\n' "$?"
builtin --
printf 'dashdash=%d\n' "$?"
builtin -- false
printf 'false=%d\n' "$?"
builtin missing
printf 'missing=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "plain=0\ndashdash=0\nfalse=1\nmissing=1\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "builtin: missing: not a shell builtin\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "builtin: missing: not a shell builtin\n")
	}
}

func TestResolveCallExprArgsStopsAfterExpansionError(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
${missing?boom} "$(printf side-effect >&2)"
`)
	var status ExitStatus
	if !errors.As(err, &status) {
		t.Fatalf("Run error = %v, want exit status, stdout=%q stderr=%q", err, stdout, stderr)
	}
	if status != 127 {
		t.Fatalf("exit status = %d, want 127", status)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if strings.Contains(stderr, "side-effect") {
		t.Fatalf("stderr = %q, want no later-expansion side effects", stderr)
	}
	if !strings.Contains(stderr, "boom") {
		t.Fatalf("stderr = %q, want unset-parameter diagnostic", stderr)
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

func TestExportPUsesEffectiveShadowedBinding(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
foo=global
f() {
  local foo=local
  export foo
  export -p
}
f
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if !strings.Contains(stdout, "declare -x foo=\"local\"\n") {
		t.Fatalf("stdout = %q, want local exported foo entry", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclAssignmentsUsePreBuiltinExpansionSnapshot(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
foo=old
export foo=new v=$foo
printf 'export=%s,%s\n' "$foo" "$v"

unset foo v
declare foo=new v=$foo
printf 'declare=%s,%s\n' "$foo" "$v"

readonly ro=new rv=$ro
printf 'readonly=%s,%s\n' "$ro" "$rv"

f() {
  local local_var=new seen=$local_var
  printf 'local=%s,%s\n' "$local_var" "$seen"
}
f
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"export=new,old\n" +
		"declare=new,\n" +
		"readonly=new,\n" +
		"local=new,\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestExportMinusNClearsExportAndPreservesAssignedValue(t *testing.T) {
	t.Parallel()

	runner, stdout, stderr, err := runInterpScriptWithRunner(t, `
foo=old
export -n foo=new
printf 'value=%s\n' "$foo"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "value=new\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	vr := runner.lookupVar("foo")
	if !vr.IsSet() || vr.Str != "new" || vr.Exported {
		t.Fatalf("foo = %#v, want set string value and exported=false", vr)
	}
}

func TestExportTargetsCallerLocalBinding(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
report_export() {
  found=no
  while IFS= read -r line; do
    case $line in
      'declare -x outer_var='*) found=yes ;;
    esac
  done
  echo "$found"
}
inner() {
  export outer_var
  printf 'inner=%s\n' "$(export -p | report_export)"
}
outer() {
  local outer_var=X
  printf 'before=%s\n' "$(export -p | report_export)"
  inner
  printf 'after=%s\n' "$(export -p | report_export)"
}
outer
printf 'global=%s\n' "$(export -p | report_export)"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "before=no\ninner=yes\nafter=yes\nglobal=no\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestLocalReadonlyErrorIncludesBuiltinName(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
readonly y=1
f() {
  local y=2
  echo status=$?
}
f
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "status=1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "local: y: readonly variable\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
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

func TestDeclareConversionErrorStillProcessesLaterOperands(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -A assoc=([k]=v)
declare -a assoc arr=ok
echo status=$?
echo arr=$arr
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=1\narr=ok\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "declare: assoc: cannot convert associative to indexed array\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestDeclareDynamicFlagOnlyOperandStillPrintsListings(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -a arr=(1 2)
flag=-a
declare "$flag"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "declare -a arr=([0]=\"1\" [1]=\"2\")\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclareAllowsLocalArrayKindShadowing(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -a shared=(1 2)
declare -A other=([g]=x)

f() {
  declare -A shared=([k]=v)
  local -a other=(3 4)
  declare -p shared
  declare -p other
}

f
declare -p shared
declare -p other
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"declare -A shared=([k]=\"v\" )\n" +
		"declare -a other=([0]=\"3\" [1]=\"4\")\n" +
		"declare -a shared=([0]=\"1\" [1]=\"2\")\n" +
		"declare -A other=([g]=\"x\" )\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
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

func TestDeclareListAndQueryModesMatchBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
filter_decl() {
  while IFS= read -r line; do
    case $line in
      test_var*) echo "$line" ;;
    esac
  done
}
filter_query() {
  while IFS= read -r line; do
    case $line in
      *test_var*) echo "$line" ;;
    esac
  done
}
test_var1=111
readonly test_var2=222
export test_var3=333
declare -n test_var4=test_var1
declare -a test_var6=()
declare -A test_var7=()
f() {
  local test_var5=555
  {
    echo '[declare]'
    declare | filter_decl
    echo '[declare-p]'
    declare -p | filter_query
    echo '[readonly]'
    readonly | filter_query
    echo '[export]'
    export | filter_query
    echo '[local]'
    local | filter_query
    echo '[pn]'
    declare -pn | filter_query
    echo '[pr]'
    declare -pr | filter_query
    echo '[px]'
    declare -px | filter_query
    echo '[pa]'
    declare -pa | filter_query
    echo '[pA]'
    declare -pA | filter_query
  }
}
f
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"[declare]\n" +
		"test_var1=111\n" +
		"test_var2=222\n" +
		"test_var3=333\n" +
		"test_var4=test_var1\n" +
		"test_var5=555\n" +
		"[declare-p]\n" +
		"declare -- test_var1=\"111\"\n" +
		"declare -r test_var2=\"222\"\n" +
		"declare -x test_var3=\"333\"\n" +
		"declare -n test_var4=\"test_var1\"\n" +
		"declare -- test_var5=\"555\"\n" +
		"declare -a test_var6=()\n" +
		"declare -A test_var7=()\n" +
		"[readonly]\n" +
		"declare -r test_var2=\"222\"\n" +
		"[export]\n" +
		"declare -x test_var3=\"333\"\n" +
		"[local]\n" +
		"declare -- test_var5=\"555\"\n" +
		"[pn]\n" +
		"declare -n test_var4=\"test_var1\"\n" +
		"[pr]\n" +
		"declare -r test_var2=\"222\"\n" +
		"[px]\n" +
		"declare -x test_var3=\"333\"\n" +
		"[pa]\n" +
		"declare -a test_var6=()\n" +
		"[pA]\n" +
		"declare -A test_var7=()\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclareAttributeFiltersIncludeIntegerLowerUpper(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
filter_query() {
  while IFS= read -r line; do
    case $line in
      *int_var*|*lower_var*|*upper_var*) echo "$line" ;;
    esac
  done
}
declare -i int_var=42
declare -l lower_var=UPPER
declare -u upper_var=lower
echo '[pi]'
declare -pi | filter_query
echo '[pl]'
declare -pl | filter_query
echo '[pu]'
declare -pu | filter_query
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"[pi]\n" +
		"declare -i int_var=\"42\"\n" +
		"[pl]\n" +
		"declare -l lower_var=\"upper\"\n" +
		"[pu]\n" +
		"declare -u upper_var=\"LOWER\"\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestReadonlyArrayDeclarationsHideTypeUntilExplicitArrayDecl(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
readonly -a arr
readonly -A dict
declare -p arr dict
declare -pa arr
declare -pA dict

declare -a arr2
readonly arr2
declare -A dict2
readonly dict2
declare -p arr2 dict2
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"declare -r arr\n" +
		"declare -r dict\n" +
		"declare -r arr\n" +
		"declare -r dict\n" +
		"declare -ar arr2\n" +
		"declare -Ar dict2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclareUnsetStateAndEvalRoundTrip(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare x
declare -p x
eval -- "$(arr=(); arr[3]= arr[4]=foo; declare -p arr)"
for i in {0..4}; do
  echo "arr[$i]: ${arr[$i]+set ... [}${arr[$i]-unset}${arr[$i]+]}"
done
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"declare -- x\n" +
		"arr[0]: unset\n" +
		"arr[1]: unset\n" +
		"arr[2]: unset\n" +
		"arr[3]: set ... []\n" +
		"arr[4]: set ... [foo]\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclarationBuiltinsViaBuiltinAndDynamicDispatch(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
x='a b'
builtin declare c=$x
echo "c=$c"

v=x
f() {
  \builtin local v=1
  echo "l:v=$v"
}
f
echo "g:v=$v"

a=typeset
"$a" v=1
echo "v=$v"

cmd=(typeset v2=1)
"${cmd[@]}"
echo "v2=$v2"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"c=a\n" +
		"l:v=1\n" +
		"g:v=x\n" +
		"v=1\n" +
		"v2=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDeclarePlusRDoesNotClearReadonly(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
readonly x=1
declare +r x
printf 'declare=%d x=%s\n' "$?" "$x"
x=2
printf 'assign=%d x=%s\n' "$?" "$x"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "declare=1 x=1\nassign=1 x=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "declare: x: readonly variable\nx: readonly variable\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestTypesetPlusRDoesNotClearReadonly(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
readonly x=1
typeset +r x
printf 'typeset=%d x=%s\n' "$?" "$x"
x=2
printf 'assign=%d x=%s\n' "$?" "$x"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "typeset=1 x=1\nassign=1 x=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "typeset: x: readonly variable\nx: readonly variable\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestDeclareFListsFunctionNames(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
add() { :; }
ble/foo() { :; }
declare -F
echo ---
declare -F add
declare -F ble/foo
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"declare -f add\n" +
		"declare -f ble/foo\n" +
		"---\n" +
		"add\n" +
		"ble/foo\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
