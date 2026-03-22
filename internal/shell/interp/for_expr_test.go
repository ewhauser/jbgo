package interp

import "testing"

func TestCStyleForArithmeticErrorMatchesBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
for ((i = $'3'; i < 5; ++i)); do
  :
done
printf 'status=%d i=%s\n' "$?" "${i-}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=1 i=\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "((: i = '3': arithmetic syntax error: operand expected (error token is \"'3'\")\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
