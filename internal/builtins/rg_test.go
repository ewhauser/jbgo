package builtins_test

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRGSearchesCurrentDirWhenStdinLooksLikeTTY(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Stdin: rgTTYReader{path: "/dev/tty"},
		Script: "mkdir -p /tmp/work/sub\n" +
			"printf 'needle\\n' > /tmp/work/sub/input.txt\n" +
			"cd /tmp/work\n" +
			"rg needle\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "sub/input.txt:needle\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGSearchesStdinWhenInputIsNotTTY(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'alpha\\nbeta\\n' | rg alpha\nprintf 'status=%d\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "alpha\nstatus=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRGSupportsPatternSourcesExplicitFilesAndCaseModes(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'alpha\\nbeta\\nWarning\\nwarning\\n' > /tmp/input.txt\n" +
			"printf '^alpha$\\n^beta$\\n' > /tmp/patterns.txt\n" +
			"printf 'hit\\n' > /tmp/a.txt\n" +
			"printf 'miss\\n' > /tmp/b.txt\n" +
			"rg -e '^alpha$' -f /tmp/patterns.txt /tmp/input.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg warning /tmp/input.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg -S warning /tmp/input.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg -S Warning /tmp/input.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg -S -s warning /tmp/input.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg -s -i warning /tmp/input.txt\n" +
			"printf '%s\\n' '---'\n" +
			"cd /tmp\n" +
			"rg hit a.txt b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := strings.Join([]string{
		"alpha",
		"beta",
		"---",
		"warning",
		"---",
		"Warning",
		"warning",
		"---",
		"Warning",
		"---",
		"warning",
		"---",
		"Warning",
		"warning",
		"---",
		"a.txt:hit",
		"",
	}, "\n")
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGRespectsHiddenIgnoreFilesAndExplicitPathBypass(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/work/.git\n" +
			"printf 'ref: refs/heads/main\\n' > /tmp/work/.git/HEAD\n" +
			"printf 'hit\\n' > /tmp/work/visible.txt\n" +
			"printf 'hit\\n' > /tmp/work/.hidden.txt\n" +
			"printf 'hit\\n' > /tmp/work/vcs.log\n" +
			"printf 'hit\\n' > /tmp/work/local.txt\n" +
			"printf 'hit\\n' > /tmp/work/extra.txt\n" +
			"printf 'hit\\n' > /tmp/work/manual.txt\n" +
			"printf '*.log\\n' > /tmp/work/.gitignore\n" +
			"printf 'local.txt\\n' > /tmp/work/.ignore\n" +
			"printf 'extra.txt\\n' > /tmp/work/.rgignore\n" +
			"printf 'manual.txt\\n' > /tmp/work/manual.ignore\n" +
			"cd /tmp/work\n" +
			"rg hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg --hidden hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg --no-ignore-vcs hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg --no-ignore hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg --no-ignore --ignore-file manual.ignore hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg hit .hidden.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg hit vcs.log\n" +
			"printf '%s\\n' '---'\n" +
			"rg -g '*.nomatch' hit manual.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := strings.Join([]string{
		"./manual.txt:hit",
		"./visible.txt:hit",
		"---",
		"./.hidden.txt:hit",
		"./manual.txt:hit",
		"./visible.txt:hit",
		"---",
		"./manual.txt:hit",
		"./vcs.log:hit",
		"./visible.txt:hit",
		"---",
		"./extra.txt:hit",
		"./local.txt:hit",
		"./manual.txt:hit",
		"./vcs.log:hit",
		"./visible.txt:hit",
		"---",
		"./extra.txt:hit",
		"./local.txt:hit",
		"./vcs.log:hit",
		"./visible.txt:hit",
		"---",
		"hit",
		"---",
		"hit",
		"---",
		"hit",
		"",
	}, "\n")
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGSupportsGlobsAndFilesMode(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/work/sub\n" +
			"printf 'hit\\n' > /tmp/work/want.txt\n" +
			"printf 'hit\\n' > /tmp/work/skip.txt\n" +
			"printf 'hit\\n' > /tmp/work/sub/deep.txt\n" +
			"printf 'hit\\n' > /tmp/work/CAPS.LOG\n" +
			"printf 'xx\\0hit\\n' > /tmp/work/bin.dat\n" +
			"cd /tmp/work\n" +
			"rg -g '*.txt' -g '!skip.txt' hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg --iglob '*.log' hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg --files . | sort\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := strings.Join([]string{
		"./sub/deep.txt:hit",
		"./want.txt:hit",
		"---",
		"./CAPS.LOG:hit",
		"---",
		"./CAPS.LOG",
		"./bin.dat",
		"./skip.txt",
		"./sub/deep.txt",
		"./want.txt",
		"",
	}, "\n")
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGGlobsOverrideHiddenAndIgnoreAndPruneExcludedDirs(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/work/.git /tmp/work/.hidden /tmp/work/ignored /tmp/work/keep /tmp/work/node_modules\n" +
			"printf 'ref: refs/heads/main\\n' > /tmp/work/.git/HEAD\n" +
			"printf 'ignored/\\n' > /tmp/work/.gitignore\n" +
			"printf 'hit\\n' > /tmp/work/.hidden.txt\n" +
			"printf 'hit\\n' > /tmp/work/.hidden/inside.txt\n" +
			"printf 'hit\\n' > /tmp/work/ignored/inside.txt\n" +
			"printf 'hit\\n' > /tmp/work/keep/keep.txt\n" +
			"printf 'hit\\n' > /tmp/work/node_modules/skip.txt\n" +
			"printf 'hit\\n' > /tmp/work/root.txt\n" +
			"cd /tmp/work\n" +
			"rg -g '.hidden.txt' hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg -g '.hidden' -g '.hidden/**' hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg -g 'ignored' -g 'ignored/**' hit . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg -g '!node_modules' hit . | sort\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := strings.Join([]string{
		"./.hidden.txt:hit",
		"---",
		"./.hidden/inside.txt:hit",
		"---",
		"./ignored/inside.txt:hit",
		"---",
		"./keep/keep.txt:hit",
		"./root.txt:hit",
		"",
	}, "\n")
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGLoadsGitIgnoreRulesFromRepoAncestorsAndSubdirs(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/repo/.git /tmp/repo/sub\n" +
			"printf 'ref: refs/heads/main\\n' > /tmp/repo/.git/HEAD\n" +
			"printf 'sub/root-ignored.txt\\n' > /tmp/repo/.gitignore\n" +
			"printf 'nested-ignored.txt\\n' > /tmp/repo/sub/.gitignore\n" +
			"printf 'hit\\n' > /tmp/repo/sub/root-ignored.txt\n" +
			"printf 'hit\\n' > /tmp/repo/sub/nested-ignored.txt\n" +
			"printf 'hit\\n' > /tmp/repo/sub/visible.txt\n" +
			"cd /tmp/repo/sub\n" +
			"rg hit .\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "./visible.txt:hit\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGTreatsDotGitFileAsGitBoundary(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/repo/sub\n" +
			"printf 'gitdir: /tmp/gitdir\\n' > /tmp/repo/.git\n" +
			"printf 'sub/ignored.txt\\n' > /tmp/repo/.gitignore\n" +
			"printf 'hit\\n' > /tmp/repo/sub/ignored.txt\n" +
			"printf 'hit\\n' > /tmp/repo/sub/visible.txt\n" +
			"cd /tmp/repo/sub\n" +
			"rg hit .\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "./visible.txt:hit\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGFollowsSymlinkDirsUsingLogicalIgnorePaths(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/repo/.git /tmp/target\n" +
			"printf 'ref: refs/heads/main\\n' > /tmp/repo/.git/HEAD\n" +
			"printf 'link/ignored.txt\\n' > /tmp/repo/.gitignore\n" +
			"printf 'hit\\n' > /tmp/target/ignored.txt\n" +
			"printf 'hit\\n' > /tmp/target/visible.txt\n" +
			"ln -s ../target /tmp/repo/link\n" +
			"cd /tmp/repo\n" +
			"rg -L hit . | sort\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "./link/visible.txt:hit\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGSupportsOutputModesAndContext(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/work\n" +
			"printf 'alpha alpha\\nbeta\\n' > /tmp/work/hit.txt\n" +
			"printf 'beta\\n' > /tmp/work/miss.txt\n" +
			"printf 'one\\nmatch\\ntwo\\n' > /tmp/work/ctx.txt\n" +
			"cd /tmp/work\n" +
			"rg -l alpha .\n" +
			"printf '%s\\n' '---'\n" +
			"rg --files-without-match alpha . | sort\n" +
			"printf '%s\\n' '---'\n" +
			"rg -c alpha hit.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg -o alpha hit.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg -C1 match ctx.txt\n" +
			"printf '%s\\n' '---'\n" +
			"rg -q alpha hit.txt\n" +
			"printf 'quiet_status=%d\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := strings.Join([]string{
		"./hit.txt",
		"---",
		"./ctx.txt",
		"./miss.txt",
		"---",
		"1",
		"---",
		"alpha",
		"alpha",
		"---",
		"one",
		"match",
		"two",
		"---",
		"quiet_status=0",
		"",
	}, "\n")
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestRGFollowLinksAndReportsLoops(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/work/root/sub /tmp/work/real\n" +
			"printf 'hit\\n' > /tmp/work/root/file.txt\n" +
			"printf 'hit\\n' > /tmp/work/real/data.txt\n" +
			"ln -s ../real/data.txt /tmp/work/root/linkfile\n" +
			"ln -s ../real /tmp/work/root/linkdir\n" +
			"ln -s .. /tmp/work/root/sub/back\n" +
			"cd /tmp/work\n" +
			"rg hit root\n" +
			"printf 'root_status=%d\\n' \"$?\"\n" +
			"printf '%s\\n' '---'\n" +
			"rg -L hit root\n" +
			"printf 'follow_status=%d\\n' \"$?\"\n" +
			"printf '%s\\n' '---'\n" +
			"rg hit root/linkfile\n" +
			"printf 'explicit_status=%d\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	stdoutLines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for _, want := range []string{
		"root/file.txt:hit",
		"root_status=0",
		"root/linkdir/data.txt:hit",
		"root/linkfile:hit",
		"follow_status=2",
		"hit",
		"explicit_status=0",
	} {
		if !containsLine(stdoutLines, want) {
			t.Fatalf("Stdout lines = %q, missing %q", stdoutLines, want)
		}
	}
	if got := result.Stderr; !strings.Contains(got, "File system loop found") {
		t.Fatalf("Stderr = %q, want loop diagnostic", got)
	}
}

func TestRGRejectsUnsupportedAdvancedFlags(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	for _, tc := range []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "pcre2",
			script: "printf 'x\\n' > /tmp/in.txt\nrg -P x /tmp/in.txt\n",
			want:   "rg: PCRE2 matching is not supported in this build\n",
		},
		{
			name:   "multiline",
			script: "printf 'x\\n' > /tmp/in.txt\nrg -U x /tmp/in.txt\n",
			want:   "rg: multiline mode is not supported in this build\n",
		},
		{
			name:   "json",
			script: "printf 'x\\n' > /tmp/in.txt\nrg --json x /tmp/in.txt\n",
			want:   "rg: JSON output is not supported in this build\n",
		},
		{
			name:   "type",
			script: "printf 'x\\n' > /tmp/in.txt\nrg --type rust x /tmp/in.txt\n",
			want:   "rg: file type filters are not supported in this build\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 2 {
				t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
			}
			if got := result.Stderr; got != tc.want {
				t.Fatalf("Stderr = %q, want %q", got, tc.want)
			}
		})
	}
}

type rgTTYReader struct {
	path string
}

func (r rgTTYReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (r rgTTYReader) RedirectPath() string {
	return r.path
}

func (rgTTYReader) RedirectFlags() int {
	return 0
}

func (rgTTYReader) RedirectOffset() int64 {
	return 0
}
