package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func sourceTestOpenHandler(ctx context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	fullPath := absPath(mustHandlerCtx(ctx).Dir, name)
	if info, err := os.Stat(fullPath); err == nil && info.IsDir() {
		return nil, &os.PathError{Op: "open", Path: name, Err: errors.New("is a directory")}
	}
	f, err := os.OpenFile(fullPath, flag, perm)
	if err == nil {
		return f, nil
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return nil, &os.PathError{Op: pathErr.Op, Path: name, Err: pathErr.Err}
	}
	return nil, err
}

func TestEvalRejectsInvalidOption(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
eval -z
printf 'invalid=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "invalid=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "eval: -z: invalid option\neval: usage: eval [arg ...]\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestEvalSyntaxErrorReturnsStatusTwo(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
eval 'echo >'
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "syntax error near unexpected token `newline'\n`echo >'\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestSourceIgnoresLeadingDoubleDash(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(scriptPath, []byte("echo sourced\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:         dir,
		OpenHandler: sourceTestOpenHandler,
	}, fmt.Sprintf("source -- %q\n", scriptPath))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "sourced\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSourceRequiresFilename(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
source
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = "source: filename argument required\nsource: usage: source [-p path] filename [arguments]\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestDotDirectoryErrorMatchesBash(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatalf("Mkdir error = %v", err)
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:         dir,
		OpenHandler: sourceTestOpenHandler,
	}, `
. ./dir/
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	const wantStderr = ".: ./dir/: is a directory\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestSourceSyntaxErrorReturnsStatusTwo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "syntax-error.sh")
	if err := os.WriteFile(scriptPath, []byte("echo >\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:         dir,
		OpenHandler: sourceTestOpenHandler,
	}, fmt.Sprintf(". %q\nprintf 'status=%%d\\n' \"$?\"\n", scriptPath))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "status=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	wantStderr := fmt.Sprintf("%s: line 1: syntax error near unexpected token `newline'\n%s: line 1: `echo >'\n", scriptPath, scriptPath)
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
