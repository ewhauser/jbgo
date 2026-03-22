package interp

import "testing"

func TestArithmeticProcessSubstitutionErrorIsFatalLikeBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
x='a[<(echo 42 | tee PWNED)]=1'
echo $(( x ))
echo after
`)
	if err == nil {
		t.Fatalf("Run error = nil, want exit status 1 (stdout=%q stderr=%q)", stdout, stderr)
	}
	requireInterpExitStatus(t, err, 1)
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "<(echo 42 | tee PWNED): arithmetic syntax error: operand expected (error token is \"<(echo 42 | tee PWNED)\")\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
