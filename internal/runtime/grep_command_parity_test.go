package runtime

import (
	"context"
	"testing"
)

func TestGrepSupportsMatchModeFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a.c\\naxc\\n' > /tmp/fixed.txt\n" +
			"printf 'foo\\nfoobar\\nfoo\\n' > /tmp/line.txt\n" +
			"printf 'cat dog cat\\n' > /tmp/only.txt\n" +
			"printf 'foo1\\nbar22\\n' > /tmp/perl.txt\n" +
			"printf 'match\\nmatch\\n' > /tmp/a.txt\n" +
			"printf 'match\\n' > /tmp/b.txt\n" +
			"grep --fixed-strings 'a.c' /tmp/fixed.txt\n" +
			"grep -F 'a.c' /tmp/fixed.txt\n" +
			"grep --line-regexp foo /tmp/line.txt\n" +
			"grep -x foo /tmp/line.txt\n" +
			"grep --only-matching cat /tmp/only.txt\n" +
			"grep -oP '[0-9]+' /tmp/perl.txt\n" +
			"grep -h match /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a.c\na.c\nfoo\nfoo\nfoo\nfoo\ncat\ncat\n1\n22\nmatch\nmatch\nmatch\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepSupportsContextFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'line1\\nline2\\nmatch\\nline4\\nline5\\n' > /tmp/context.txt\n" +
			"grep -A1 match /tmp/context.txt\n" +
			"grep -B1 match /tmp/context.txt\n" +
			"grep -C1 match /tmp/context.txt\n" +
			"grep -n -B1 -A1 match /tmp/context.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "match\nline4\nline2\nmatch\nline2\nmatch\nline4\n2-line2\n3:match\n4-line4\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepSupportsFilesWithoutMatchQuietAndMaxCountIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hit\\nhit\\nmiss\\n' > /tmp/hits.txt\n" +
			"printf 'miss\\n' > /tmp/miss.txt\n" +
			"grep --files-without-match hit /tmp/hits.txt /tmp/miss.txt\n" +
			"grep -L hit /tmp/hits.txt /tmp/miss.txt\n" +
			"grep --max-count=1 hit /tmp/hits.txt\n" +
			"grep -m1 hit /tmp/hits.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/miss.txt\n/tmp/miss.txt\nhit\nhit\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepQuietSuppressesOutputIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hit\\n' > /tmp/hit.txt\ngrep --quiet hit /tmp/hit.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("want quiet output, got stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestGrepQuietNoMatchReturnsOneIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hit\\n' > /tmp/hit.txt\ngrep -q miss /tmp/hit.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("want quiet output, got stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}
