package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestFactorHelp(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "factor --help\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "Print the prime factors of the given NUMBER(s).\n\n" +
		"Usage: factor [OPTION]... [NUMBER]...\n\n" +
		"If no NUMBER is specified, read it from standard input.\n\n" +
		"Options:\n" +
		"  -h, --exponents  Print factors in the form p^e\n" +
		"      --help       display this help and exit\n" +
		"      --version    output version information and exit\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestFactorVersion(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "factor --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "factor (gbash)\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestFactorSupportsArgumentsExponentFormsAndLongInference(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "factor 3 6 ' +9'\n" +
			"factor -hh 1234 10240\n" +
			"factor --exp 8\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "" +
		"3: 3\n" +
		"6: 2 3\n" +
		"9: 3 3\n" +
		"1234: 2 617\n" +
		"10240: 2^11 5\n" +
		"8: 2^3\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestFactorKeepsGoingAfterInvalidNumbers(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "factor not-a-valid-number 12\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stdout, "12: 2 2 3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	wantErr := "factor: warning: 'not-a-valid-number': invalid digit found in string\n"
	if got := result.Stderr; got != wantErr {
		t.Fatalf("Stderr = %q, want %q", got, wantErr)
	}
}

func TestFactorMatchesUpstreamStdinTokenizationForBinaryInput(t *testing.T) {
	session := newSession(t, &Config{})
	input := []byte("\x00 \xff\x00\xff\xaa\x00\xaa\x44 a&#2\n6 9\x003\xc024\t2\t\t4\x000+4\xff \xf7\xc1")
	writeSessionFile(t, session, "/tmp/factor-input.bin", input)

	result := mustExecSession(t, session, "cat /tmp/factor-input.bin | factor\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}

	wantOut := "" +
		"6: 2 3\n" +
		"9: 3 3\n" +
		"2: 2\n" +
		"4: 2 2\n"
	if got := result.Stdout; got != wantOut {
		t.Fatalf("Stdout = %q, want %q", got, wantOut)
	}

	wantErr := "" +
		"factor: warning: '': invalid digit found in string\n" +
		"factor: warning: '\\377': invalid digit found in string\n" +
		"factor: warning: 'a&#2': invalid digit found in string\n" +
		"factor: warning: '\\367\\301': invalid digit found in string\n"
	if got := result.Stderr; got != wantErr {
		t.Fatalf("Stderr = %q, want %q", got, wantErr)
	}
}

func TestFactorHandlesLargeNumbersBeyondUint64AndUint128(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "factor 4611686018427387896\n" +
			"factor 158909489063877810457\n" +
			"factor '+170141183460469231731687303715884105729'\n" +
			"factor -h 340282366920938463463374607431768211456\n" +
			"factor -h 115792089237316195423570985008687907853269984665640564039457584007913129639936\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "" +
		"4611686018427387896: 2 2 2 179951 3203431780337\n" +
		"158909489063877810457: 3401347 3861211 12099721\n" +
		"170141183460469231731687303715884105729: 3 56713727820156410577229101238628035243\n" +
		"340282366920938463463374607431768211456: 2^128\n" +
		"115792089237316195423570985008687907853269984665640564039457584007913129639936: 2^256\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestFactorRejectsNegativeOptionLikeGNUFactor(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "factor -1\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty stdout", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "factor: invalid option -- '1'") {
		t.Fatalf("Stderr = %q, want invalid-option error", result.Stderr)
	}
}
