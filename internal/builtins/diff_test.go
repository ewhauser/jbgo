package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestDiffSupportsLongFlagAliases(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestDiffUnidirectionalNewFileOnlyAppliesToFirstOperand(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\n' > /tmp/existing.txt\ndiff --unidirectional-new-file /tmp/existing.txt /tmp/missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "/tmp/missing.txt") {
		t.Fatalf("Stderr = %q, want missing path", result.Stderr)
	}
}

func TestDiffDirectoryOperandReportsTrouble(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/dir/subfile\nprintf 'one\\n' > /tmp/subfile\ndiff /tmp/dir /tmp/subfile\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if got, want := result.Stderr, "diff: /tmp/dir/subfile: Is a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestDiffContextOutputUsesPlusForInsertions(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\n' > /tmp/left.txt\nprintf 'a\\nb\\n' > /tmp/right.txt\ndiff --context /tmp/left.txt /tmp/right.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if strings.Contains(result.Stdout, "! b") {
		t.Fatalf("Stdout = %q, insertion should not use ! marker", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "+ b") {
		t.Fatalf("Stdout = %q, insertion should use + marker", result.Stdout)
	}
}

func TestDiffHelpAndVersionShortCircuitOptionParsing(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	helpResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "diff --help --definitely-invalid\n",
	})
	if err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if helpResult.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", helpResult.ExitCode, helpResult.Stderr)
	}
	if !strings.Contains(helpResult.Stdout, "Usage: diff [OPTION]... FILES") {
		t.Fatalf("help Stdout = %q, want help text", helpResult.Stdout)
	}

	versionResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "diff -v -z\n",
	})
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if versionResult.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", versionResult.ExitCode, versionResult.Stderr)
	}
	if !strings.Contains(versionResult.Stdout, "diff (GNU diffutils) 3.12") {
		t.Fatalf("version Stdout = %q, want version text", versionResult.Stdout)
	}
}
