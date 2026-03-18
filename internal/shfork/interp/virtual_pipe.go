package interp

import (
	"io"
	"os"
	"sync"
	"time"
)

// StdinReader is the interface for standard input in the interpreter.
// It supports reading, closing, and deadline-based cancellation for the read builtin.
type StdinReader interface {
	io.ReadCloser
	SetReadDeadline(t time.Time) error
}

// Ensure *os.File implements StdinReader (it does via net.Conn-like interface).
var _ StdinReader = (*os.File)(nil)

// Ensure *VirtualPipeReader implements StdinReader.
var _ StdinReader = (*VirtualPipeReader)(nil)

// defaultPipeBufferSize is the default buffer size for virtual pipes.
// This matches typical Linux pipe buffer size for consistent behavior.
const defaultPipeBufferSize = 65536

// errDeadlineExceeded is returned when a read deadline is exceeded.
// This matches the behavior of os.File.SetReadDeadline.
var errDeadlineExceeded = os.ErrDeadlineExceeded

// virtualPipe is an in-memory pipe with internal buffering.
// Unlike io.Pipe which is synchronous, this allows writes to proceed
// without blocking until the buffer is full, matching OS pipe semantics.
type virtualPipe struct {
	mu           sync.Mutex
	cond         *sync.Cond
	buf          []byte
	bufSize      int
	closed       bool
	wrClosed     bool
	err          error
	readDeadline time.Time
}

// VirtualPipeReader is the read end of a virtualPipe.
// It implements io.ReadCloser and supports SetReadDeadline for
// compatibility with context cancellation in the read builtin.
type VirtualPipeReader struct {
	pipe *virtualPipe
}

// VirtualPipeWriter is the write end of a virtualPipe.
type VirtualPipeWriter struct {
	pipe *virtualPipe
}

// checkDeadline checks if the read deadline has been exceeded.
// Must be called with the mutex held.
func (p *virtualPipe) checkDeadline() error {
	if !p.readDeadline.IsZero() && time.Now().After(p.readDeadline) {
		return errDeadlineExceeded
	}
	return nil
}

// NewVirtualPipe creates a new buffered virtual pipe with the default buffer size.
func NewVirtualPipe() (*VirtualPipeReader, *VirtualPipeWriter) {
	return NewVirtualPipeSize(defaultPipeBufferSize)
}

// NewVirtualPipeSize creates a new buffered virtual pipe with the specified buffer size.
func NewVirtualPipeSize(size int) (*VirtualPipeReader, *VirtualPipeWriter) {
	if size <= 0 {
		size = defaultPipeBufferSize
	}
	p := &virtualPipe{
		buf:     make([]byte, 0, size),
		bufSize: size,
	}
	p.cond = sync.NewCond(&p.mu)
	return &VirtualPipeReader{pipe: p}, &VirtualPipeWriter{pipe: p}
}

// Read reads data from the pipe. It blocks until data is available,
// the write end is closed, the pipe is closed, or the read deadline is exceeded.
func (r *VirtualPipeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	r.pipe.mu.Lock()
	defer r.pipe.mu.Unlock()

	for len(r.pipe.buf) == 0 {
		if r.pipe.closed {
			return 0, r.pipe.err
		}
		if r.pipe.wrClosed {
			return 0, io.EOF
		}
		if err := r.pipe.checkDeadline(); err != nil {
			return 0, err
		}
		r.pipe.cond.Wait()
	}

	if r.pipe.closed {
		return 0, r.pipe.err
	}
	if err := r.pipe.checkDeadline(); err != nil {
		return 0, err
	}

	n := copy(p, r.pipe.buf)
	r.pipe.buf = r.pipe.buf[n:]
	r.pipe.cond.Broadcast()
	return n, nil
}

// SetReadDeadline sets the deadline for future Read calls.
// A zero value for t means Read will not time out.
// This is compatible with the os.File.SetReadDeadline interface.
func (r *VirtualPipeReader) SetReadDeadline(t time.Time) error {
	r.pipe.mu.Lock()
	r.pipe.readDeadline = t
	r.pipe.cond.Broadcast() // Wake up any blocked readers to check deadline
	r.pipe.mu.Unlock()

	// If deadline is in the future, schedule a wake-up
	if !t.IsZero() && t.After(time.Now()) {
		go func() {
			time.Sleep(time.Until(t))
			r.pipe.mu.Lock()
			r.pipe.cond.Broadcast()
			r.pipe.mu.Unlock()
		}()
	}
	return nil
}

// Close closes the read end of the pipe. Subsequent writes will fail
// with io.ErrClosedPipe (broken pipe semantics).
func (r *VirtualPipeReader) Close() error {
	return r.CloseWithError(io.ErrClosedPipe)
}

// CloseWithError closes the read end with the given error.
// Subsequent writes will return this error.
func (r *VirtualPipeReader) CloseWithError(err error) error {
	r.pipe.mu.Lock()
	defer r.pipe.mu.Unlock()

	if r.pipe.closed {
		return nil
	}
	r.pipe.closed = true
	if err == nil {
		err = io.ErrClosedPipe
	}
	r.pipe.err = err
	r.pipe.cond.Broadcast()
	return nil
}

// Write writes data to the pipe. It blocks if the buffer is full
// until space is available or the pipe is closed.
func (w *VirtualPipeWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.pipe.mu.Lock()
	defer w.pipe.mu.Unlock()

	written := 0
	for written < len(p) {
		if w.pipe.closed {
			if written > 0 {
				return written, w.pipe.err
			}
			return 0, w.pipe.err
		}
		if w.pipe.wrClosed {
			return written, io.ErrClosedPipe
		}

		// Wait for space in the buffer
		for len(w.pipe.buf) >= w.pipe.bufSize {
			if w.pipe.closed {
				return written, w.pipe.err
			}
			if w.pipe.wrClosed {
				return written, io.ErrClosedPipe
			}
			w.pipe.cond.Wait()
		}

		if w.pipe.closed {
			return written, w.pipe.err
		}

		// Write as much as we can
		space := w.pipe.bufSize - len(w.pipe.buf)
		toWrite := min(len(p)-written, space)
		w.pipe.buf = append(w.pipe.buf, p[written:written+toWrite]...)
		written += toWrite
		w.pipe.cond.Broadcast()
	}

	return written, nil
}

// Close closes the write end of the pipe. Subsequent reads will
// return io.EOF once the buffer is drained.
func (w *VirtualPipeWriter) Close() error {
	return w.CloseWithError(nil)
}

// CloseWithError closes the write end with the given error.
// If err is nil, subsequent reads return io.EOF after draining the buffer.
// If err is non-nil, subsequent reads return the error immediately.
func (w *VirtualPipeWriter) CloseWithError(err error) error {
	w.pipe.mu.Lock()
	defer w.pipe.mu.Unlock()

	if w.pipe.wrClosed {
		return nil
	}
	w.pipe.wrClosed = true
	if err != nil {
		w.pipe.closed = true
		w.pipe.err = err
	}
	w.pipe.cond.Broadcast()
	return nil
}
