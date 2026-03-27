package fs

import (
	"context"
	"io"
	stdfs "io/fs"
	"os"
	"sync"
	"sync/atomic"
)

// FIFOFileSystem is an optional filesystem capability for creating named pipes.
type FIFOFileSystem interface {
	Mkfifo(ctx context.Context, name string, perm stdfs.FileMode) error
}

func mkfifoViaOptional(ctx context.Context, fsys FileSystem, name string, perm stdfs.FileMode) error {
	if fifoFS, ok := fsys.(FIFOFileSystem); ok {
		return fifoFS.Mkfifo(ctx, name, perm)
	}
	return &os.PathError{Op: "mkfifo", Path: Clean(name), Err: stdfs.ErrPermission}
}

type memoryFIFO struct {
	mu      sync.Mutex
	cond    *sync.Cond
	buf     []byte
	readers int
	writers int

	readerEpoch uint64
	writerEpoch uint64
}

func newMemoryFIFO() *memoryFIFO {
	fifo := &memoryFIFO{}
	fifo.cond = sync.NewCond(&fifo.mu)
	return fifo
}

func (f *memoryFIFO) clone() *memoryFIFO {
	if f == nil {
		return nil
	}
	clone := newMemoryFIFO()
	f.mu.Lock()
	clone.buf = append([]byte(nil), f.buf...)
	f.mu.Unlock()
	return clone
}

type memoryFIFOFile struct {
	fs       *MemoryFS
	path     string
	fifo     *memoryFIFO
	readable bool
	writable bool
	closed   atomic.Bool

	readerEpoch uint64
	sawReader   bool
	writerEpoch uint64
	sawWriter   bool
}

func newMemoryFIFOFile(fs *MemoryFS, path string, fifo *memoryFIFO, flag int) *memoryFIFOFile {
	file := &memoryFIFOFile{
		fs:       fs,
		path:     path,
		fifo:     fifo,
		readable: canRead(flag),
		writable: canWrite(flag),
	}
	fifo.mu.Lock()
	if file.readable {
		if fifo.readers == 0 {
			fifo.readerEpoch++
		}
		fifo.readers++
	}
	if file.readable {
		file.writerEpoch = fifo.writerEpoch
		// Remember any writer that was already attached when the reader opened.
		// If that writer closes before the reader performs its first Read, the
		// reader must still observe EOF instead of waiting forever for a new one.
		file.sawWriter = fifo.writerEpoch > 0
	}
	if file.writable {
		file.readerEpoch = fifo.readerEpoch
		file.sawReader = fifo.readers > 0
		fifo.writerEpoch++
		fifo.writers++
	}
	fifo.cond.Broadcast()
	fifo.mu.Unlock()
	return file
}

func (f *memoryFIFOFile) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !f.readable {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}
	if len(p) == 0 {
		return 0, nil
	}

	f.fifo.mu.Lock()
	defer f.fifo.mu.Unlock()

	for len(f.fifo.buf) == 0 {
		if f.closed.Load() {
			return 0, stdfs.ErrClosed
		}
		if f.fifo.writers == 0 && (f.sawWriter || f.fifo.writerEpoch != f.writerEpoch) {
			return 0, io.EOF
		}
		f.sawWriter = f.sawWriter || f.fifo.writers > 0
		f.fifo.cond.Wait()
	}
	f.sawWriter = true

	n := copy(p, f.fifo.buf)
	f.fifo.buf = f.fifo.buf[n:]
	f.fifo.cond.Broadcast()
	return n, nil
}

func (f *memoryFIFOFile) Write(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !f.writable {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrPermission}
	}
	if len(p) == 0 {
		return 0, nil
	}

	f.fifo.mu.Lock()
	defer f.fifo.mu.Unlock()

	for f.fifo.readers == 0 {
		if f.closed.Load() {
			return 0, stdfs.ErrClosed
		}
		if f.sawReader || f.fifo.readerEpoch != f.readerEpoch {
			return 0, &os.PathError{Op: "write", Path: f.path, Err: io.ErrClosedPipe}
		}
		f.fifo.cond.Wait()
	}
	f.sawReader = true
	f.readerEpoch = f.fifo.readerEpoch
	f.fifo.buf = append(f.fifo.buf, p...)
	f.fifo.cond.Broadcast()
	return len(p), nil
}

func (f *memoryFIFOFile) Close() error {
	if !f.closed.CompareAndSwap(false, true) {
		return nil
	}

	f.fifo.mu.Lock()
	if f.readable && f.fifo.readers > 0 {
		f.fifo.readers--
	}
	if f.writable && f.fifo.writers > 0 {
		f.fifo.writers--
	}
	f.fifo.cond.Broadcast()
	f.fifo.mu.Unlock()
	return nil
}

func (f *memoryFIFOFile) Stat() (stdfs.FileInfo, error) {
	if f.closed.Load() {
		return nil, stdfs.ErrClosed
	}
	return f.fs.Stat(context.Background(), f.path)
}

var _ File = (*memoryFIFOFile)(nil)
