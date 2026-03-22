package interp

import "testing"

func TestInvalidFunctionNameWithDollarReturnsStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
foo$x() {
  echo hi
}
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "`foo$x': not a valid identifier\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestInvalidFunctionNameWithProcSubstReturnsStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
foo<(echo hi)() { :; }
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "`foo<(echo hi)': not a valid identifier\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestInvalidFunctionNameWithCommandSubReturnsStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
foo-$(echo hi)() { ls ; }
`)
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "`foo-$(echo hi)': not a valid identifier\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
