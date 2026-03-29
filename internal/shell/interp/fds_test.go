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

func TestShellFDSeekUsesLogicalCurrentOffsetAfterPeek(t *testing.T) {
	t.Parallel()

	fd := newShellInputFD(bytes.NewReader([]byte("abc")))

	peeked, err := fd.PeekByte()
	if err != nil {
		t.Fatalf("PeekByte() error = %v", err)
	}
	if got, want := peeked, byte('a'); got != want {
		t.Fatalf("PeekByte() = %q, want %q", got, want)
	}

	position, err := fd.Seek(1, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	if got, want := position, int64(1); got != want {
		t.Fatalf("Seek() position = %d, want %d", got, want)
	}
	if fd.hasBufferedByte() {
		t.Fatalf("buffered = true, want false")
	}

	next, err := fd.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte() error = %v", err)
	}
	if got, want := next, byte('b'); got != want {
		t.Fatalf("ReadByte() = %q, want %q", got, want)
	}
}

func TestSetStandardFDsDoesNotCloseOwnedDescriptorWhenRewiringStdout(t *testing.T) {
	t.Parallel()

	closer := &trackingCloser{}
	r := &Runner{
		stdout: &bytes.Buffer{},
		fds: map[int]*shellFD{
			1: {writer: &bytes.Buffer{}, closer: closer, owned: true},
		},
	}

	r.setStandardFDs(standardFDUpdate{stdout: &bytes.Buffer{}, setStdout: true})

	if got := closer.closes; got != 0 {
		t.Fatalf("closes = %d, want 0", got)
	}
}

func TestForkFDTableForExecSharesBufferedReadState(t *testing.T) {
	t.Parallel()

	parent := newShellInputFD(bytes.NewReader([]byte("ab")))

	peeked, err := parent.PeekByte()
	if err != nil {
		t.Fatalf("PeekByte() error = %v", err)
	}
	if got, want := peeked, byte('a'); got != want {
		t.Fatalf("PeekByte() = %q, want %q", got, want)
	}

	forked := forkFDTableForExec(map[int]*shellFD{3: parent})[3]

	got, err := forked.ReadByte()
	if err != nil {
		t.Fatalf("forked.ReadByte() error = %v", err)
	}
	if want := byte('a'); got != want {
		t.Fatalf("forked.ReadByte() = %q, want %q", got, want)
	}
	if parent.hasBufferedByte() {
		t.Fatalf("parent still has buffered byte after forked read")
	}

	got, err = parent.ReadByte()
	if err != nil {
		t.Fatalf("parent.ReadByte() error = %v", err)
	}
	if want := byte('b'); got != want {
		t.Fatalf("parent.ReadByte() = %q, want %q", got, want)
	}
}

func TestShellFDSeekAccountsForBufferedByte(t *testing.T) {
	t.Parallel()

	base := seekableTestStdin{Reader: bytes.NewReader([]byte("abcdef"))}
	fd := newShellInputFD(base)

	got, err := fd.PeekByte()
	if err != nil {
		t.Fatalf("PeekByte() error = %v", err)
	}
	if got != 'a' {
		t.Fatalf("PeekByte() = %q, want %q", got, 'a')
	}

	position, err := fd.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek(current) error = %v", err)
	}
	if position != 0 {
		t.Fatalf("Seek(current) = %d, want 0", position)
	}

	buf := make([]byte, 2)
	if n, err := fd.Read(buf); err != nil || n != 2 {
		t.Fatalf("Read() = (%d, %v), want (2, nil)", n, err)
	}
	if got := string(buf); got != "ab" {
		t.Fatalf("Read() = %q, want %q", got, "ab")
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

func TestDupRedirectToQuotedAtIsAmbiguous(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: "/tmp",
		Params: []string{
			"2 3",
			"c d",
		},
	}, `
echo hi 1>& "$@"
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\n")
	}
	if stderr != "\"$@\": ambiguous redirect\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "\"$@\": ambiguous redirect\n")
	}
}

func TestSelfDupRedirectOnClosedFDIsNoOp(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, ": 3>&3\n: 4<&4\necho hello\n")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "hello\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "hello\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDupRedirectRequiresCompatibleDescriptorMode(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "echo hi 1>&0\necho status=$?\n")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "status=1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "status=1\n")
	}
	if stderr != "0: Bad file descriptor\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "0: Bad file descriptor\n")
	}
}

func TestInputDupRedirectCanReuseOutputDescriptor(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, "echo one 1>&2\necho two 1<&2\n")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "one\ntwo\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "one\ntwo\n")
	}
}

func TestArithmeticCommandRedirectCoversCommandSubstitution(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	errFile := filepath.Join(dir, "arith.err")

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, fmt.Sprintf(`
emit_num() {
  echo 42
  echo STDERR >&2
}
(( a = $(emit_num) + 10 )) 2> %q
printf 'a=%%s\n' "$a"
printf '%%s\n' --
printf '%%s\n' "$(< %q)"
`, errFile, errFile))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "a=52\n--\nSTDERR\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "a=52\n--\nSTDERR\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestConditionalRedirectCoversCommandSubstitution(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	errFile := filepath.Join(dir, "cond.err")

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, fmt.Sprintf(`
emit_word() {
  echo STDOUT
  echo STDERR >&2
}
[[ $(emit_word) == STDOUT ]] 2> %q
printf '%%s\n' "$?"
printf '%%s\n' --
printf '%%s\n' "$(< %q)"
`, errFile, errFile))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "0\n--\nSTDERR\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "0\n--\nSTDERR\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestLoopRedirectCoversCommandSubstitution(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	errFile := filepath.Join(dir, "loop.err")

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, fmt.Sprintf(`
emit_item() {
  echo item
  echo LOOPERR >&2
}
for item in $(emit_item); do
  printf '%%s\n' "$item"
done 2> %q
printf '%%s\n' --
printf '%%s\n' "$(< %q)"
`, errFile, errFile))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "item\n--\nLOOPERR\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "item\n--\nLOOPERR\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNamedRedirectFDReusesClosedDescriptor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir: dir,
		OpenHandler: func(_ context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			return os.OpenFile(name, flag, perm)
		},
	}, fmt.Sprintf(`
exec {fd}> %q
printf 'first=%%s\n' "$fd"
exec {fd}>&-
exec {fd}> %q
printf 'second=%%s\n' "$fd"
exec {fd}>&-
`, first, second))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "first=10\nsecond=10\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "first=10\nsecond=10\n")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
