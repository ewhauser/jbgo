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

func TestCStyleForContinuesAfterFalseAndListBodyStatus(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
for ((i=1; i>=0; i--)); do
  [[ $i -eq 0 ]] && echo hit
done
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "hit\nstatus=0\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
