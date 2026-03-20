package interp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type trackingCloser struct {
	closes int
}

func (c *trackingCloser) Close() error {
	c.closes++
	return nil
}

func TestSetFDClosesOwnedDescriptorWhenRemoved(t *testing.T) {
	t.Parallel()

	closer := &trackingCloser{}
	r := &Runner{
		fds: map[int]*shellFD{
			5: {closer: closer, owned: true},
		},
	}

	r.setFD(5, nil)

	if got := closer.closes; got != 1 {
		t.Fatalf("closes = %d, want 1", got)
	}
}

func TestSetFDPreservesSharedDescriptorUntilLastReference(t *testing.T) {
	t.Parallel()

	closer := &trackingCloser{}
	shared := &shellFD{closer: closer, owned: true}
	r := &Runner{
		fds: map[int]*shellFD{
			5: shared,
			6: shared,
		},
	}

	r.setFD(5, nil)
	if got := closer.closes; got != 0 {
		t.Fatalf("closes after first delete = %d, want 0", got)
	}

	r.setFD(6, nil)
	if got := closer.closes; got != 1 {
		t.Fatalf("closes after second delete = %d, want 1", got)
	}
}

func TestSetFDDoesNotCloseNonOwnedStandardDescriptors(t *testing.T) {
	t.Parallel()

	closer := &trackingCloser{}
	r := &Runner{
		stdout: &bytes.Buffer{},
		fds: map[int]*shellFD{
			1: {writer: &bytes.Buffer{}, closer: closer},
		},
	}

	r.setFD(1, newShellOutputFD(&bytes.Buffer{}))

	if got := closer.closes; got != 0 {
		t.Fatalf("closes = %d, want 0", got)
	}
}

func TestNestedStdoutRedirectRestoresOuterDescriptor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	inner := filepath.Join(dir, "inner.txt")
	outer := filepath.Join(dir, "outer.txt")

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, fmt.Sprintf(`
inner() {
  echo i1
  echo i2
}
outer() {
  echo o1
  inner > %q
  echo o2
}
outer > %q
printf '%%s\n' "$(< %q)"
echo --
printf '%%s\n' "$(< %q)"
`, inner, outer, inner, outer))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	const wantStdout = "i1\ni2\n--\no1\no2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestInnerDupRedirectDoesNotLoseOuterFileRedirect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outFile := filepath.Join(dir, "block.txt")

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, fmt.Sprintf(`
{ echo foo52 1>&2; echo 012345789; } > %q
IFS= read -r line < %q
printf '%%d\n' "$(( ${#line} + 1 ))"
`, outFile, outFile))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "10\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "10\n")
	}
	if stderr != "foo52\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "foo52\n")
	}
}

func TestRedirectToEmptyStringUsesBashStyleDiagnostic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, `
echo hi > ''
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\n")
	}
	if stderr != ": No such file or directory\n" {
		t.Fatalf("stderr = %q, want %q", stderr, ": No such file or directory\n")
	}
}

func TestBraceExpandedFileRedirectIsAmbiguous(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, `
echo hi > a-{one,two}
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\n")
	}
	if stderr != "a-{one,two}: ambiguous redirect\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "a-{one,two}: ambiguous redirect\n")
	}
}

func TestDupRedirectTreatsGlobWordLiterally(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tenPath := filepath.Join(dir, "10")
	literalPath := filepath.Join(dir, "1*")
	if err := os.WriteFile(tenPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", tenPath, err)
	}

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
		ReadDirHandler: func(_ context.Context, name string) ([]os.DirEntry, error) {
			return os.ReadDir(name)
		},
	}, fmt.Sprintf(`
echo should-not-be-on-stdout >& %s
printf 'ten=%%s\n' "$(< %q)"
printf 'literal=%%s\n' "$(< %q)"
`, literalPath, tenPath, literalPath))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "ten=\nliteral=should-not-be-on-stdout\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "ten=\nliteral=should-not-be-on-stdout\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
