package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/testutil"
)

func TestShellScriptsMatchBashBehavior(t *testing.T) {
	t.Parallel()
	bashPath := testutil.RequireNixBashOrSkip(t)

	scripts := loadBashCompatScripts(t)
	// Keep each allowlisted divergence in its own single-purpose fixture so a
	// known gap never suppresses unrelated parity checks from the same script.
	knownDivergences := map[string]string{
		"11-bitwise-hex-arithmetic.sh": "hex literals (0xFF) and bitwise operators in arithmetic expansion do not evaluate correctly",
		"12-herestring-whitespace.sh":  "read from a here-string does not strip leading/trailing IFS whitespace the way Bash does",
		"13-sparse-array-unset.sh":     "unsetting individual array indices does not create sparse arrays; the array remains densely indexed",
	}

	for _, scriptPath := range scripts {
		name := filepath.Base(scriptPath)

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scriptBytes, err := os.ReadFile(scriptPath)
			if err != nil {
				t.Fatalf("ReadFile(%q) error = %v", scriptPath, err)
			}

			workDir := t.TempDir()
			gbash := runBashCompatGBash(t, workDir, string(scriptBytes))
			bash := runBashCompatBash(t, bashPath, workDir, scriptPath)

			if reflect.DeepEqual(gbash, bash) {
				if reason, ok := knownDivergences[name]; ok {
					t.Fatalf("known Bash divergence resolved for %s; remove allowlist entry: %s", name, reason)
				}
				return
			}

			if reason, ok := knownDivergences[name]; ok {
				t.Skipf(
					"known Bash divergence: %s\nscript:\n%s\n\ngbash:\n%s\n\nbash:\n%s",
					reason,
					string(scriptBytes),
					mustJSON(t, gbash),
					mustJSON(t, bash),
				)
			}

			t.Fatalf(
				"bash parity mismatch\nscript:\n%s\n\ngbash:\n%s\n\nbash:\n%s",
				string(scriptBytes),
				mustJSON(t, gbash),
				mustJSON(t, bash),
			)
		})
	}
}

func loadBashCompatScripts(t testing.TB) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join("testdata", "bash-compat", "*.sh"))
	if err != nil {
		t.Fatalf("Glob(bash-compat) error = %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("Glob(bash-compat) returned no fixtures")
	}
	sort.Strings(matches)
	return matches
}

func runBashCompatGBash(t testing.TB, workDir, script string) normalizedExecutionResult {
	t.Helper()

	session := newSession(t, &Config{
		BaseEnv: map[string]string{
			"HOME": "/",
			"PATH": "/bin",
		},
		FileSystem: ReadWriteDirectoryFileSystem(workDir, ReadWriteDirectoryOptions{}),
	})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: script,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	return normalizeBashCompatResult(normalizedExecutionResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}, workDir)
}

func runBashCompatBash(t testing.TB, bashPath, workDir, scriptPath string) normalizedExecutionResult {
	t.Helper()

	absScriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		t.Fatalf("Abs(%q) error = %v", scriptPath, err)
	}

	cmd := exec.Command(bashPath, "--noprofile", "--norc", absScriptPath) //nolint:noctx // test oracle runs real bash
	cmd.Dir = workDir
	cmd.Env = []string{
		"HOME=" + workDir,
		"PWD=" + workDir,
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"LANG=C",
		"TMPDIR=" + workDir,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("bash Run() error = %v", err)
		}
		exitCode = exitErr.ExitCode()
	}

	return normalizeBashCompatResult(normalizedExecutionResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, workDir)
}

func normalizeBashCompatResult(result normalizedExecutionResult, workDir string) normalizedExecutionResult {
	result.Stdout = normalizeBashCompatOutput(result.Stdout, workDir)
	result.Stderr = normalizeBashCompatOutput(result.Stderr, workDir)
	return result
}

func normalizeBashCompatOutput(value, workDir string) string {
	workDir = filepath.ToSlash(workDir)
	value = filepath.ToSlash(value)
	value = strings.ReplaceAll(value, workDir, "/")
	return value
}
