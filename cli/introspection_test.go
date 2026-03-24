package cli

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ewhauser/gbash"
)

func TestRunFileScriptSetsScriptPathIntrospection(t *testing.T) {
	t.Parallel()

	rootDir := writeCLIRootScript(t, "main.sh", strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		`printf 'LINE:%s\n' "${BASH_LINENO[0]}"`,
		"",
	}, "\n"))

	exitCode, stdout, stderr, err := runCLI(t, []string{"--root", rootDir, "main.sh"}, "")
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
		"ZERO:main.sh",
		"SRC:main.sh",
		"LINE:0",
		"",
	}, "\n"); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunJSONFileScriptSetsScriptPathIntrospection(t *testing.T) {
	t.Parallel()

	rootDir := writeCLIRootScript(t, "main.sh", strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		"",
	}, "\n"))

	exitCode, stdout, stderr, err := runCLI(t, []string{"--root", rootDir, "--json", "main.sh"}, "")
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
		"ZERO:main.sh",
		"SRC:main.sh",
		"",
	}, "\n"); got != want {
		t.Fatalf("payload.Stdout = %q, want %q", got, want)
	}
}

func TestRunFileScriptRejectsNULBytes(t *testing.T) {
	t.Parallel()

	rootDir := writeCLIRootFile(t, "nul-script.sh", []byte("echo one \x00 echo two"))

	exitCode, stdout, stderr, err := runCLI(t, []string{"--root", rootDir, "nul-script.sh"}, "")
	if err == nil {
		t.Fatal("run() error = nil, want file execution failure")
	}
	if exitCode != 126 {
		t.Fatalf("exitCode = %d, want 126", exitCode)
	}
	if got, want := stdout, ""; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := err.Error(), "nul-script.sh: nul-script.sh: cannot execute binary file"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRunFileScriptDoesNotReadHostPathByDefault(t *testing.T) {
	t.Parallel()

	scriptPath := writeCLIScript(t, "echo host-only\n")

	exitCode, stdout, stderr, err := runCLI(t, []string{scriptPath}, "")
	if err == nil {
		t.Fatal("run() error = nil, want missing sandbox file error")
	}
	if exitCode != 127 {
		t.Fatalf("exitCode = %d, want 127", exitCode)
	}
	if got, want := stdout, ""; got != want {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got, want := err.Error(), scriptPath+": No such file or directory"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestRunFileScriptMapsAbsoluteHostPathWithinRoot(t *testing.T) {
	t.Parallel()

	rootDir := writeCLIRootScript(t, filepath.Join("nested", "main.sh"), strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		"",
	}, "\n"))
	scriptPath := filepath.Join(rootDir, "nested", "main.sh")
	sandboxPath := path.Join(gbash.DefaultWorkspaceMountPoint, "nested", "main.sh")

	exitCode, stdout, stderr, err := runCLI(t, []string{"--root", rootDir, scriptPath}, "")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if got, want := stdout, "ZERO:"+sandboxPath+"\nSRC:"+sandboxPath+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunFileScriptMapsAbsoluteHostPathWithinReadWriteRoot(t *testing.T) {
	t.Parallel()

	rootDir := writeCLIRootScript(t, filepath.Join("nested", "main.sh"), strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		"",
	}, "\n"))
	scriptPath := filepath.Join(rootDir, "nested", "main.sh")

	exitCode, stdout, stderr, err := runCLI(t, []string{"--readwrite-root", rootDir, scriptPath}, "")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if got, want := stdout, "ZERO:/nested/main.sh\nSRC:/nested/main.sh\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunFileScriptCopyScriptStagesHostPath(t *testing.T) {
	t.Parallel()

	scriptPath := writeCLIScript(t, strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		"",
	}, "\n"))

	exitCode, stdout, stderr, err := runCLI(t, []string{"--copy-script", scriptPath}, "")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if got, want := stdout, "ZERO:/tmp/.gbash-host-script/main.sh\nSRC:/tmp/.gbash-host-script/main.sh\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunFileScriptCopyScriptMissingHostFile(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(t.TempDir(), "missing.sh")

	exitCode, stdout, stderr, err := runCLI(t, []string{"--copy-script", scriptPath}, "")
	if err == nil {
		t.Fatal("run() error = nil, want missing host file error")
	}
	if exitCode != 127 {
		t.Fatalf("exitCode = %d, want 127", exitCode)
	}
	if got, want := stdout, ""; got != want {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got, want := err.Error(), scriptPath+": No such file or directory"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
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

	scriptPath := filepath.Join(t.TempDir(), "main.sh")
	if err := os.WriteFile(scriptPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}
	return scriptPath
}

func writeCLIRootScript(t *testing.T, name, contents string) string {
	t.Helper()

	return writeCLIRootFile(t, name, []byte(contents))
}

func writeCLIRootFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	root := t.TempDir()
	filePath := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
	}
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", filePath, err)
	}
	return root
}
