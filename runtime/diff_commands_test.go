package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestDiffSupportsLongFlagAliases(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\n' > /tmp/a.txt\nprintf 'ONE\\nTWO\\n' > /tmp/b.txt\ndiff --ignore-case --report-identical-files /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Files /tmp/a.txt and /tmp/b.txt are identical\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDiffSupportsLongBriefAndUnifiedFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	briefResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\n' > /tmp/a.txt\nprintf 'two\\n' > /tmp/b.txt\ndiff --brief /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if briefResult.ExitCode != 1 {
		t.Fatalf("brief ExitCode = %d, want 1; stderr=%q", briefResult.ExitCode, briefResult.Stderr)
	}
	if got, want := briefResult.Stdout, "Files /tmp/a.txt and /tmp/b.txt differ\n"; got != want {
		t.Fatalf("brief Stdout = %q, want %q", got, want)
	}

	unifiedResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\n' > /tmp/a.txt\nprintf 'one\\nthree\\n' > /tmp/b.txt\ndiff --unified /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if unifiedResult.ExitCode != 1 {
		t.Fatalf("unified ExitCode = %d, want 1; stderr=%q", unifiedResult.ExitCode, unifiedResult.Stderr)
	}
	for _, want := range []string{"--- /tmp/a.txt", "+++ /tmp/b.txt", "-two", "+three"} {
		if !strings.Contains(unifiedResult.Stdout, want) {
			t.Fatalf("unified Stdout = %q, want %q", unifiedResult.Stdout, want)
		}
	}
}
