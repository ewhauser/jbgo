package builtins_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/testutil"
)

func TestRGMatchesRipgrep(t *testing.T) {
	t.Parallel()

	ripgrepPath := testutil.RequireNixRipgrep(t)
	testCases := []struct {
		name       string
		stdin      string
		stdinTTY   bool
		sortStdout bool
		setup      func(t *testing.T, workDir string)
		buildArgs  func(workDir string) (gbashArgs, ripgrepArgs []string)
	}{
		{
			name: "explicit-current-dir",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "a.txt", "hit\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"hit", "."}, []string{"hit", "."}
			},
		},
		{
			name:  "stdin",
			stdin: "alpha\nbeta\n",
			buildArgs: func(string) ([]string, []string) {
				return []string{"alpha"}, []string{"alpha"}
			},
		},
		{
			name: "glob-filter",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "want.txt", "hit\n")
				writeHostFile(t, workDir, "skip.log", "hit\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-g", "*.txt", "hit", "."}, []string{"-g", "*.txt", "hit", "."}
			},
		},
		{
			name:       "hidden-and-ignore",
			sortStdout: true,
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, ".git/HEAD", "ref: refs/heads/main\n")
				writeHostFile(t, workDir, "visible.txt", "hit\n")
				writeHostFile(t, workDir, ".hidden.txt", "hit\n")
				writeHostFile(t, workDir, "skip.log", "hit\n")
				writeHostFile(t, workDir, ".gitignore", "*.log\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"--hidden", "hit", "."}, []string{"--hidden", "hit", "."}
			},
		},
		{
			name: "only-matching-line-number",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "hit hit\nmiss\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-n", "-o", "hit", "input.txt"}, []string{"-n", "-o", "hit", "input.txt"}
			},
		},
		{
			name: "files-mode",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "visible.txt", "hit\n")
				writeHostFile(t, workDir, "bin.dat", "xx\x00hit\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"--files", "."}, []string{"--files", "."}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			workDir := root + "/work"
			writeHostFile(t, root, "work/.keep", "")
			if tc.setup != nil {
				tc.setup(t, workDir)
			}

			gbashArgs, ripgrepArgs := tc.buildArgs(workDir)
			gbash := runGBashRG(t, root, tc.stdin, tc.stdinTTY, gbashArgs...)
			ripgrep := runRipgrep(t, ripgrepPath, workDir, tc.stdin, ripgrepArgs...)
			if tc.sortStdout {
				gbash.Stdout = sortOracleLines(gbash.Stdout)
				ripgrep.Stdout = sortOracleLines(ripgrep.Stdout)
			}

			if gbash != ripgrep {
				t.Fatalf("ripgrep mismatch\ngbash:   %+v\nripgrep: %+v", gbash, ripgrep)
			}
		})
	}
}

func runGBashRG(t testing.TB, root, stdin string, stdinTTY bool, args ...string) grepOracleResult {
	t.Helper()

	env := defaultBaseEnv()
	env["HOME"] = "/"
	env["LC_ALL"] = "C"
	env["LANG"] = "C"
	env["TZ"] = "UTC"

	rt := newRuntime(t, &Config{
		BaseEnv:    env,
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
	})

	var script strings.Builder
	script.WriteString("rg")
	for _, arg := range args {
		script.WriteByte(' ')
		script.WriteString(diffShellQuote(arg))
	}
	script.WriteByte('\n')

	req := &ExecutionRequest{
		WorkDir: "/work",
		Script:  script.String(),
		Stdin:   strings.NewReader(stdin),
	}
	if stdinTTY {
		req.Stdin = rgTTYReader{path: "/dev/tty"}
	}

	result, err := rt.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("gbash Run(%q) error = %v", script.String(), err)
	}

	return grepOracleResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}
}

func sortOracleLines(text string) string {
	trimmed := strings.TrimSuffix(text, "\n")
	if trimmed == "" {
		return text
	}
	lines := strings.Split(trimmed, "\n")
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}
