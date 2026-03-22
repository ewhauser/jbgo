package interp

import "testing"

func TestWhileBreakInConditionSkipsBody(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
while break; do
  echo x
done
echo done
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "done\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestTopLevelReturnReportsStatusOne(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
return
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "status=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "return: can only `return' from a function or sourced script\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
