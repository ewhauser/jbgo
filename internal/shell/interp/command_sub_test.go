package interp

import "testing"

func TestBackticksInsideDoubleQuotesStripEscapedQuotesLikeBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "echo \"x `echo \\\"hi\\\"`\"\n")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "x hi\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "x hi\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
