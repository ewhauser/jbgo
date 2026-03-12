//go:build !windows

package compatrun

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/compatfs"
)

func TestRunnerExecSupportsNestedSubexecAndHostWorkdir(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	fsys, err := compatfs.New()
	if err != nil {
		t.Fatalf("compatfs.New() error = %v", err)
	}
	commandDir := makeCommandDir(t, tmp, []string{"printf", "env", "cat", "pwd"})

	runner, err := New(&Config{
		FS:                fsys,
		BaseEnv:           map[string]string{"HOME": tmp, "PATH": commandDir},
		DefaultDir:        tmp,
		BuiltinCommandDir: commandDir,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := runner.Exec(context.Background(), &commands.ExecutionRequest{
		Script:     "printf 'hello\\n' > note.txt\nenv cat note.txt\npwd\n",
		Env:        map[string]string{"HOME": tmp, "PATH": commandDir},
		ReplaceEnv: true,
		WorkDir:    tmp,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	wantStdout := "hello\n" + filepath.ToSlash(tmp) + "\n"
	if result.Stdout != wantStdout {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, wantStdout)
	}
}

func TestRunnerRunUtilityUsesStdinAndReturnsCommandFailures(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	fsys, err := compatfs.New()
	if err != nil {
		t.Fatalf("compatfs.New() error = %v", err)
	}
	commandDir := makeCommandDir(t, tmp, []string{"cat"})

	runner, err := New(&Config{
		FS:                fsys,
		BaseEnv:           map[string]string{"HOME": tmp, "PATH": commandDir},
		DefaultDir:        tmp,
		BuiltinCommandDir: commandDir,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := runner.RunUtility(context.Background(), "cat", nil, strings.NewReader("stdin-data"))
	if err != nil {
		t.Fatalf("RunUtility(cat) error = %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "stdin-data" {
		t.Fatalf("RunUtility(cat) = exit=%d stdout=%q", result.ExitCode, result.Stdout)
	}

	missing, err := runner.RunUtility(context.Background(), "missing-command", nil, nil)
	if err != nil {
		t.Fatalf("RunUtility(missing-command) error = %v", err)
	}
	if missing.ExitCode != 127 {
		t.Fatalf("missing-command exit = %d, want 127", missing.ExitCode)
	}
	if !strings.Contains(missing.Stderr, "missing-command: command not found") {
		t.Fatalf("missing-command stderr = %q", missing.Stderr)
	}
}

func makeCommandDir(t *testing.T, root string, names []string) string {
	t.Helper()
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("# compat command shim\n"), fs.FileMode(0o755)); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	return filepath.ToSlash(dir)
}
