package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestJoinKeepsUnpairedSecondFileLinesInSortOrder(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\n' > /tmp/left\nprintf 'a\\nb\\n' > /tmp/right\njoin -a2 /tmp/left /tmp/right\n",
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

func TestJoinAutoOutputHonorsNonFirstJoinField(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: strings.Join([]string{
			"printf 'a 1 2\\nb 1\\nd 1 2\\n' > /tmp/left",
			"printf 'a 3 4\\nb 3 4\\nc 3 4\\n' > /tmp/right",
			"join -j3 -a1 -a2 -e . -o auto /tmp/left /tmp/right",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2 a 1 . .\n. b 1 . .\n2 d 1 . .\n4 . . a 3\n4 . . b 3\n4 . . c 3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJoinDefaultBlankSeparatorDoesNotTreatFormFeedAsWhitespace(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '\\f 1\\n' > /tmp/left\nprintf '\\f 2\\n' > /tmp/right\njoin /tmp/left /tmp/right\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\f 1 2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJoinHeaderCheckOrderUsesDataLineNumbers(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: strings.Join([]string{
			"printf 'ID Name\\n2 B\\n1 A\\n' > /tmp/left",
			"printf 'ID Color\\n2 blue\\n' > /tmp/right",
			"join --header --check-order /tmp/left /tmp/right",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "ID Name Color\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "join: /tmp/left:3: is not sorted: 1 A\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestJoinSupportsAttachedCompatFieldSyntax(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '2 a\\n1 b\\n' > /tmp/left\nprintf '2 c\\n1 d\\n' > /tmp/right\njoin -12 /tmp/left /tmp/right\n",
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
