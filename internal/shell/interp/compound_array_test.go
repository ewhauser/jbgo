package interp

import (
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
)

func TestCompoundArrayAssignments(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
hello=100
a=([hello]=1 [hello]+=2)
printf 'idx=%s\n' "${a[100]}"
a+=([hello]+=:34 [hello]+=:56)
printf 'idx-append=%s\n' "${a[100]}"

declare -A assoc
assoc=([hello]=1 [hello]+=2)
printf 'assoc=%s\n' "${assoc[hello]}"
assoc+=([hello]+=:34 [hello]+=:56)
printf 'assoc-append=%s\n' "${assoc[hello]}"

a=([100]=1 2 3 4 [5]=a b c d)
printf 'cursor=%s,%s,%s,%s,%s,%s,%s,%s\n' \
  "${a[5]}" "${a[6]}" "${a[7]}" "${a[8]}" \
  "${a[100]}" "${a[101]}" "${a[102]}" "${a[103]}"

declare -A pairs=(1 2 3)
printf 'pairs=%s,%s\n' "${pairs[1]}" "${pairs[3]}"

i=1
a=([100+i++]=$((i++)) [200+i++]=$((i++)) [300+i++]=$((i++)))
printf 'eval1=%s,%s,%s,%s\n' "${a[104]}" "${a[205]}" "${a[306]}" "$i"

a=(old1 old2 old3)
old1=101 old2=102 old3=103
new1=201 new2=202 new3=203
a+=([0]=new1 [1]=new2 [2]=new3 [5]="${a[2]}" [a[0]]="${a[0]}" [a[1]]="${a[1]}")
printf 'eval3=%s,%s,%s,%s,%s,%s\n' "${a[0]}" "${a[1]}" "${a[2]}" "${a[5]}" "${a[201]}" "${a[202]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "" +
		"idx=12\n" +
		"idx-append=12:34:56\n" +
		"assoc=2\n" +
		"assoc-append=2:34:56\n" +
		"cursor=a,b,c,d,1,2,3,4\n" +
		"pairs=2,\n" +
		"eval1=1,2,3,7\n" +
		"eval3=new1,new2,new3,old3,old1,old2\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestCompoundAssociativeReplacementAppendUsesOriginalValue(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
declare -A assoc=([hello]=old)
assoc=([hello]=1 [hello]+=2 [hello]+=3)
printf 'replace=%s\n' "${assoc[hello]}"

unset assoc
declare -A assoc
assoc=([hello]=1 [hello]+=2 [hello]+=3)
printf 'fresh=%s\n' "${assoc[hello]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "replace=old3\nfresh=3\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestCompoundArrayAssignmentsThroughNameRef(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
declare -A map=([k]=v)
declare -n ref=map
ref+=(['k']+=x ['new']=y)
printf 'kind=%s\n' "${map@a}"
printf 'vals=%s,%s\n' "${map[k]}" "${map[new]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "kind=A\nvals=vx,y\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestCompoundArrayAssignmentsPreserveAttributes(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
declare -alx replace=(old)
replace=(new)
printf 'replace=%s,%s\n' "${replace@a}" "${replace[0]}"

declare -Aiux append=([k]=v)
append+=([k]+=x ['new']=y)
printf 'append=%s,%s,%s\n' "${append@a}" "${append[k]}" "${append[new]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "" +
		"replace=alx,new\n" +
		"append=Aiux,vx,y\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestCompoundAssociativeArrayMixedFormsFail(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare -A assoc=([old]=keep)
assoc=([j]=1 2 3 4)
printf 'status=%s count=%s old=%s j=%s\n' "$?" "${#assoc[@]}" "${assoc[old]-missing}" "${assoc[j]-missing}"
printf 'after\n'
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "" +
		"status=1 count=1 old=missing j=1\n" +
		"after\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	const wantStderr = "assoc: 2: must use subscript when assigning associative array\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestCommandPrefixAssignmentFailureRestoresEarlierVars(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
TMP=orig
readonly LOCK=keep
unset NEW
declare -A assoc=([old]=keep)
TMP=1 NEW=2 LOCK=bad printf 'inner\n'
printf 'status=%s tmp=%s new=%s lock=%s count=%s old=%s\n' "$?" "$TMP" "${NEW-unset}" "$LOCK" "${#assoc[@]}" "${assoc[old]}"
printf 'after\n'
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "" +
		"status=1 tmp=orig new=unset lock=keep count=1 old=keep\n" +
		"after\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "LOCK: readonly variable\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "LOCK: readonly variable\n")
	}
}

func TestCompoundArrayKeyedValuesUseStartupHomeForAssignmentTildes(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:         "/tmp",
		StartupHome: "/startup",
		Env: expand.ListEnviron(
			"HOME=/home/user",
		),
	}, `
a=([2]=~ [4]=~:~:~)
printf '%s\n%s\n' "${a[2]}" "${a[4]}"
declare -A assoc=([home]=~ [hello]=~:~:~)
printf '%s\n%s\n' "${assoc[home]}" "${assoc[hello]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "/startup\n/startup:/startup:/startup\n/startup\n/startup:/startup:/startup\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
