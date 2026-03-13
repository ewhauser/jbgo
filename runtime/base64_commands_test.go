package runtime

import (
	"context"
	"testing"
)

func TestBase64SupportsSeparateLongWrapArgument(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hello world' | base64 --wrap 0\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "aGVsbG8gd29ybGQ="; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBase64SupportsCombinedDecodeIgnoreGarbageFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '@aGVsbG8gd29ybGQ=\\n' | base64 -di\n",
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

func TestBase64DecodeAcceptsUnpaddedAndConcatenatedInput(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'aQ\\n' | base64 --decode\nprintf '\\n'\nprintf 'MTIzNA==MTIzNA\\n' | base64 --decode\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "i\n12341234"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBase64DecodePreservesRecoveredBytesOnInvalidTail(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'SGVsbG9=' | base64 --decode\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Hello"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "base64: invalid input\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBase64DecodeRepairsShortPaddedTailBeforeFailing(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'Zz=' | base64 --decode\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "g"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "base64: invalid input\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBase64SupportsShortAliasAndInferredLongOptions(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'aGVsbG8sIHdvcmxkIQ==\\n' | base64 -D --ig\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello, world!"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBase64SupportsFileInputAndInferredWrapFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hello, world!' > /tmp/input.txt\nbase64 --wr=10 /tmp/input.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "aGVsbG8sIH\ndvcmxkIQ==\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
