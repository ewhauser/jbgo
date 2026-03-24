package builtins_test

import (
	"context"
	"testing"
)

func TestExprSupportsGNUKeywordOperatorsAndRegexCaptures(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "export LC_ALL=fr_FR.UTF-8\n" +
			"expr length αbcdef\n" +
			"expr index αbcδef δ\n" +
			"expr substr αbcδef 4 2\n" +
			"expr 'abbccd' : 'a\\(\\([bc]\\)\\2\\)*d'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "6\n4\nδe\ncc\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExprShortCircuitsBooleanOperatorsAndGNUTruthiness(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "expr 0 '&' 1 / 0\n" +
			"expr 1 '|' 1 / 0\n" +
			"expr '' '|' ''\n" +
			"expr 00\n" +
			"expr -0\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0\n1\n0\n00\n-0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestExprReportsGNUSyntaxDiagnostics(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "expr 2 +\n" +
			"expr '(' 2\n" +
			"expr '_' : 'a\\('\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	want := "expr: syntax error: missing argument after '+'\n" +
		"expr: syntax error: expecting ')' after '2'\n" +
		"expr: Unmatched ( or \\(\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}
