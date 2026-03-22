package interp

import "testing"

func TestCompletionBuiltinsDispatchFromThinSwitch(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, ""+
		"complete -W\n"+
		"printf 'complete=%d\\n' \"$?\"\n"+
		"compopt -o invalid cmd\n"+
		"printf 'compopt=%d\\n' \"$?\"\n")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "complete=2\ncompopt=2\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, "complete: -W: option requires an argument\ncompopt: invalid: invalid option name\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestWaitBuiltinWithoutArgsReturnsZero(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "false &\nfalse &\nwait\nprintf 'status=%d\\n' \"$?\"\n")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "status=0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestWaitBuiltinReturnsLastExplicitChildStatus(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "false &\nfirst=$!\ntrue &\nsecond=$!\nwait \"$first\" \"$second\"\nprintf 'status=%d\\n' \"$?\"\n")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "status=0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestCallerBuiltinReportsFrameFieldsAndMissingDepth(t *testing.T) {
	t.Parallel()

	src := "outer() {\n  inner\n}\ninner() {\n  caller 0\n  printf 'status0=%d\\n' \"$?\"\n  caller 1\n  printf 'status1=%d\\n' \"$?\"\n}\nouter\n"
	stdout, stderr, err := runInterpScript(t, src)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "2 outer varref-test.sh\nstatus0=0\nstatus1=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
