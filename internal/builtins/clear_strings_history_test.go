package builtins_test

import (
	"context"
	"testing"
)

func TestClearOutputsANSISequence(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "clear\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\x1b[2J\x1b[H"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestStringsSupportsLengthsOffsetsAndStdin(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'ab\\000hello\\000world!\\nxyz\\001' > /tmp/data.bin\n" +
			"strings /tmp/data.bin\n" +
			"strings -n3 /tmp/data.bin\n" +
			"strings -td /tmp/data.bin\n" +
			"printf 'ab\\000hello\\000' | strings -\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello\nworld!\nhello\nworld!\nxyz\n      3 hello\n      9 world!\nhello\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHistoryReadsEnvAndClearsWithinOneExecution(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Env: map[string]string{
			"BASH_HISTORY": "[\"echo one\",\"pwd\"]",
		},
		Script: "history\nhistory 1\nhistory -c\nhistory\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "    1  echo one\n    2  pwd\n    2  pwd\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
