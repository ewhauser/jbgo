package builtins_test

import (
	"context"
	"testing"
)

func TestBashCommandStringBackquoteQuoteErrorsMatchBashRecovery(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'echo `echo \"`'\n" +
			"echo status=$?\n" +
			"bash -c 'echo `echo \\\\\\\\\"`'\n" +
			"echo status=$?\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, result.Stdout, result.Stderr)
	}
	const wantStdout = "\nstatus=0\n\nstatus=0\n"
	if got := result.Stdout; got != wantStdout {
		t.Fatalf("Stdout = %q, want %q", got, wantStdout)
	}
	const wantStderr = "unexpected EOF while looking for matching `\"'\nunexpected EOF while looking for matching `\"'\n"
	if got := result.Stderr; got != wantStderr {
		t.Fatalf("Stderr = %q, want %q", got, wantStderr)
	}
}

func TestBackticksInsideDoubleQuotesPreserveEscapedQuotesInDBrackets(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "file=/tmp/command-sub-dbracket\n" +
			": > \"$file\"\n" +
			"echo \"123 `[[ $(echo \\\\\" > \\\"$file\\\") ]]` 456\"\n" +
			"IFS= read -r line < \"$file\"\n" +
			"printf '%s\\n' \"$line\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, result.Stdout, result.Stderr)
	}
	const wantStdout = "123  456\n\"\n"
	if got := result.Stdout; got != wantStdout {
		t.Fatalf("Stdout = %q, want %q", got, wantStdout)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}
