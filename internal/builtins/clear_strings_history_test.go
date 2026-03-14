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

func TestStringsHonorsEightBitEncodingMode(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'ab\\351cd\\000tail\\000' > /tmp/eight.bin\n" +
			"strings -es /tmp/eight.bin\n" +
			"strings -eS /tmp/eight.bin\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "tail\nab\351cd\ntail\n"; got != want {
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

func TestHistoryRejectsInvalidCountArguments(t *testing.T) {
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name   string
		script string
		stderr string
	}{
		{
			name: "nonnumeric",
			script: "BASH_HISTORY='[\"echo one\"]'\n" +
				"history abc\n",
			stderr: "history: abc: numeric argument required\n",
		},
		{
			name: "negative",
			script: "BASH_HISTORY='[\"echo one\"]'\n" +
				"history -- -1\n",
			stderr: "history: -1: numeric argument required\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got, want := result.ExitCode, 1; got != want {
				t.Fatalf("ExitCode = %d, want %d", got, want)
			}
			if got := result.Stdout; got != "" {
				t.Fatalf("Stdout = %q, want empty", got)
			}
			if got, want := result.Stderr, tc.stderr; got != want {
				t.Fatalf("Stderr = %q, want %q", got, want)
			}
		})
	}
}
