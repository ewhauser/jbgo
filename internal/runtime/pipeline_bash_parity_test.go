package runtime

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPipelineStateMatchesBashBehavior(t *testing.T) {
	t.Parallel()
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	testCases := []struct {
		name   string
		script string
	}{
		{
			name: "piped while loop mutations stay isolated",
			script: "" +
				"count=0\n" +
				"printf '%s\\n' alpha beta gamma | while IFS= read -r _; do count=$((count + 1)); done\n" +
				"printf 'after-pipeline:%s\\n' \"$count\"\n" +
				"while IFS= read -r _; do count=$((count + 1)); done <<'EOF'\n" +
				"delta\n" +
				"epsilon\n" +
				"EOF\n" +
				"printf 'after-heredoc:%s\\n' \"$count\"\n",
		},
		{
			name: "final stage read does not persist variable",
			script: "" +
				"unset value\n" +
				"printf 'hello\\n' | read -r value\n" +
				"printf 'value:<%s>\\n' \"${value-}\"\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gbash := runPipelineParityGBash(t, tc.script)
			bash := runPipelineParityBash(t, bashPath, tc.script)

			if gbash.ExitCode != bash.ExitCode || gbash.Stdout != bash.Stdout || gbash.Stderr != bash.Stderr {
				t.Fatalf(
					"pipeline parity mismatch\nscript:\n%s\n\ngbash:\n%s\n\nbash:\n%s",
					tc.script,
					mustJSON(t, gbash),
					mustJSON(t, bash),
				)
			}
		})
	}
}

func runPipelineParityGBash(t testing.TB, script string) normalizedExecutionResult {
	t.Helper()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	return normalizedExecutionResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}
}

func runPipelineParityBash(t testing.TB, bashPath, script string) normalizedExecutionResult {
	t.Helper()

	homeDir := filepath.ToSlash(t.TempDir())
	cmd := exec.Command(bashPath, "--noprofile", "--norc", "-c", "cd \"$HOME\"\n"+script) //nolint:noctx // test oracle runs real bash
	cmd.Env = []string{
		"HOME=" + homeDir,
		"PWD=" + homeDir,
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"LANG=C",
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("bash Run() error = %v", err)
		}
		exitCode = exitErr.ExitCode()
	}

	return normalizedExecutionResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stdoutlessBashDiagnostic(stderr.String(), homeDir),
	}
}

func stdoutlessBashDiagnostic(value, homeDir string) string {
	return bashLinePrefixPattern.ReplaceAllString(filepath.ToSlash(value), "")
}
