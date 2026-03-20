package interp

import "testing"

func TestAliasExpansionParsesSyntaxBeforeExecution(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
shopt -s expand_aliases
alias loop='for i in 1 2 3; do printf "%s\n" '
loop $i; done
alias both='echo one && echo two'
both
alias LEFT='{'
LEFT echo brace; }
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "1\n2\n3\none\ntwo\nbrace\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAliasExpansionInEvalUsesLiveAliasState(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
shopt -s expand_aliases
eval "alias sayhi='echo hello'
sayhi inside"
sayhi outside
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "hello inside\nhello outside\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmptyAliasRemovesCommandWord(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
shopt -s expand_aliases
alias a=''
a echo hello
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "hello\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAliasBuiltinListsAliasesSorted(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
alias ll='ls -l'
alias ex=exit
alias
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const want = "alias ex='exit'\nalias ll='ls -l'\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
