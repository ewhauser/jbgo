package interp

import "testing"

func TestDeclOperands(t *testing.T) {
	t.Parallel()

	stdout, _, err := runInterpScript(t, `
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
declare +a 'string_arr=(2 3)'
declare +A 'string_assoc=([k]=v)'
printf 'plus=%s|%s\n' "$string_arr" "$string_assoc"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "array=1 2 3\nassoc=x\nsplit=1,2\nprefix=unset kept=ok\nalias=works\nplus=(2 3)|([k]=v)\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}
