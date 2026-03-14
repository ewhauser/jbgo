package runtime

import (
	"context"
	"testing"
)

func TestSortSupportsLongOrderingFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '  zebra\\n alpha\\n' > /tmp/blanks.txt\n" +
			"printf 'b!\\na@\\n' > /tmp/dict.txt\n" +
			"printf '2K\\n100\\n1M\\n' > /tmp/human.txt\n" +
			"printf 'Feb\\nJan\\nDec\\n' > /tmp/months.txt\n" +
			"printf 'v1.10\\nv1.2\\nv1.1\\n' > /tmp/version.txt\n" +
			"printf 'zebra,10\\nalpha,2\\nbeta,1\\n' > /tmp/key.csv\n" +
			"sort --ignore-leading-blanks /tmp/blanks.txt\n" +
			"sort --dictionary-order /tmp/dict.txt\n" +
			"sort --human-numeric-sort /tmp/human.txt\n" +
			"sort --month-sort /tmp/months.txt\n" +
			"sort --version-sort /tmp/version.txt\n" +
			"sort --field-separator=, --key=2,2n /tmp/key.csv\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " alpha\n  zebra\na@\nb!\n100\n2K\n1M\nJan\nFeb\nDec\nv1.1\nv1.2\nv1.10\nbeta,1\nalpha,2\nzebra,10\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsCheckStableAndOutputFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b 1\\na 1\\n' > /tmp/stable.txt\n" +
			"sort -s -k2,2 /tmp/stable.txt -o /tmp/stable.out\n" +
			"cat /tmp/stable.out\n" +
			"printf 'a\\nb\\n' > /tmp/check-ok.txt\n" +
			"sort --check /tmp/check-ok.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "b 1\na 1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortCheckReportsDisorderIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\na\\n' > /tmp/check-bad.txt\nsort -c /tmp/check-bad.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if got, want := result.Stderr, "sort: /tmp/check-bad.txt:2: disorder: a\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestSortSupportsPostOperandOutputFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\na\\n' > /tmp/in.txt\nsort /tmp/in.txt -o /tmp/out.txt\ncat /tmp/out.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsCheckEqualsSilent(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\nc\\nb\\n' > /tmp/in.txt\nsort --check=silent /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestSortSupportsLegacyPlusKeySyntax(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'x 2\\ny 10\\nz 1\\n' > /tmp/in.txt\nsort +1 -2n /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "z 1\nx 2\ny 10\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
