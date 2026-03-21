package builtins_test

import (
	"runtime"
	"strings"
	"testing"
)

func TestGzipQuietSuppressesMissingInputDiagnostics(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, "missing=\ngzip -cdfq -- \"$missing\"\n")
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if runtime.GOOS == "darwin" {
		if got := result.Stderr; got != "" {
			t.Fatalf("Stderr = %q, want empty", got)
		}
		return
	}
	if got := result.Stderr; !strings.Contains(got, "gzip:") {
		t.Fatalf("Stderr = %q, want gzip diagnostic", got)
	}
}

func TestGzipDecompressEmptyOperandFallsBackToSuffixLookup(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, "missing=\ngzip -cdf -- \"$missing\"\n")
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got := result.Stderr; !strings.Contains(got, ".gz") {
		t.Fatalf("Stderr = %q, want .gz lookup diagnostic", got)
	}
}
