package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestSeqBasicCountingAndWidth(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "seq 5\nseq -w 5 10\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n2\n3\n4\n5\n05\n06\n07\n08\n09\n10\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSeqSupportsSeparatorsFormatsAndHexInput(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "seq -s , -t ! 2 6\nseq -f '%.2f' 0.0 0.1 0.3\nseq 0x1p-1 2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2,3,4,5,6!0.00\n0.10\n0.20\n0.30\n0.5\n1.5\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSeqPreservesPrecisionAndNegativeZero(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "seq -w -0.0 1\nseq 1 1.20 3.000000\nseq 1 1.20 0x3.000000\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "-0.0\n01.0\n1.00\n2.20\n1\n2.2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSeqParsesFractionalValuesWithLeadingZeroes(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "seq 0.8 0.1 0.9\nseq 0.000000 0.000001 0.000003\nseq 0.1 -0.1 -0.2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0.8\n0.9\n0.000000\n0.000001\n0.000002\n0.000003\n0.1\n0.0\n-0.1\n-0.2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSeqErrorsOnMissingOperandZeroIncrementAndInvalidNumbers(t *testing.T) {
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name       string
		script     string
		wantStderr string
	}{
		{
			name:       "missing operand",
			script:     "seq\n",
			wantStderr: "seq: missing operand",
		},
		{
			name:       "zero increment",
			script:     "seq 10 0 32\n",
			wantStderr: "seq: invalid Zero increment value: '0'",
		},
		{
			name:       "invalid number",
			script:     "seq NaN\n",
			wantStderr: "seq: invalid 'not-a-number' argument: 'NaN'",
		},
		{
			name:       "empty format",
			script:     "seq -f '' 1\n",
			wantStderr: "seq: format '' has no % directive",
		},
		{
			name:       "too many format directives",
			script:     "seq -f '%g%' 1\n",
			wantStderr: "seq: format '%g%' has too many % directives",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("ExitCode = 0, want non-zero")
			}
			if !strings.Contains(result.Stderr, tc.wantStderr) {
				t.Fatalf("Stderr = %q, want to contain %q", result.Stderr, tc.wantStderr)
			}
		})
	}
}

func TestSeqInfiniteOutputCanBeBoundedByTimeout(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "timeout 0.02 seq inf > /tmp/seq.out || true\nhead -n 3 /tmp/seq.out\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n2\n3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout marker", result.Stderr)
	}
}
