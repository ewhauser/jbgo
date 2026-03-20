package interp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestMapfileBuiltinNulDelimiter(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
printf 'alpha\0beta\0' | {
  mapfile -d '' arr
  printf '%d\n' "${#arr[@]}"
  printf '<%s>\n' "${arr[@]}"
}
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "2\n<alpha>\n<beta>\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "2\n<alpha>\n<beta>\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMapfileBuiltinTruncatesEmbeddedNul(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
{
  printf '.\0.\n'
  printf '.\0.\n'
} | {
  mapfile lines
  printf 'len=%d\n' "${#lines[@]}"
  printf '<%s>\n' "${lines[@]}"
}
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "len=2\n<.>\n<.>\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "len=2\n<.>\n<.>\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMapfileBuiltinOriginPreservesSparseArray(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
arr=(x y z)
mapfile -O 5 -t arr <<'EOF'
a0
a1
a2
EOF
printf '%s|%s|%s|%s|%s|%s|%s|%s|%d\n' "${arr[0]-}" "${arr[1]-}" "${arr[2]-}" "${arr[3]-unset}" "${arr[4]-unset}" "${arr[5]-unset}" "${arr[6]-unset}" "${arr[7]-unset}" "${#arr[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "x|y|z|unset|unset|a0|a1|a2|6\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "x|y|z|unset|unset|a0|a1|a2|6\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMapfileBuiltinSkipAndLimit(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
mapfile -s 2 -n 2 -t arr <<'EOF'
a0
a1
a2
a3
a4
EOF
printf '%s|%s|%d\n' "${arr[0]-}" "${arr[1]-}" "${#arr[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "a2|a3|2\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "a2|a3|2\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMapfileBuiltinNamedFD(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
exec {fd}<<'EOF'
red
blue
EOF
mapfile -u "$fd" -t arr
printf '%s|%s|%d\n' "${arr[0]-}" "${arr[1]-}" "${#arr[@]}"
exec {fd}<&-
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "red|blue|2\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "red|blue|2\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMapfileBuiltinDirectoryReadMatchesBash(t *testing.T) {
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
arr=(keep)
mapfile arr < ./dir
printf 'status=%d\n' "$?"
declare -p arr
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=0\ndeclare -a arr=()\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=0\ndeclare -a arr=()\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
