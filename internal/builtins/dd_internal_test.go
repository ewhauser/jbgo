package builtins

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

type ddReadStep struct {
	data []byte
	err  error
}

type ddScriptedReader struct {
	steps []ddReadStep
	calls int
}

func (r *ddScriptedReader) Read(p []byte) (int, error) {
	if r.calls >= len(r.steps) {
		r.calls++
		return 0, io.EOF
	}
	step := r.steps[r.calls]
	r.calls++
	n := copy(p, step.data)
	return n, step.err
}

type ddLoopGuardReader struct {
	err      error
	maxCalls int
	calls    int
}

func (r *ddLoopGuardReader) Read(_ []byte) (int, error) {
	r.calls++
	if r.calls > r.maxCalls {
		panic("dd retried an unrecoverable read without making progress")
	}
	return 0, r.err
}

type ddCaptureWriter struct {
	data []byte
}

func (w *ddCaptureWriter) WriteData(data []byte) (ddWriteStats, error) {
	w.data = append(w.data, data...)
	return ddWriteStats{bytesTotal: uint64(len(data))}, nil
}

func (w *ddCaptureWriter) Flush() (ddWriteStats, error) {
	return ddWriteStats{}, nil
}

func (w *ddCaptureWriter) Sync() error {
	return nil
}

func (w *ddCaptureWriter) Finalize(context.Context, *Invocation) error {
	return nil
}

type ddEOFReader struct {
	sizes []int
}

func (r *ddEOFReader) Read(p []byte) (int, error) {
	r.sizes = append(r.sizes, len(p))
	return 0, io.EOF
}

type ddOpenFailFS struct {
	gbfs.FileSystem
	failPath string
	err      error
}

func (fs ddOpenFailFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	if name == fs.failPath {
		return nil, fs.err
	}
	return fs.FileSystem.Open(ctx, name)
}

func writeDdTestFile(tb testing.TB, fsys gbfs.FileSystem, name string, data []byte) {
	tb.Helper()

	if err := fsys.MkdirAll(context.Background(), "/tmp", 0o755); err != nil {
		tb.Fatalf("MkdirAll(/tmp) error = %v", err)
	}
	file, err := fsys.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		tb.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(data); err != nil {
		tb.Fatalf("Write(%q) error = %v", name, err)
	}
}

func TestRunDdWithIONoerrorPreservesPartialReadAndFails(t *testing.T) {
	t.Parallel()

	reader := &ddScriptedReader{
		steps: []ddReadStep{
			{data: []byte("abc"), err: errors.New("boom")},
			{err: io.EOF},
		},
	}
	writer := &ddCaptureWriter{}
	var stderr bytes.Buffer

	err := runDdWithIO(context.Background(), &Invocation{Stderr: &stderr}, &ddSettings{
		ibs:    4,
		obs:    4,
		status: ddStatusNone,
		iflags: ddInputFlags{
			fullblock: true,
		},
		conv: ddConvOptions{
			noerror: true,
		},
	}, &ddInput{reader: reader, label: "input"}, writer)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("runDdWithIO() error = %v, want exit status 1", err)
	}
	if got, want := string(writer.data), "abc"; got != want {
		t.Fatalf("captured output = %q, want %q", got, want)
	}
	if got := stderr.String(); !strings.Contains(got, "dd: error reading 'input': boom\n") {
		t.Fatalf("stderr = %q, want read warning", got)
	}
}

func TestRunDdWithIONoerrorStopsOnZeroByteReadError(t *testing.T) {
	t.Parallel()

	reader := &ddLoopGuardReader{err: errors.New("boom"), maxCalls: 1}
	writer := &ddCaptureWriter{}
	var stderr bytes.Buffer

	err := runDdWithIO(context.Background(), &Invocation{Stderr: &stderr}, &ddSettings{
		ibs:    4,
		obs:    4,
		status: ddStatusNone,
		conv: ddConvOptions{
			noerror: true,
		},
	}, &ddInput{reader: reader, label: "input"}, writer)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("runDdWithIO() error = %v, want exit status 1", err)
	}
	if len(writer.data) != 0 {
		t.Fatalf("captured output length = %d, want 0", len(writer.data))
	}
	if reader.calls != 1 {
		t.Fatalf("Read() calls = %d, want 1", reader.calls)
	}
	if got := stderr.String(); !strings.Contains(got, "dd: error reading 'input': boom\n") {
		t.Fatalf("stderr = %q, want read warning", got)
	}
}

