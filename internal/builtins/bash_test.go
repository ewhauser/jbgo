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
	const want = "1 +\r\n2: arithmetic syntax error: operand expected (error token is \"\r\n2\")\n"
	if got != want {
		t.Fatalf("normalizeNestedShellArithStderr() = %q, want %q", got, want)
	}
}

func TestNormalizeNestedShellArithStderrPreservesOtherLines(t *testing.T) {
	t.Parallel()

	script := "echo before >&2\necho $(( 1 +\r\n2))\necho after >&2\n"
	stderr := "before\nline 2: not a valid arithmetic operator: `2`\nline 2: `2))'\nafter\n"

	got, ok := normalizeNestedShellArithStderr(script, stderr)
	if !ok {
		t.Fatal("normalizeNestedShellArithStderr() ok = false, want true")
	}
	const want = "before\n1 +\r\n2: arithmetic syntax error: operand expected (error token is \"\r\n2\")\nafter\n"
	if got != want {
		t.Fatalf("normalizeNestedShellArithStderr() = %q, want %q", got, want)
	}
}

func TestNormalizeNestedShellArithStderrDropsBareContinuationLine(t *testing.T) {
	t.Parallel()

	script := "echo $(( 1 +\r\n2))\n"
	stderr := "line 1: not a valid arithmetic operator: `2`\n`2))'\n"

	got, ok := normalizeNestedShellArithStderr(script, stderr)
	if !ok {
		t.Fatal("normalizeNestedShellArithStderr() ok = false, want true")
	}
	const want = "1 +\r\n2: arithmetic syntax error: operand expected (error token is \"\r\n2\")\n"
	if got != want {
		t.Fatalf("normalizeNestedShellArithStderr() = %q, want %q", got, want)
	}
}

func TestNormalizeNestedShellArithStderrMatchesFailingExpression(t *testing.T) {
	t.Parallel()

	script := "f() { : $(( 1 +\r\n2)); }\n: $(( 3 +\r\n4))\n"
	stderr := "line 4: not a valid arithmetic operator: `4`\nline 4: `4))'\n"

	got, ok := normalizeNestedShellArithStderr(script, stderr)
	if !ok {
		t.Fatal("normalizeNestedShellArithStderr() ok = false, want true")
	}
	const want = "3 +\r\n4: arithmetic syntax error: operand expected (error token is \"\r\n4\")\n"
	if got != want {
		t.Fatalf("normalizeNestedShellArithStderr() = %q, want %q", got, want)
	}
}

func TestPrefixNestedShellDiagnosticPreservesOperandSpacing(t *testing.T) {
	t.Parallel()

	result := &ExecutionResult{
		ExitCode: 2,
		Stderr:   "dummy0: \r42\r + 1 : arithmetic syntax error: operand expected (error token is \"\r42\r + 1 \")\n",
	}

	prefixNestedShellDiagnostic(result, "dummy0")

	const want = "dummy0: line 1: \r42\r + 1 : arithmetic syntax error: operand expected (error token is \"\r42\r + 1 \")\n"
	if got := result.Stderr; got != want {
		t.Fatalf("result.Stderr = %q, want %q", got, want)
	}
}

func TestPrefixNestedShellDiagnosticLeavesPrefixedUserStderrUntouched(t *testing.T) {
	t.Parallel()

	result := &ExecutionResult{
		ExitCode: 1,
		Stderr:   "bash: unbound variable\n",
	}

	prefixNestedShellDiagnostic(result, "bash")

	const want = "bash: unbound variable\n"
	if got := result.Stderr; got != want {
		t.Fatalf("result.Stderr = %q, want %q", got, want)
	}
}
