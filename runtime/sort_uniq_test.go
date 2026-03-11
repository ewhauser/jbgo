package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestSortSupportsKeySortingWithCustomDelimiter(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo zebra,10 > /tmp/in.csv\n echo alpha,2 >> /tmp/in.csv\n echo beta,1 >> /tmp/in.csv\n sort -t, -k2,2n /tmp/in.csv\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "beta,1\nalpha,2\nzebra,10\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsNumericReverseUniquePipeline(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 10 > /tmp/in.txt\n echo 2 >> /tmp/in.txt\n echo 2 >> /tmp/in.txt\n echo 1 >> /tmp/in.txt\n cat /tmp/in.txt | sort -nru\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "10\n2\n1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsCaseInsensitiveUnique(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo Apple > /tmp/in.txt\n echo apple >> /tmp/in.txt\n echo Banana >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n sort -fu /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Apple\nBanana\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortReturnsErrorForMissingFile(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "sort /missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/missing.txt") {
		t.Fatalf("Stderr = %q, want missing-file error", result.Stderr)
	}
}

func TestUniqSupportsCountsAndAdjacentRuns(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo apple > /tmp/in.txt\n echo apple >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n echo cherry >> /tmp/in.txt\n uniq -c /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "   2 apple\n   3 banana\n   1 cherry\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUniqWorksWithSortForFullDeduping(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo b > /tmp/in.txt\n echo a >> /tmp/in.txt\n echo b >> /tmp/in.txt\n echo c >> /tmp/in.txt\n sort /tmp/in.txt | uniq\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\nc\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUniqReturnsErrorForMissingFile(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uniq /missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/missing.txt") {
		t.Fatalf("Stderr = %q, want missing-file error", result.Stderr)
	}
}

func TestUniqSupportsIgnoreCase(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'Apple\\napple\\nBanana\\n' > /tmp/in.txt\nuniq --ignore-case -c /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "   2 Apple\n   1 Banana\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
