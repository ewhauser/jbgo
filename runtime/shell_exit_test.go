package runtime

import "testing"

func TestExecMarksShellExit(t *testing.T) {
	t.Parallel()

	session := newSession(t, nil)
	result := mustExecSession(t, session, "exit 7")

	if got, want := result.ExitCode, 7; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if !result.ShellExited {
		t.Fatalf("ShellExited = false, want true")
	}
}
