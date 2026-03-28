package builtins_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/testutil"
)

type grepOracleResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func TestGrepMatchesRipgrep(t *testing.T) {
	t.Parallel()
	ripgrepPath := testutil.RequireNixRipgrep(t)

	testCases := []struct {
		name      string
		stdin     string
		setup     func(t *testing.T, workDir string)
		buildArgs func(workDir string) (gbashArgs, ripgrepArgs []string)
	}{
		{
			name:  "stdin-literal",
			stdin: "alpha\nbeta\nalpha-two\n",
			buildArgs: func(string) ([]string, []string) {
				return []string{"alpha"}, []string{"alpha"}
			},
		},
		{
			name: "ignore-case-line-number",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "Warning\ninfo\nwarning\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-n", "-i", "warning", grepOracleGBashPath("input.txt")},
					[]string{"-n", "-i", "warning", grepOracleHostPath(workDir, "input.txt")}
			},
		},
		{
			name: "fixed-only-matching",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "alpha alpha\nbeta\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-F", "-o", "alpha", grepOracleGBashPath("input.txt")},
					[]string{"-F", "-o", "alpha", grepOracleHostPath(workDir, "input.txt")}
			},
		},
		{
			name: "word-regexp",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "an\nanother\nan an\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-w", "an", grepOracleGBashPath("input.txt")},
					[]string{"-w", "an", grepOracleHostPath(workDir, "input.txt")}
			},
		},
		{
			name: "line-regexp",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "beta\nalphabet\nbeta two\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-x", "beta", grepOracleGBashPath("input.txt")},
					[]string{"-x", "beta", grepOracleHostPath(workDir, "input.txt")}
			},
		},
		{
			name: "invert-match",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "keep\nskip\nalso keep\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-v", "skip", grepOracleGBashPath("input.txt")},
					[]string{"-v", "skip", grepOracleHostPath(workDir, "input.txt")}
			},
		},
		{
			name: "max-count",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "hit\nhit again\nhit\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-m", "1", "hit", grepOracleGBashPath("input.txt")},
					[]string{"-m", "1", "hit", grepOracleHostPath(workDir, "input.txt")}
			},
		},
		{
			name: "count",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "input.txt", "hit\nmiss\nhit\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-c", "hit", grepOracleGBashPath("input.txt")},
					[]string{"-c", "hit", grepOracleHostPath(workDir, "input.txt")}
			},
		},
		{
			name: "files-with-matches",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "a.txt", "hit\n")
				writeHostFile(t, workDir, "b.txt", "miss\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"-l", "hit", grepOracleGBashPath("a.txt"), grepOracleGBashPath("b.txt")},
					[]string{"-l", "hit", grepOracleHostPath(workDir, "a.txt"), grepOracleHostPath(workDir, "b.txt")}
			},
		},
		{
			name: "files-without-match",
			setup: func(t *testing.T, workDir string) {
				writeHostFile(t, workDir, "a.txt", "hit\n")
				writeHostFile(t, workDir, "b.txt", "miss\n")
			},
			buildArgs: func(workDir string) ([]string, []string) {
				return []string{"--files-without-match", "hit", grepOracleGBashPath("a.txt"), grepOracleGBashPath("b.txt")},
					[]string{"--files-without-match", "hit", grepOracleHostPath(workDir, "a.txt"), grepOracleHostPath(workDir, "b.txt")}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			workDir := filepath.Join(root, "work")
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				t.Fatalf("MkdirAll(%q) error = %v", workDir, err)
			}
			if tc.setup != nil {
				tc.setup(t, workDir)
			}

			gbashArgs, ripgrepArgs := tc.buildArgs(workDir)
			gbash := runGBashGrep(t, root, tc.stdin, gbashArgs...)
			ripgrep := runRipgrep(t, ripgrepPath, workDir, tc.stdin, ripgrepArgs...)

			if gbash != ripgrep {
				t.Fatalf("ripgrep mismatch\ngbash:   %+v\nripgrep: %+v", gbash, ripgrep)
			}
		})
	}
}

func runGBashGrep(t testing.TB, root, stdin string, args ...string) grepOracleResult {
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
	script.WriteString("grep")
	for _, arg := range args {
		script.WriteByte(' ')
		script.WriteString(diffShellQuote(arg))
	}
	script.WriteByte('\n')

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		WorkDir: "/work",
		Script:  script.String(),
		Stdin:   strings.NewReader(stdin),
	})
	if err != nil {
		t.Fatalf("gbash Run(%q) error = %v", script.String(), err)
	}

	return grepOracleResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}
}

func runRipgrep(t testing.TB, ripgrepPath, workDir, stdin string, args ...string) grepOracleResult {
	t.Helper()

	allArgs := append([]string{"--color=never", "--no-heading"}, args...)
	cmd := exec.CommandContext(context.Background(), ripgrepPath, allArgs...) //nolint:gosec // Test oracle runs the pinned Nix ripgrep binary.
	cmd.Args[0] = "rg"
	cmd.Dir = workDir
	cmd.Env = []string{
		"HOME=" + workDir,
		"PWD=" + workDir,
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"LANG=C",
		"TZ=UTC",
		"TMPDIR=" + workDir,
	}
	cmd.Stdin = strings.NewReader(stdin)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("ripgrep Run() error = %v", err)
		}
		exitCode = exitErr.ExitCode()
	}

	return grepOracleResult{
		ExitCode: exitCode,
		Stdout:   normalizeRipgrepPaths(stdout.String(), workDir),
		Stderr:   normalizeRipgrepPaths(stderr.String(), workDir),
	}
}

func grepOracleGBashPath(relPath string) string {
	return "/work/" + strings.TrimPrefix(relPath, "/")
}

func grepOracleHostPath(workDir, relPath string) string {
	return filepath.Join(workDir, filepath.FromSlash(relPath))
}

func normalizeRipgrepPaths(text, workDir string) string {
	return strings.ReplaceAll(text, filepath.ToSlash(workDir), "/work")
}
