package interp

import (
	"errors"
	"io"
	"testing"
	"time"
)

func testVirtualPipe(tb testing.TB, size int) (*virtualPipeReader, *virtualPipeWriter, *virtualPipe) {
	tb.Helper()

	pr, pw := newVirtualPipeSize(size)
	reader, ok := pr.(*virtualPipeReader)
	if !ok {
		tb.Fatalf("reader type = %T, want *virtualPipeReader", pr)
	}
	writer, ok := pw.(*virtualPipeWriter)
	if !ok {
		tb.Fatalf("writer type = %T, want *virtualPipeWriter", pw)
	}
	return reader, writer, reader.pipe
}

func waitForPipeState(tb testing.TB, pipe *virtualPipe, timeout time.Duration, check func(*virtualPipe) bool) {
	tb.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pipe.mu.Lock()
		ok := check(pipe)
		pipe.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	tb.Fatalf("timed out waiting for virtual pipe state")
}

func TestVirtualPipeLazyAllocation(t *testing.T) {
	t.Parallel()

	pr, pw, pipe := testVirtualPipe(t, defaultPipeBufferSize)
	defer pr.Close()
	defer pw.Close()

	if pipe.buf != nil {
		t.Fatalf("new pipe buf = %v, want nil", pipe.buf)
	}
	if pipe.readPos != 0 || pipe.writePos != 0 {
		t.Fatalf("new pipe positions = (%d, %d), want (0, 0)", pipe.readPos, pipe.writePos)
	}

	if _, err := pw.Write([]byte("hi")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if pipe.buf == nil {
		t.Fatal("pipe buffer not allocated on first write")
	}
	if got, want := len(pipe.buf), minVirtualPipeAllocSize; got != want {
		t.Fatalf("len(buf) = %d, want %d", got, want)
	}
	if !pipe.bufPooled {
		t.Fatal("expected default pipe to borrow its first buffer from the pool")
	}
	if pipe.readPos != 0 || pipe.writePos != 2 {
		t.Fatalf("pipe positions after first write = (%d, %d), want (0, 2)", pipe.readPos, pipe.writePos)
	}
}

func TestVirtualPipeBlocksAtConfiguredCapacity(t *testing.T) {
	t.Parallel()

	pr, pw, pipe := testVirtualPipe(t, 4)
	defer pr.Close()
	defer pw.Close()

	type writeResult struct {
		n   int
		err error
	}
	done := make(chan writeResult, 1)
	go func() {
		n, err := pw.Write([]byte("abcde"))
		done <- writeResult{n: n, err: err}
	}()

	waitForPipeState(t, pipe, time.Second, func(pipe *virtualPipe) bool {
		return pipe.unreadLen() == 4
	})

	select {
	case result := <-done:
		t.Fatalf("Write() completed early with (%d, %v), want blocked writer", result.n, result.err)
	default:
	}

	if got, want := len(pipe.buf), 4; got != want {
		t.Fatalf("len(buf) = %d, want %d", got, want)
	}
	if pipe.bufPooled {
		t.Fatal("small custom-capacity pipe should not use pooled buffers")
	}

	buf := make([]byte, 2)
	if n, err := pr.Read(buf); err != nil || n != 2 {
		t.Fatalf("Read() = (%d, %v), want (2, nil)", n, err)
	}
	if got, want := string(buf), "ab"; got != want {
		t.Fatalf("first read = %q, want %q", got, want)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Write() error = %v", result.err)
		}
		if got, want := result.n, 5; got != want {
			t.Fatalf("Write() n = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("Write() remained blocked after reader made space")
	}

	if err := pw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	rest, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(rest), "cde"; got != want {
		t.Fatalf("remaining data = %q, want %q", got, want)
	}
}

func TestVirtualPipeReaderCloseReleasesBuffer(t *testing.T) {
	t.Parallel()

	pr, pw, pipe := testVirtualPipe(t, defaultPipeBufferSize)
	defer pw.Close()

	if _, err := pw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if pipe.buf == nil {
		t.Fatal("pipe buffer not allocated before reader close")
	}

	if err := pr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if pipe.buf != nil {
		t.Fatalf("buf after reader close = %v, want nil", pipe.buf)
	}
	if pipe.readPos != 0 || pipe.writePos != 0 {
		t.Fatalf("positions after reader close = (%d, %d), want (0, 0)", pipe.readPos, pipe.writePos)
	}
	if pipe.bufPooled {
		t.Fatal("bufPooled should be cleared after releasing the buffer")
	}
	if _, err := pw.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Write() error = %v, want %v", err, io.ErrClosedPipe)
	}
}

func TestVirtualPipeWriterErrorCloseReleasesBuffer(t *testing.T) {
	t.Parallel()

	pr, pw, pipe := testVirtualPipe(t, defaultPipeBufferSize)
	defer pr.Close()

	if _, err := pw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	closeErr := io.ErrUnexpectedEOF
	if err := pw.CloseWithError(closeErr); !errors.Is(err, closeErr) && err != nil {
		t.Fatalf("CloseWithError() error = %v, want nil", err)
	}

	if pipe.buf != nil {
		t.Fatalf("buf after writer error close = %v, want nil", pipe.buf)
	}
	if pipe.readPos != 0 || pipe.writePos != 0 {
		t.Fatalf("positions after writer error close = (%d, %d), want (0, 0)", pipe.readPos, pipe.writePos)
	}

	buf := make([]byte, 8)
	if n, err := pr.Read(buf); n != 0 || !errors.Is(err, closeErr) {
		t.Fatalf("Read() = (%d, %v), want (0, %v)", n, err, closeErr)
	}
}

func TestVirtualPipeDrainReleasesBuffer(t *testing.T) {
	t.Parallel()

	pr, pw, pipe := testVirtualPipe(t, defaultPipeBufferSize)
	defer pr.Close()

	if _, err := pw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got, want := string(data), "hello"; got != want {
		t.Fatalf("ReadAll() = %q, want %q", got, want)
	}

	if pipe.buf != nil {
		t.Fatalf("buf after draining closed pipe = %v, want nil", pipe.buf)
	}
	if pipe.readPos != 0 || pipe.writePos != 0 {
		t.Fatalf("positions after draining closed pipe = (%d, %d), want (0, 0)", pipe.readPos, pipe.writePos)
	}
	if pipe.bufPooled {
		t.Fatal("bufPooled should be cleared after draining and releasing the buffer")
	}
}
