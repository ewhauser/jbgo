package builtins_test

import (
	"context"
	"testing"
)

func TestSedSupportsScriptFileFlagIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 's/foo/bar/\\n2p\\n' > /tmp/script.sed\n" +
			"printf 'foo\\nfoo\\n' > /tmp/in.txt\n" +
			"sed -f /tmp/script.sed /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "bar\nbar\nbar\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSedBasicRegexpPlusMatchesOneOrMoreSpaces(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a   b\\n' | sed 's/ \\+/ /g'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a b\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
