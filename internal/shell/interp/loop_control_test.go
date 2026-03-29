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

func TestBreakInConditionClampedToLoopDepth(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
while break 2; do :; done
while :; do echo second; break; done
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "second\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestTopLevelReturnReportsStatusTwo(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
return
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "status=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "varref-test.sh: line 2: return: can only `return' from a function or sourced script\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
