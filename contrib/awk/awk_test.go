package awk

import (
	"strings"
	"testing"
)

func TestAWKSupportsProgramFilesFieldSeparatorsAndVars(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "printf 'BEGIN { print prefix }\\n{ print $2 }\\n' > /tmp/prog.awk\nprintf 'a,b\\nc,d\\n' > /tmp/in.csv\nawk -F, -v prefix=rows -f /tmp/prog.awk /tmp/in.csv\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "rows\nb\nd\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKDisablesExec(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "awk 'BEGIN { system(\"echo nope\") }'\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "NoExec") && !strings.Contains(result.Stderr, "can't") {
		t.Fatalf("Stderr = %q, want sandbox execution denial", result.Stderr)
	}
}
