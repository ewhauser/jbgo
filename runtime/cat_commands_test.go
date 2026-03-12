package runtime

import (
	"context"
	"testing"
)

func TestCatSupportsLongAndShortNumberFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'alpha\\nbeta\\n' > /tmp/a.txt\nprintf 'gamma\\n' > /tmp/b.txt\ncat --number /tmp/a.txt /tmp/b.txt\ncat -n /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "     1\talpha\n     2\tbeta\n     3\tgamma\n     1\tgamma\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCatShowEndsHandlesCarriageReturnsAcrossFiles(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\r' > /tmp/a.txt\nprintf '\\n2\\r\\n' > /tmp/b.txt\ncat -E /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1^M$\n2^M$\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCatShowEndsLeavesStandaloneCarriageReturnLiteral(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\r' > /tmp/a.txt\nprintf '2\\r\\n' > /tmp/b.txt\ncat -E /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\r2^M$\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCatRejectsShellAppendToSelf(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'x\\n' > /tmp/out\ncat /tmp/out >> /tmp/out\nprintf 'status:%s\\n' \"$?\"\ncat /tmp/out\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status:1\nx\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "cat: /tmp/out: input file is output file\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}
