package interp

import (
	"strings"
	"testing"
)

func TestConditionalRegexRuntimeErrors(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
[[ 'a b' =~ ^)a\ b($ ]]
printf 'literal=%d\n' "$?"
printf 'literal_rematch=%d\n' "${#BASH_REMATCH[@]}"
brace='{'
[[ x =~ $brace ]]
printf 'brace=%d\n' "$?"
printf 'brace_rematch=%d\n' "${#BASH_REMATCH[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "literal=2\nliteral_rematch=0\nbrace=2\nbrace_rematch=0\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}

	if got := strings.Count(stderr, "syntax error in conditional expression:"); got != 2 {
		t.Fatalf("stderr = %q, want 2 regex diagnostics", stderr)
	}
	if !strings.Contains(stderr, "error parsing regexp:") {
		t.Fatalf("stderr = %q, want regexp compile error", stderr)
	}
	if !strings.Contains(stderr, "invalid regular expression") {
		t.Fatalf("stderr = %q, want bare-brace regex diagnostic", stderr)
	}
}
