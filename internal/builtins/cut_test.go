package builtins_test

import (
	"context"
	"testing"
)

func TestCutSupportsLongOnlyDelimitedFlagIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'left:right\\nplain\\n' > /tmp/in.txt\ncut --only-delimited -d: -f2 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "right\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCutSupportsByteSelectionOnBinaryInput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.bin", []byte{0xc3, '|', 'x'})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "cut -b1 /tmp/in.bin | od -An -tx1 -v\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " c3 0a\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCutSupportsOutputDelimiterAndComplement(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'abcdefg\\n' > /tmp/in.txt\n" +
			"cut -c4-,2-3 --output-d=: /tmp/in.txt\n" +
			"cut --complement -b2-4 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "bc:defg\naefg\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCutSupportsNewlineDelimiterAndZeroTerminatedRecords(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\nb\\n' > /tmp/text.txt\n" +
			"cut -d'\n' -f1- --output-d=: /tmp/text.txt\n" +
			"printf 'a:1\\0b:2' > /tmp/zero.bin\n" +
			"cut -z -d: -f1 /tmp/zero.bin\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a:b\na\x00b\x00"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
