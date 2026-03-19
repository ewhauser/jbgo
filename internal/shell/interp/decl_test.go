package interp

import "testing"

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
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "array=1 2 3\nassoc=x\nsplit=1,2\nprefix=unset kept=ok\nalias=works\nliteral=$HOME\nspaced=1 2\nquoted=\"1 2\"\ncmd=$(printf hacked)\narr=3|hacked|abcd|homex len=4\nempty=1\nscalar=(1 2)\nflagged=1 2\nplus=(2 3)|([k]=v)\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "declare: `': not a valid identifier\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "declare: `': not a valid identifier\n")
	}
}
