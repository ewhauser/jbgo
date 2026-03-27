package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestSortSupportsLongOrderingFlagsIsolated(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestSortAllowsEquivalentQuietCheckModes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\nb\\n' > /tmp/in.txt\nsort -C --check=quiet /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestSortSupportsLegacyPlusKeySyntax(t *testing.T) {
	t.Parallel()
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

func TestSortSupportsAbbreviatedSortAndCheckModes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '0.1\\n0.02\\n0.2\\n0.002\\n0.3\\n' > /tmp/version.txt\n" +
			"sort --sort=v /tmp/version.txt\n" +
			"printf 'a\\nb\\n' > /tmp/check-ok.txt\n" +
			"sort --check=d /tmp/check-ok.txt\n" +
			"printf 'a\\nc\\nb\\n' > /tmp/check-bad.txt\n" +
			"sort --check=q /tmp/check-bad.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0.1\n0.002\n0.02\n0.2\n0.3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestSortRejectsEmptySortAndCheckModes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "empty sort mode",
			script: "printf '' > /tmp/in.txt\nsort --sort= /tmp/in.txt\n",
			want:   "invalid argument \"\" for --sort",
		},
		{
			name:   "empty check mode",
			script: "printf '' > /tmp/in.txt\nsort --check= /tmp/in.txt\n",
			want:   "invalid argument \"\" for --check",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := newRuntime(t, &Config{})
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 2 {
				t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
			}
			if !strings.Contains(result.Stderr, tc.want) {
				t.Fatalf("Stderr = %q, want to contain %q", result.Stderr, tc.want)
			}
		})
	}
}

func TestSortRejectsCheckOutputAndExtraOperandCombinations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "check output",
			script: "printf 'a\\nb\\n' > /tmp/in.txt\nsort -c -o /tmp/out.txt /tmp/in.txt\n",
			want:   "options '-co' are incompatible",
		},
		{
			name:   "check silent output",
			script: "printf 'a\\nb\\n' > /tmp/in.txt\nsort -C -o /tmp/out.txt /tmp/in.txt\n",
			want:   "options '-Co' are incompatible",
		},
		{
			name:   "check conflict",
			script: "printf 'a\\nb\\n' > /tmp/in.txt\nsort -c -C /tmp/in.txt\n",
			want:   "options '-cC' are incompatible",
		},
		{
			name:   "extra operand with check",
			script: "printf 'a\\n' > /tmp/a\nprintf 'b\\n' > /tmp/b\nsort -c /tmp/a /tmp/b\n",
			want:   "extra operand '/tmp/b' not allowed with -c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := newRuntime(t, &Config{})
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 2 {
				t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
			}
			if !strings.Contains(result.Stderr, tc.want) {
				t.Fatalf("Stderr = %q, want to contain %q", result.Stderr, tc.want)
			}
		})
	}
}

func TestSortRejectsMultipleOutputFiles(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\na\\n' > /tmp/in.txt\nsort -o /tmp/out1.txt -o /tmp/out2.txt /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "multiple output files specified") {
		t.Fatalf("Stderr = %q, want multiple-output error", result.Stderr)
	}
}

func TestSortRejectsIncompatibleGlobalModesWhenKeysPresent(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n' > /tmp/in.txt\nsort -n -g -k1,1 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "options '-gn' are incompatible") {
		t.Fatalf("Stderr = %q, want global-ordering incompatibility", result.Stderr)
	}
}

func TestSortMergeBatchSizeValidatesConfiguredTempDirs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		script     string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "usable temp dir",
			script: "printf '1\\n' > /tmp/a\n" +
				"printf '2\\n' > /tmp/b\n" +
				"printf '3\\n' > /tmp/c\n" +
				"sort -m --batch-size=2 -T /tmp /tmp/a /tmp/b /tmp/c\n",
			wantExit:   0,
			wantStdout: "1\n2\n3\n",
		},
		{
			name: "later usable temp dir",
			script: "printf '1\\n' > /tmp/a\n" +
				"printf '2\\n' > /tmp/b\n" +
				"printf '3\\n' > /tmp/c\n" +
				"sort -m --batch-size=2 -T /tmp/missing-merge-dir -T /tmp /tmp/a /tmp/b /tmp/c\n",
			wantExit:   0,
			wantStdout: "1\n2\n3\n",
		},
		{
			name: "missing temp dir",
			script: "printf '1\\n' > /tmp/a\n" +
				"printf '2\\n' > /tmp/b\n" +
				"printf '3\\n' > /tmp/c\n" +
				"sort -m --batch-size=2 -T /tmp/missing-merge-dir /tmp/a /tmp/b /tmp/c\n",
			wantExit:   2,
			wantStderr: "cannot create temporary file in '/tmp/missing-merge-dir':",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := newRuntime(t, &Config{})
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantExit, result.Stderr)
			}
			if tc.wantStdout != "" && result.Stdout != tc.wantStdout {
				t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.wantStdout)
			}
			if tc.wantStderr != "" && !strings.Contains(result.Stderr, tc.wantStderr) {
				t.Fatalf("Stderr = %q, want to contain %q", result.Stderr, tc.wantStderr)
			}
		})
	}
}

func TestSortRejectsZeroParallel(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\na\\n' > /tmp/in.txt\nsort --parallel=0 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "number in parallel must be nonzero") {
		t.Fatalf("Stderr = %q, want zero-parallel error", result.Stderr)
	}
}
