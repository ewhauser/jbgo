package interp

import (
	"errors"
	"testing"
)

func TestBraceExpansionBadSubstitutionSkipsCommand(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
echo before
echo -{z..A}-
`)
	var status ExitStatus
	if !errors.As(err, &status) || status != 1 {
		t.Fatalf("status err = %v, want exit status 1", err)
	}
	if stdout != "before\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "before\n")
	}
	const wantStderr = "bad substitution: no closing \"`\" in `-\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
