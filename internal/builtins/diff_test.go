package builtins_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/policy"
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

func TestDiffNoDereferenceReportsSymlinkTypeMismatch(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	writeSessionFile(t, session, "/tmp/regular", []byte("target\n"))
	if err := session.FileSystem().Symlink(context.Background(), "target", "/tmp/link"); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "diff --no-dereference /tmp/regular /tmp/link\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "File /tmp/regular is a regular file while file /tmp/link is a symbolic link\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestDiffIgnoreSpaceChangePreservesLeadingWhitespace(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'x\\n' > /tmp/a.txt\nprintf ' x\\n' > /tmp/b.txt\ndiff -b /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "< x") || !strings.Contains(result.Stdout, ">  x") {
		t.Fatalf("Stdout = %q, want leading-space difference", result.Stdout)
	}
}

func TestDiffRejectsNegativeContextLengths(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	for _, script := range []string{
		"diff --context=-1 /dev/null /dev/null\n",
		"diff -U-1 /dev/null /dev/null\n",
	} {
		result, err := rt.Run(context.Background(), &ExecutionRequest{Script: script})
		if err != nil {
			t.Fatalf("Run(%q) error = %v", script, err)
		}
		if result.ExitCode != 2 {
			t.Fatalf("Run(%q) ExitCode = %d, want 2; stdout=%q stderr=%q", script, result.ExitCode, result.Stdout, result.Stderr)
		}
		if result.Stdout != "" {
			t.Fatalf("Run(%q) Stdout = %q, want empty", script, result.Stdout)
		}
		if !strings.Contains(result.Stderr, "invalid context length '-1'") {
			t.Fatalf("Run(%q) Stderr = %q, want invalid context length", script, result.Stderr)
		}
	}
}

func TestDiffRecursiveNewFileDescendsIntoOneSidedDirectory(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/left/dir/sub\nmkdir -p /tmp/right\nprintf 'x\\n' > /tmp/left/dir/sub/file\ndiff -r -N /tmp/left /tmp/right\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if strings.Contains(result.Stderr, "Is a directory") {
		t.Fatalf("Stderr = %q, should recurse instead of reporting trouble", result.Stderr)
	}
	if !strings.Contains(result.Stdout, "diff -r -N /tmp/left/dir/sub/file /tmp/right/dir/sub/file") {
		t.Fatalf("Stdout = %q, want recursive file comparison", result.Stdout)
	}
}

func TestDiffNewFileMissingDirectoryWithoutRecursiveReportsCommonSubdirectory(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/left/dir/sub\nmkdir -p /tmp/right\nprintf 'x\\n' > /tmp/left/dir/sub/file\ndiff -N /tmp/left /tmp/right\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "Common subdirectories: /tmp/left/dir and /tmp/right/dir\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDiffSideBySideUsesPipeForReplacementRows(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'old\\n' > /tmp/a.txt\nprintf 'new\\n' > /tmp/b.txt\ndiff -y /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("Stdout = %q, want a single replacement row", result.Stdout)
	}
	if !strings.Contains(lines[0], "|") || !strings.Contains(lines[0], "old") || !strings.Contains(lines[0], "new") {
		t.Fatalf("Stdout = %q, want side-by-side replacement row", result.Stdout)
	}
	if strings.Contains(lines[0], "<") || strings.Contains(lines[0], ">") {
		t.Fatalf("Stdout = %q, replacement should not use delete/insert markers", result.Stdout)
	}
}

func TestDiffRejectsNegativeNumericOptionValues(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		script string
		want   string
	}{
		{script: "diff --width=-1 /dev/null /dev/null\n", want: "invalid width '-1'"},
		{script: "diff -W0 /dev/null /dev/null\n", want: "invalid width '0'"},
		{script: "diff --tabsize=-1 /dev/null /dev/null\n", want: "invalid tabsize '-1'"},
		{script: "diff --horizon-lines=-1 /dev/null /dev/null\n", want: "invalid horizon length '-1'"},
	}
	for _, tc := range tests {
		result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
		if err != nil {
			t.Fatalf("Run(%q) error = %v", tc.script, err)
		}
		if result.ExitCode != 2 {
			t.Fatalf("Run(%q) ExitCode = %d, want 2; stdout=%q stderr=%q", tc.script, result.ExitCode, result.Stdout, result.Stderr)
		}
		if result.Stdout != "" {
			t.Fatalf("Run(%q) Stdout = %q, want empty", tc.script, result.Stdout)
		}
		if !strings.Contains(result.Stderr, tc.want) {
			t.Fatalf("Run(%q) Stderr = %q, want %q", tc.script, result.Stderr, tc.want)
		}
	}
}
