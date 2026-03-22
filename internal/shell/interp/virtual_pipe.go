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

// Ensure *virtualPipeReader implements StdinReader.
var _ StdinReader = (*virtualPipeReader)(nil)

// defaultPipeBufferSize is the default buffer size for virtual pipes.
// This matches typical Linux pipe buffer size for consistent behavior.
const defaultPipeBufferSize = 65536

const minVirtualPipeAllocSize = 4 * 1024

var virtualPipeBufferBuckets = []int{
	4 * 1024,
	8 * 1024,
	16 * 1024,
	32 * 1024,
	64 * 1024,
}

var virtualPipeBufferPools = map[int]*sync.Pool{
	4 * 1024:  newVirtualPipeBufferPool(4 * 1024),
	8 * 1024:  newVirtualPipeBufferPool(8 * 1024),
	16 * 1024: newVirtualPipeBufferPool(16 * 1024),
	32 * 1024: newVirtualPipeBufferPool(32 * 1024),
	64 * 1024: newVirtualPipeBufferPool(64 * 1024),
}

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
	readPos      int
	writePos     int
	bufSize      int
	bufPooled    bool
	closed       bool
	wrClosed     bool
	err          error
	readDeadline time.Time
}

// virtualPipeReader is the read end of a virtualPipe.
// It implements io.ReadCloser and supports SetReadDeadline for
// compatibility with context cancellation in the read builtin.
type virtualPipeReader struct {
	pipe *virtualPipe
}

// virtualPipeWriter is the write end of a virtualPipe.
type virtualPipeWriter struct {
	pipe *virtualPipe
}

// checkDeadline checks if the read deadline has been exceeded.
// Must be called with the mutex held.
func (p *virtualPipe) checkDeadline() error {
	// Use !Before instead of After so that equality (same clock tick) also expires.
	// This prevents flaky cancellation when deadline == time.Now().
	if !p.readDeadline.IsZero() && !time.Now().Before(p.readDeadline) {
		return errDeadlineExceeded
	}
	return nil
}

// NewVirtualPipe creates a new buffered virtual pipe with the default buffer size.
func NewVirtualPipe() (StdinReader, io.WriteCloser) {
	return newVirtualPipeSize(defaultPipeBufferSize)
}

func newVirtualPipeBufferPool(size int) *sync.Pool {
	return &sync.Pool{
		New: func() any {
			return make([]byte, size)
		},
	}
}

func newVirtualPipeSize(size int) (StdinReader, io.WriteCloser) {
	if size <= 0 {
		size = defaultPipeBufferSize
	}
	p := &virtualPipe{
		bufSize: size,
	}
	p.cond = sync.NewCond(&p.mu)
	return &virtualPipeReader{pipe: p}, &virtualPipeWriter{pipe: p}
}

func (p *virtualPipe) unreadLen() int {
	return p.writePos - p.readPos
}

func (p *virtualPipe) pooledCapacityFor(needed int) int {
	switch p.bufSize {
	case 4 * 1024, 8 * 1024, 16 * 1024, 32 * 1024, 64 * 1024:
		for _, size := range virtualPipeBufferBuckets {
			if size >= needed && size <= p.bufSize {
				return size
			}
		}
	}
	return 0
}

func (p *virtualPipe) nextBufferCapacity(needed int) int {
	if needed <= 0 {
		return 0
	}
	if pooled := p.pooledCapacityFor(needed); pooled != 0 {
		return pooled
	}

	size := len(p.buf)
	if size == 0 {
		size = min(p.bufSize, max(minVirtualPipeAllocSize, needed))
	}
	for size < needed && size < p.bufSize {
		next := size * 2
		if next <= size || next > p.bufSize {
			size = p.bufSize
			break
		}
		size = next
	}
	if size < needed {
		size = needed
	}
	if size > p.bufSize {
		size = p.bufSize
	}
	return size
}

func borrowVirtualPipeBuffer(size int) ([]byte, bool) {
	if pool := virtualPipeBufferPools[size]; pool != nil {
		if buf, ok := pool.Get().([]byte); ok && cap(buf) >= size {
			return buf[:size], true
		}
		return make([]byte, size), true
	}
	return make([]byte, size), false
}

func releaseVirtualPipeBuffer(buf []byte, pooled bool) {
	if !pooled || buf == nil {
		return
	}
	if pool := virtualPipeBufferPools[cap(buf)]; pool != nil {
		pool.Put(buf[:cap(buf)])
	}
}

