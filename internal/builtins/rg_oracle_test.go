package builtins_test

import (
	"context"
	"os"
	"path/filepath"
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
		runDir     func(workDir string) string
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
			name:       "glob-filter",
			sortStdout: true,
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "want.txt", "hit\n")
				writeHostFile(t, workDir, "skip.log", "hit\n")
				writeHostFile(t, workDir, "sub/deep.txt", "hit\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-g", "*.txt", "hit", "."}, []string{"-g", "*.txt", "hit", "."}
			},
		},
		{
			name: "hidden-include-glob-file",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, ".hidden.txt", "hit\n")
				writeHostFile(t, workDir, "visible.txt", "miss\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-g", ".hidden.txt", "hit", "."}, []string{"-g", ".hidden.txt", "hit", "."}
			},
		},
		{
			name: "hidden-include-glob-dir",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, ".hidden/inside.txt", "hit\n")
				writeHostFile(t, workDir, "visible.txt", "miss\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-g", ".hidden", "-g", ".hidden/**", "hit", "."}, []string{"-g", ".hidden", "-g", ".hidden/**", "hit", "."}
			},
		},
		{
			name: "include-glob-overrides-ignore",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, ".git/HEAD", "ref: refs/heads/main\n")
				writeHostFile(t, workDir, ".gitignore", "ignored/\n")
				writeHostFile(t, workDir, "ignored/inside.txt", "hit\n")
				writeHostFile(t, workDir, "visible.txt", "miss\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-g", "ignored", "-g", "ignored/**", "hit", "."}, []string{"-g", "ignored", "-g", "ignored/**", "hit", "."}
			},
		},
		{
			name:       "exclude-dir-glob",
			sortStdout: true,
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "node_modules/skip.txt", "hit\n")
				writeHostFile(t, workDir, "keep/keep.txt", "hit\n")
				writeHostFile(t, workDir, "root.txt", "hit\n")
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-g", "!node_modules", "hit", "."}, []string{"-g", "!node_modules", "hit", "."}
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
			name: "subdir-gitignore",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, ".git/HEAD", "ref: refs/heads/main\n")
				writeHostFile(t, workDir, ".gitignore", "sub/root-ignored.txt\n")
				writeHostFile(t, workDir, "sub/.gitignore", "nested-ignored.txt\n")
				writeHostFile(t, workDir, "sub/root-ignored.txt", "hit\n")
				writeHostFile(t, workDir, "sub/nested-ignored.txt", "hit\n")
				writeHostFile(t, workDir, "sub/visible.txt", "hit\n")
			},
			runDir: func(workDir string) string {
				return workDir + "/sub"
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"hit", "."}, []string{"hit", "."}
			},
		},
		{
			name: "git-file-boundary",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, ".git", "gitdir: /tmp/gitdir\n")
				writeHostFile(t, workDir, ".gitignore", "sub/ignored.txt\n")
				writeHostFile(t, workDir, "sub/ignored.txt", "hit\n")
				writeHostFile(t, workDir, "sub/visible.txt", "hit\n")
			},
			runDir: func(workDir string) string {
				return workDir + "/sub"
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"hit", "."}, []string{"hit", "."}
			},
		},
		{
			name: "follow-link-ignore",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, ".git/HEAD", "ref: refs/heads/main\n")
				writeHostFile(t, workDir, ".gitignore", "link/ignored.txt\n")

				targetDir := filepath.Join(filepath.Dir(workDir), "target")
				if err := os.MkdirAll(targetDir, 0o755); err != nil {
					t.Fatalf("MkdirAll(%q) error = %v", targetDir, err)
				}
				writeHostFile(t, filepath.Dir(workDir), "target/ignored.txt", "hit\n")
				writeHostFile(t, filepath.Dir(workDir), "target/visible.txt", "hit\n")
				if err := os.Symlink("../target", filepath.Join(workDir, "link")); err != nil {
					t.Fatalf("Symlink(link) error = %v", err)
				}
			},
			buildArgs: func(string) ([]string, []string) {
				return []string{"-L", "hit", "."}, []string{"-L", "hit", "."}
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
			runDir := workDir
			if tc.runDir != nil {
				runDir = tc.runDir(workDir)
			}
			gbash := runGBashRG(t, root, runDir, tc.stdin, tc.stdinTTY, gbashArgs...)
			ripgrep := runRipgrep(t, ripgrepPath, runDir, tc.stdin, ripgrepArgs...)
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

func runGBashRG(t testing.TB, root, workDir, stdin string, stdinTTY bool, args ...string) grepOracleResult {
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
		WorkDir: strings.TrimPrefix(workDir, root),
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
