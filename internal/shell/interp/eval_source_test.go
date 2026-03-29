package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestTopLevelControlFlowDiagnosticsIncludeFileAndLine(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(t.TempDir(), "top-level-control-flow.sh")
	script := "break\ncontinue\nreturn\n"

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    filepath.Dir(scriptPath),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	err = runner.runShellReader(context.Background(), strings.NewReader(script), scriptPath, nil)
	if status, ok := err.(ExitStatus); !ok || status != 2 {
		t.Fatalf("runShellReader() error = %v, want exit status 2", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	wantStderr := "" +
		fmt.Sprintf("%s: line 1: break: only meaningful in a `for', `while', or `until' loop\n", scriptPath) +
		fmt.Sprintf("%s: line 2: continue: only meaningful in a `for', `while', or `until' loop\n", scriptPath) +
		fmt.Sprintf("%s: line 3: return: can only `return' from a function or sourced script\n", scriptPath)
	if got := stderr.String(); got != wantStderr {
		t.Fatalf("stderr = %q, want %q", got, wantStderr)
	}
}

func TestEvalPrefixAssignmentsConsumeNestedLocals(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
unlocal() { unset -v "$1"; }

f1() {
  local v=local1
  echo "nested:${v-(unset)}"
  v=tempenv2 eval '
    echo "temp:${v-(unset)}"
    local v=local2
    echo "shadow:${v-(unset)}"
  '
  echo "after:${v-(unset)}"
}

f2() {
  local v=local1
  v=tempenv2 eval '
    local v=local2
    (unset v; echo "unset:${v-(unset)}")
    (unlocal v; echo "unlocal:${v-(unset)}")
  '
}

f3() {
  local v=local1
  v=tempenv2 eval '
    local v=local2
    v=tempenv3 eval "
      local v=local3
      echo \"deep:\${v-(unset)}\"
      unlocal v
      echo \"deep1:\${v-(unset)}\"
      unlocal v
      echo \"deep2:\${v-(unset)}\"
      unlocal v
      echo \"deep3:\${v-(unset)}\"
      unlocal v
      echo \"deep4:\${v-(unset)}\"
    "
  '
}

v=global
v=tempenv1 f1
v=global
v=tempenv1 f2
v=global
v=tempenv1 f3
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"nested:local1\n" +
		"temp:tempenv2\n" +
		"shadow:local2\n" +
		"after:local1\n" +
		"unset:(unset)\n" +
		"unlocal:local1\n" +
		"deep:local3\n" +
		"deep1:local2\n" +
		"deep2:local1\n" +
		"deep3:global\n" +
		"deep4:(unset)\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSourcePrefixAssignmentsConsumeNestedLocals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "consume-locals.sh")
	script := "echo \"temp:${v-(unset)}\"\nlocal v=local2\necho \"shadow:${v-(unset)}\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:         dir,
		OpenHandler: sourceTestOpenHandler,
	}, fmt.Sprintf(`
f() {
  local v=local1
  v=tempenv2 source %q
  echo "after:${v-(unset)}"
}

v=global
v=tempenv1 f
`, scriptPath))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "" +
		"temp:tempenv2\n" +
		"shadow:local2\n" +
		"after:local1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
