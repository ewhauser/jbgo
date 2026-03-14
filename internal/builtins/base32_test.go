package builtins_test

import (
	"context"
	"testing"
)

func TestBase32EncodesWithoutWrapping(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hello world' | base32 --wrap 0\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "NBSWY3DPEB3W64TMMQ======"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBase32DecodesIgnoringGarbageWithCombinedShortFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '&NBSWY3DPEB3W64TMMQ======\\n' | base32 -di\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello world"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBase32OmitsTrailingNewlineForEmptyInput(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '' | base32\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty output", result.Stdout)
	}
}

func TestBase32SupportsShortAliasAndInferredLongOptions(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'JBSWY3DPFQQFO33SNRSCC===\\n' | base32 -D --ig\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Hello, World!"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBase32SupportsFileInputAndInferredWrapFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'Hello, World!\\n' > /tmp/input.txt\nbase32 --wr=8 /tmp/input.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "JBSWY3DP\nFQQFO33S\nNRSCCCQ=\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
