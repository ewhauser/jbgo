package fs

import (
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"testing"
	"time"
)

func TestMemoryFIFOReaderOpenedWhileWriterActiveSeesEOFAfterWriterCloses(t *testing.T) {
	t.Parallel()

	mem := NewMemory()
	fifo := newMemoryFIFO()
	writer := newMemoryFIFOFile(mem, "/pipe", fifo, os.O_WRONLY)
	reader := newMemoryFIFOFile(mem, "/pipe", fifo, os.O_RDONLY)
	defer func() { _ = reader.Close() }()

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	buf := make([]byte, 1)
	n, err := reader.Read(buf)
	if n != 0 {
		t.Fatalf("reader.Read() bytes = %d, want 0", n)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("reader.Read() error = %v, want EOF", err)
	}
}

func TestMemoryFIFOReadUnblocksOnClose(t *testing.T) {
	t.Parallel()

	mem := NewMemory()
	reader := newMemoryFIFOFile(mem, "/pipe", newMemoryFIFO(), os.O_RDONLY)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := reader.Read(buf)
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	if err := reader.Close(); err != nil {
		t.Fatalf("reader.Close() error = %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, stdfs.ErrClosed) {
			t.Fatalf("reader.Read() error = %v, want closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader.Read() remained blocked after Close()")
	}
}

func TestMemoryFIFOWriteUnblocksOnClose(t *testing.T) {
	t.Parallel()

	mem := NewMemory()
	writer := newMemoryFIFOFile(mem, "/pipe", newMemoryFIFO(), os.O_WRONLY)

	done := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("x"))
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, stdfs.ErrClosed) {
			t.Fatalf("writer.Write() error = %v, want closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer.Write() remained blocked after Close()")
	}
}
