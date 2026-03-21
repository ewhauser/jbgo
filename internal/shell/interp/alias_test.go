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

func TestAliasBuiltinHandlesDashDashAndLookupFailures(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
shopt -s expand_aliases
alias -- foo=echo
alias e='echo' missing alias_with_empty=
printf 'define=%d\n' "$?"
foo ok
alias -- e alias_with_empty
printf 'show=%d\n' "$?"
unalias -- foo
foo gone
printf 'missing=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "define=1\nok\nalias e='echo'\nalias alias_with_empty=''\nshow=0\nmissing=127\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "alias: missing: not found\n\"foo\": executable file not found in $PATH\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestUnaliasBuiltinFlagsUsageAndMissingNames(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
alias e=echo ll='ls -l' foo=bar spam=eggs
unalias e missing ll
printf 'partial=%d\n' "$?"
alias
unalias -a
printf 'clear=%d\n' "$?"
alias
alias after=echo
unalias -a ignored
printf 'clear-with-args=%d\n' "$?"
alias
unalias +a
printf 'plus=%d\n' "$?"
unalias -
printf 'dash=%d\n' "$?"
unalias
printf 'usage=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "partial=1\nalias foo='bar'\nalias spam='eggs'\nclear=0\nclear-with-args=0\nplus=1\ndash=1\nusage=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "unalias: missing: not found\nunalias: +a: not found\nunalias: -: not found\nunalias: usage: unalias [-a] name [name ...]\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