func TestRunDdWithIONoerrorSyncPadsZeroByteReadError(t *testing.T) {
	t.Parallel()

	reader := &ddLoopGuardReader{err: errors.New("boom"), maxCalls: 1}
	writer := &ddCaptureWriter{}
	var stderr bytes.Buffer

	err := runDdWithIO(context.Background(), &Invocation{Stderr: &stderr}, &ddSettings{
		ibs:    4,
		obs:    4,
		status: ddStatusNone,
		count:  &ddNumber{value: 1},
		conv: ddConvOptions{
			noerror: true,
			sync:    true,
		},
	}, &ddInput{reader: reader, label: "input"}, writer)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("runDdWithIO() error = %v, want exit status 1", err)
	}
	if got, want := writer.data, []byte{0, 0, 0, 0}; !bytes.Equal(got, want) {
		t.Fatalf("captured output = %v, want %v", got, want)
	}
	if reader.calls != 1 {
		t.Fatalf("Read() calls = %d, want 1", reader.calls)
	}
	if got := stderr.String(); !strings.Contains(got, "dd: error reading 'input': boom\n") {
		t.Fatalf("stderr = %q, want read warning", got)
	}
}

func TestRunDdWithIONoerrorSyncContinuesUntilCountSatisfied(t *testing.T) {
	t.Parallel()

	reader := &ddLoopGuardReader{err: errors.New("boom"), maxCalls: 3}
	writer := &ddCaptureWriter{}
	var stderr bytes.Buffer

	err := runDdWithIO(context.Background(), &Invocation{Stderr: &stderr}, &ddSettings{
		ibs:    4,
		obs:    4,
		status: ddStatusNone,
		count:  &ddNumber{value: 3},
		conv: ddConvOptions{
			noerror: true,
			sync:    true,
		},
	}, &ddInput{reader: reader, label: "input"}, writer)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("runDdWithIO() error = %v, want exit status 1", err)
	}
	if got, want := writer.data, make([]byte, 12); !bytes.Equal(got, want) {
		t.Fatalf("captured output = %v, want %v", got, want)
	}
	if reader.calls != 3 {
		t.Fatalf("Read() calls = %d, want 3", reader.calls)
	}
	if got, want := strings.Count(stderr.String(), "dd: error reading 'input': boom\n"), 3; got != want {
		t.Fatalf("warning count = %d, want %d; stderr=%q", got, want, stderr.String())
	}
}

func TestReadDdBlockPreservesNonEOFErrorOnNonFullblockRead(t *testing.T) {
	t.Parallel()

	reader := &ddScriptedReader{
		steps: []ddReadStep{
			{data: []byte("abc"), err: errors.New("boom")},
		},
	}

	chunk, stats, eof, err := readDdBlock(reader, 4, false)
	if got, want := string(chunk), "abc"; got != want {
		t.Fatalf("chunk = %q, want %q", got, want)
	}
	if stats.recordsComplete != 0 || stats.recordsPartial != 1 || stats.bytesTotal != 3 {
		t.Fatalf("stats = %+v, want partial 3-byte record", stats)
	}
	if eof {
		t.Fatalf("eof = true, want false")
	}
	if got := err; got == nil || got.Error() != "boom" {
		t.Fatalf("err = %v, want boom", got)
	}
}

func TestRunDdWithIONoerrorSurfacesNonFullblockReadError(t *testing.T) {
	t.Parallel()

	reader := &ddScriptedReader{
		steps: []ddReadStep{
			{data: []byte("abc"), err: errors.New("boom")},
		},
	}
	writer := &ddCaptureWriter{}
	var stderr bytes.Buffer

	err := runDdWithIO(context.Background(), &Invocation{Stderr: &stderr}, &ddSettings{
		ibs:    4,
		obs:    4,
		status: ddStatusNone,
		conv: ddConvOptions{
			noerror: true,
		},
	}, &ddInput{reader: reader, label: "input"}, writer)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("runDdWithIO() error = %v, want exit status 1", err)
	}
	if got, want := string(writer.data), "abc"; got != want {
		t.Fatalf("captured output = %q, want %q", got, want)
	}
	if got := stderr.String(); !strings.Contains(got, "dd: error reading 'input': boom\n") {
		t.Fatalf("stderr = %q, want read warning", got)
	}
}

