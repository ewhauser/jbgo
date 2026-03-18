package shell

import (
	"errors"
	"io"
	"sync"
)

// defaultPipeBufferSize is the default buffer size for virtual pipes.
// This matches typical Linux pipe buffer size for consistent behavior.
const defaultPipeBufferSize = 65536

// bufferedPipe is an in-memory pipe with internal buffering.
// Unlike io.Pipe which is synchronous, this allows writes to proceed
// without blocking until the buffer is full, matching OS pipe semantics.
type bufferedPipe struct {
	mu       sync.Mutex
	cond     *sync.Cond
	buf      []byte
	bufSize  int
	closed   bool
	wrClosed bool
	err      error
}

// bufferedPipeReader is the read end of a bufferedPipe.
type bufferedPipeReader struct {
	pipe *bufferedPipe
}

// bufferedPipeWriter is the write end of a bufferedPipe.
type bufferedPipeWriter struct {
	pipe *bufferedPipe
}

// newBufferedPipe creates a new buffered pipe with the default buffer size.
func newBufferedPipe() (*bufferedPipeReader, *bufferedPipeWriter) {
	return newBufferedPipeSize(defaultPipeBufferSize)
}

// newBufferedPipeSize creates a new buffered pipe with the specified buffer size.
func newBufferedPipeSize(size int) (*bufferedPipeReader, *bufferedPipeWriter) {
	if size <= 0 {
		size = defaultPipeBufferSize
	}
	p := &bufferedPipe{
		buf:     make([]byte, 0, size),
		bufSize: size,
	}
	p.cond = sync.NewCond(&p.mu)
	return &bufferedPipeReader{pipe: p}, &bufferedPipeWriter{pipe: p}
}

// Read reads data from the pipe. It blocks until data is available,
// the write end is closed, or the pipe is closed.
func (r *bufferedPipeReader) Read(p []byte) (int, error) {
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
		r.pipe.cond.Wait()
	}

	if r.pipe.closed {
		return 0, r.pipe.err
	}

	n := copy(p, r.pipe.buf)
	r.pipe.buf = r.pipe.buf[n:]
	r.pipe.cond.Broadcast()
	return n, nil
}

// Close closes the read end of the pipe. Subsequent writes will fail.
func (r *bufferedPipeReader) Close() error {
	return r.CloseWithError(errors.New("read end closed"))
}

// CloseWithError closes the read end with the given error.
// Subsequent writes will return this error.
func (r *bufferedPipeReader) CloseWithError(err error) error {
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
func (w *bufferedPipeWriter) Write(p []byte) (int, error) {
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
func (w *bufferedPipeWriter) Close() error {
	return w.CloseWithError(nil)
}

// CloseWithError closes the write end with the given error.
// If err is nil, subsequent reads return io.EOF after draining the buffer.
// If err is non-nil, subsequent reads return the error immediately.
func (w *bufferedPipeWriter) CloseWithError(err error) error {
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
