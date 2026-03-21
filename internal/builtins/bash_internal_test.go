package builtins

import "testing"

func TestNormalizeNestedShellArithStderr(t *testing.T) {
	t.Parallel()

	script := "echo $(( 1 +\r\n2))\n"
	stderr := "line 2: not a valid arithmetic operator: `2`\nline 2: `2))'\n"

	got, ok := normalizeNestedShellArithStderr(script, stderr)
	if !ok {
		t.Fatal("normalizeNestedShellArithStderr() ok = false, want true")
	}
	const want = "1 +\r\n2: syntax error: operand expected (error token is \"\r\n2\")\n"
	if got != want {
		t.Fatalf("normalizeNestedShellArithStderr() = %q, want %q", got, want)
	}
}

func TestPrefixNestedShellDiagnosticPreservesOperandSpacing(t *testing.T) {
	t.Parallel()

	result := &ExecutionResult{
		ExitCode: 2,
		Stderr:   "dummy0: \r42\r + 1 : syntax error: operand expected (error token is \"\r42\r + 1 \")\n",
	}

	prefixNestedShellDiagnostic(result, "dummy0")

	const want = "dummy0: line 1: \r42\r + 1 : syntax error: operand expected (error token is \"\r42\r + 1 \")\n"
	if got := result.Stderr; got != want {
		t.Fatalf("result.Stderr = %q, want %q", got, want)
	}
}
