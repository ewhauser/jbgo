package interp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBuiltinPackedOptions(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
echo hi | { read -rn1 var; printf '%s\n' "$var"; }
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "h\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "h\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestReadBuiltinNulDelimiter(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
printf 'alpha\0beta' | {
  IFS= read -r -d '' first
  IFS= read -r second
  printf '%s|%s\n' "$first" "$second"
}
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "alpha|beta\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "alpha|beta\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestReadBuiltinNamedFD(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
exec {fd}<<'EOF'
left right
EOF
read -u "$fd" a b
printf '%s|%s\n' "$a" "$b"
exec {fd}<&-
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "left|right\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "left|right\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestReadBuiltinDirectoryReadError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatalf("Mkdir error = %v", err)
	}
	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			if !filepath.IsAbs(name) {
				name = filepath.Join(dir, name)
			}
			return os.OpenFile(name, flag, perm)
		},
	}, `
read x < ./dir
printf 'status=%d\n' "$?"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\n")
	}
	if stderr != "read: 0: read error: Is a directory\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "read: 0: read error: Is a directory\n")
	}
}

func TestReadBuiltinTimeoutStatus(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	pr, pw := runner.newPipe()
	defer pw.Close()
	runner.setStdinReader(pr)

	err = runner.runShellReader(context.Background(), strings.NewReader(`
read -t 0.01 var
printf 'status=%d\n' "$?"
`), "read-timeout-test.sh", nil)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout.String() != "status=142\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "status=142\n")
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestReadBuiltinPollNoDataStatus(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	pr, pw := runner.newPipe()
	defer pw.Close()
	runner.setStdinReader(pr)

	err = runner.runShellReader(context.Background(), strings.NewReader(`
read -t 0 var
printf 'status=%d\n' "$?"
`), "read-poll-test.sh", nil)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout.String() != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "status=1\n")
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