func (p *virtualPipe) releaseBufferLocked() {
	releaseVirtualPipeBuffer(p.buf, p.bufPooled)
	p.buf = nil
	p.readPos = 0
	p.writePos = 0
	p.bufPooled = false
}

func (p *virtualPipe) compactBufferLocked() {
	unread := p.unreadLen()
	if unread == 0 {
		p.readPos = 0
		p.writePos = 0
		return
	}
	if p.readPos == 0 {
		return
	}
	copy(p.buf[:unread], p.buf[p.readPos:p.writePos])
	p.readPos = 0
	p.writePos = unread
}

func (p *virtualPipe) growBufferLocked(newSize int) {
	unread := p.unreadLen()
	buf, pooled := borrowVirtualPipeBuffer(newSize)
	if unread > 0 && p.buf != nil {
		copy(buf, p.buf[p.readPos:p.writePos])
	}
	p.releaseBufferLocked()
	p.buf = buf
	p.bufPooled = pooled
	p.readPos = 0
	p.writePos = unread
}

func (p *virtualPipe) ensureWriteCapacityLocked(toWrite int) {
	if toWrite <= 0 {
		return
	}
	if p.buf == nil {
		p.growBufferLocked(p.nextBufferCapacity(toWrite))
		return
	}
	if len(p.buf)-p.writePos >= toWrite {
		return
	}
	if p.readPos > 0 {
		p.compactBufferLocked()
		if len(p.buf)-p.writePos >= toWrite {
			return
		}
	}
	p.growBufferLocked(p.nextBufferCapacity(p.unreadLen() + toWrite))
}

func (p *virtualPipe) resetOrReleaseEmptyBufferLocked() {
	if p.unreadLen() > 0 {
		return
	}
	if p.closed || p.wrClosed {
		p.releaseBufferLocked()
		return
	}
	p.readPos = 0
	p.writePos = 0
}

// Read reads data from the pipe. It blocks until data is available,
// the write end is closed, the pipe is closed, or the read deadline is exceeded.
func (r *virtualPipeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	r.pipe.mu.Lock()
	defer r.pipe.mu.Unlock()

	for r.pipe.unreadLen() == 0 {
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

	n := copy(p, r.pipe.buf[r.pipe.readPos:r.pipe.writePos])
	r.pipe.readPos += n
	r.pipe.resetOrReleaseEmptyBufferLocked()
	r.pipe.cond.Broadcast()
	return n, nil
}

// SetReadDeadline sets the deadline for future Read calls.
// A zero value for t means Read will not time out.
// This is compatible with the os.File.SetReadDeadline interface.
func (r *virtualPipeReader) SetReadDeadline(t time.Time) error {
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
func (r *virtualPipeReader) Close() error {
	return r.CloseWithError(io.ErrClosedPipe)
}

// CloseWithError closes the read end with the given error.
// Subsequent writes will return this error.
func (r *virtualPipeReader) CloseWithError(err error) error {
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
	r.pipe.releaseBufferLocked()
	r.pipe.cond.Broadcast()
	return nil
}

// Write writes data to the pipe. It blocks if the buffer is full
// until space is available or the pipe is closed.
func (w *virtualPipeWriter) Write(p []byte) (int, error) {
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
		for w.pipe.unreadLen() >= w.pipe.bufSize {
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
		space := w.pipe.bufSize - w.pipe.unreadLen()
		toWrite := min(len(p)-written, space)
		w.pipe.ensureWriteCapacityLocked(toWrite)
		copy(w.pipe.buf[w.pipe.writePos:w.pipe.writePos+toWrite], p[written:written+toWrite])
		w.pipe.writePos += toWrite
		written += toWrite
		w.pipe.cond.Broadcast()
	}

	return written, nil
}

// Close closes the write end of the pipe. Subsequent reads will
// return io.EOF once the buffer is drained.
func (w *virtualPipeWriter) Close() error {
	return w.CloseWithError(nil)
}

// CloseWithError closes the write end with the given error.
// If err is nil, subsequent reads return io.EOF after draining the buffer.
// If err is non-nil, subsequent reads return the error immediately.
func (w *virtualPipeWriter) CloseWithError(err error) error {
	w.pipe.mu.Lock()
	defer w.pipe.mu.Unlock()

	if w.pipe.wrClosed {
		return nil
	}
	w.pipe.wrClosed = true
	if err != nil {
		w.pipe.closed = true
		w.pipe.err = err
		w.pipe.releaseBufferLocked()
	} else if w.pipe.unreadLen() == 0 {
		w.pipe.releaseBufferLocked()
	}
	w.pipe.cond.Broadcast()
	return nil
}
