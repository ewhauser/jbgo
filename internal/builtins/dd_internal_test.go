package builtins

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
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
