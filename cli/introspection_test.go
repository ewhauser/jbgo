package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFileScriptSetsScriptPathIntrospection(t *testing.T) {
	t.Parallel()

	scriptPath := writeCLIScript(t, strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		`printf 'LINE:%s\n' "${BASH_LINENO[0]}"`,
		"",
	}, "\n"))

	exitCode, stdout, stderr, err := runCLI(t, []string{scriptPath}, "")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got, want := stdout, strings.Join([]string{
		"ZERO:" + scriptPath,
		"SRC:" + scriptPath,
		"LINE:0",
		"",
	}, "\n"); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunJSONFileScriptSetsScriptPathIntrospection(t *testing.T) {
	t.Parallel()

	scriptPath := writeCLIScript(t, strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		"",
	}, "\n"))

	exitCode, stdout, stderr, err := runCLI(t, []string{"--json", scriptPath}, "")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}

	var payload jsonExecutionResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("Unmarshal(stdout) error = %v; stdout=%q", err, stdout)
	}
	if payload.ExitCode != 0 {
		t.Fatalf("payload.ExitCode = %d, want 0", payload.ExitCode)
	}
	if got, want := payload.Stdout, strings.Join([]string{
		"ZERO:" + scriptPath,
		"SRC:" + scriptPath,
		"",
	}, "\n"); got != want {
		t.Fatalf("payload.Stdout = %q, want %q", got, want)
	}
}

func TestRunCommandStringLeavesBashSourceUnset(t *testing.T) {
	t.Parallel()

	exitCode, stdout, stderr, err := runCLI(t, []string{
		"-c",
		`set -u; printf 'ZERO:%s\n' "$0"; printf 'SRC:%s\n' "${BASH_SOURCE[0]-unset}"`,
		"inline-name",
	}, "")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got, want := stdout, "ZERO:inline-name\nSRC:unset\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunStdinScriptLeavesBashSourceUnset(t *testing.T) {
	t.Parallel()

	exitCode, stdout, stderr, err := runCLI(t, []string{"-s"}, `set -u; printf 'SRC:%s\n' "${BASH_SOURCE[0]-unset}"`+"\n")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got, want := stdout, "SRC:unset\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func runCLI(t *testing.T, args []string, stdin string) (exitCode int, stdoutText, stderrText string, err error) {
	t.Helper()

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode, err = run(context.Background(), Config{Name: "gbash"}, args, strings.NewReader(stdin), &stdout, &stderr, false)
	return exitCode, stdout.String(), stderr.String(), err
}

func writeCLIScript(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "main.sh")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