func TestDdDiscardClampsHugeSkipBeforeIntConversion(t *testing.T) {
	t.Parallel()

	reader := &ddEOFReader{}
	discarded, err := ddDiscard(reader, ^uint64(0))
	if err != nil {
		t.Fatalf("ddDiscard() error = %v", err)
	}
	if discarded != 0 {
		t.Fatalf("ddDiscard() discarded = %d, want 0", discarded)
	}
	if len(reader.sizes) != 1 {
		t.Fatalf("Read() calls = %d, want 1", len(reader.sizes))
	}
	if got, want := reader.sizes[0], 32*1024; got != want {
		t.Fatalf("Read() size = %d, want %d", got, want)
	}
}

func TestDdPrepareSwabChunkCarriesOddByteAcrossReads(t *testing.T) {
	t.Parallel()

	first, carry, hasCarry := ddPrepareSwabChunk([]byte("abc"), 0, false, false)
	if got, want := string(first), "ba"; got != want {
		t.Fatalf("first chunk = %q, want %q", got, want)
	}
	if !hasCarry || carry != 'c' {
		t.Fatalf("carry = (%v, %q), want (true, %q)", hasCarry, carry, byte('c'))
	}

	second, _, hasCarry := ddPrepareSwabChunk([]byte("de"), carry, hasCarry, true)
	if got, want := string(second), "dce"; got != want {
		t.Fatalf("second chunk = %q, want %q", got, want)
	}
	if hasCarry {
		t.Fatalf("hasCarry = true, want false")
	}
}

func TestDdScaledOffsetRejectsBlockOverflow(t *testing.T) {
	t.Parallel()

	_, err := ddScaledOffset(nil, "skip", ddNumber{value: math.MaxUint64/512 + 1}, 512)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("ddScaledOffset() error = %v, want exit status 1", err)
	}
	if got := err.Error(); !strings.Contains(got, "skip offset is too large") {
		t.Fatalf("error = %q, want overflow diagnostic", got)
	}
}

func TestOpenDdOutputFailsWhenExistingBytesCannotBeRead(t *testing.T) {
	t.Parallel()

	mem := gbfs.NewMemory()
	writeDdTestFile(t, mem, "/tmp/out.txt", []byte("abcdef"))

	inv := NewInvocation(&InvocationOptions{
		Cwd: "/",
		FileSystem: ddOpenFailFS{
			FileSystem: mem,
			failPath:   "/tmp/out.txt",
			err:        errors.New("boom"),
		},
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
		}),
	})

	_, err := openDdOutput(context.Background(), inv, &ddSettings{
		outfile:    "/tmp/out.txt",
		outfileSet: true,
		obs:        1,
		seek:       ddNumber{value: 1},
	})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("openDdOutput() error = %v, want exit status 1", err)
	}
	if got := err.Error(); !strings.Contains(got, "dd: error reading '/tmp/out.txt': boom") {
		t.Fatalf("error = %q, want read failure diagnostic", got)
	}
}

func TestDdFileWriterSkipZerosPreservesExistingData(t *testing.T) {
	t.Parallel()

	writer := &ddFileWriter{
		obs:    2,
		data:   []byte("abcdef"),
		cursor: 2,
	}

	stats, err := writer.SkipZeros(4)
	if err != nil {
		t.Fatalf("SkipZeros() error = %v", err)
	}
	if got, want := stats.recordsComplete, uint64(2); got != want {
		t.Fatalf("recordsComplete = %d, want %d", got, want)
	}
	if got, want := stats.bytesTotal, uint64(4); got != want {
		t.Fatalf("bytesTotal = %d, want %d", got, want)
	}
	if got, want := string(writer.data), "abcdef"; got != want {
		t.Fatalf("data = %q, want %q", got, want)
	}
	if got, want := writer.cursor, 6; got != want {
		t.Fatalf("cursor = %d, want %d", got, want)
	}
}
