package shell

import (
	"testing"
	"time"
)

func TestBufferedPipeWriteDoesNotBlockImmediately(t *testing.T) {
	t.Parallel()

	reader, writer := newBufferedPipe()
	defer func() { _ = reader.Close() }()

	// Small writes should not block - this is the key semantic difference
	// from io.Pipe which would block until someone reads.
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Write a small amount of data
		_, err := writer.Write([]byte("hello"))
		if err != nil {
			t.Errorf("Write failed: %v", err)
		}
		_ = writer.Close()
	}()

	select {
	case <-done:
		// Write completed without blocking - expected behavior
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Write blocked when it should have buffered")
	}

	// Now read the data
	buf := make([]byte, 10)
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("got %q, want %q", buf[:n], "hello")
	}
}

func TestBufferedPipeBlocksWhenFull(t *testing.T) {
	t.Parallel()

	reader, writer := newBufferedPipeSize(10) // Small buffer
	defer func() { _ = reader.Close() }()

	// Fill the buffer
	_, err := writer.Write([]byte("0123456789"))
	if err != nil {
		t.Fatalf("First write failed: %v", err)
	}

	// Next write should block
	blocked := make(chan struct{})
	go func() {
		_, _ = writer.Write([]byte("x"))
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("Write should have blocked when buffer is full")
	case <-time.After(50 * time.Millisecond):
		// Expected - write is blocked
	}

	// Read some data to unblock
	buf := make([]byte, 5)
	_, _ = reader.Read(buf)

	// Now the blocked write should complete
	select {
	case <-blocked:
		// Write completed after space was freed
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Write did not unblock after read")
	}

	_ = writer.Close()
}

func TestBufferedPipeCloseUnblocksReader(t *testing.T) {
	t.Parallel()

	reader, writer := newBufferedPipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 10)
		_, err := reader.Read(buf)
		// Should get EOF after writer closes
		if err == nil {
			t.Error("Read should have returned error after close")
		}
	}()

	// Give the goroutine time to block on read
	time.Sleep(10 * time.Millisecond)

	// Close writer should unblock reader with EOF
	_ = writer.Close()

	select {
	case <-done:
		// Read unblocked as expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Read did not unblock after writer close")
	}
}

func TestBufferedPipeCloseUnblocksWriter(t *testing.T) {
	t.Parallel()

	reader, writer := newBufferedPipeSize(10)

	// Fill buffer
	_, _ = writer.Write([]byte("0123456789"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := writer.Write([]byte("x"))
		// Should get error after reader closes
		if err == nil {
			t.Error("Write should have returned error after close")
		}
	}()

	// Give the goroutine time to block
	time.Sleep(10 * time.Millisecond)

	// Close reader should unblock writer with error
	_ = reader.Close()

	select {
	case <-done:
		// Write unblocked as expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Write did not unblock after reader close")
	}
}

func TestBufferedPipeWriterCloseUnblocksOwnWrite(t *testing.T) {
	t.Parallel()

	reader, writer := newBufferedPipeSize(10)
	defer func() { _ = reader.Close() }()

	// Fill buffer
	_, _ = writer.Write([]byte("0123456789"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := writer.Write([]byte("x"))
		// Should get ErrClosedPipe after writer closes
		if err == nil {
			t.Error("Write should have returned error after writer close")
		}
	}()

	// Give the goroutine time to block on full buffer
	time.Sleep(10 * time.Millisecond)

	// Close writer should unblock the blocked write
	_ = writer.Close()

	select {
	case <-done:
		// Write unblocked as expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Blocked write did not unblock after writer close")
	}
}
