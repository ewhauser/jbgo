package interp

import (
	"errors"
	"io"
	"strconv"
	"sync"
	"time"
)

const shellNamedFDStart = 10

type readDeadliner interface {
	SetReadDeadline(time.Time) error
}

// shellFD models one shell-visible file descriptor.
//
// Multiple descriptor numbers may point at the same shellFD so duplicated
// descriptors share file position and any buffered read state.
type shellFD struct {
	reader   io.Reader
	writer   io.Writer
	closer   io.Closer
	deadline readDeadliner
	owned    bool

	buffered   bool
	bufferByte byte
	closed     bool
	writeErr   error
	writeErrMu sync.Mutex
}

func newShellInputFD(reader io.Reader) *shellFD {
	return newShellInputFDWithOwnership(reader, false)
}

func newOwnedShellInputFD(reader io.Reader) *shellFD {
	return newShellInputFDWithOwnership(reader, true)
}

func newShellInputFDWithOwnership(reader io.Reader, owned bool) *shellFD {
	if reader == nil {
		return &shellFD{}
	}
	fd := &shellFD{reader: reader, owned: owned}
	if closer, ok := reader.(io.Closer); ok {
		fd.closer = closer
	}
	if deadliner, ok := reader.(readDeadliner); ok {
		fd.deadline = deadliner
	}
	return fd
}

func newShellOutputFD(writer io.Writer) *shellFD {
	return &shellFD{writer: writer}
}

func newShellReadWriteFD(file io.ReadWriteCloser, readable, writable bool) *shellFD {
	fd := &shellFD{closer: file, owned: true}
	if readable {
		fd.reader = file
	}
	if writable {
		fd.writer = file
	}
	if deadliner, ok := file.(readDeadliner); ok {
		fd.deadline = deadliner
	}
	return fd
}

func (fd *shellFD) Read(p []byte) (int, error) {
	if fd == nil || fd.reader == nil {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if fd.buffered {
		p[0] = fd.bufferByte
		fd.buffered = false
		if len(p) == 1 {
			return 1, nil
		}
		n, err := fd.reader.Read(p[1:])
		return n + 1, err
	}
	return fd.reader.Read(p)
}

func (fd *shellFD) Write(p []byte) (int, error) {
	if fd == nil || fd.writer == nil {
		return 0, io.ErrClosedPipe
	}
	n, err := fd.writer.Write(p)
	if err != nil {
		fd.writeErrMu.Lock()
		if fd.writeErr == nil {
			fd.writeErr = err
		}
		fd.writeErrMu.Unlock()
	}
	return n, err
}

func (fd *shellFD) ReadByte() (byte, error) {
	var buf [1]byte
	n, err := fd.Read(buf[:])
	if n > 0 {
		return buf[0], nil
	}
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

func (fd *shellFD) PeekByte() (byte, error) {
	if fd == nil || fd.reader == nil {
		return 0, io.EOF
	}
	if fd.buffered {
		return fd.bufferByte, nil
	}
	var buf [1]byte
	n, err := fd.reader.Read(buf[:])
	if n > 0 {
		fd.bufferByte = buf[0]
		fd.buffered = true
		return buf[0], nil
	}
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

func (fd *shellFD) UnderlyingReader() io.Reader {
	if fd == nil {
		return nil
	}
	return fd.reader
}

func (fd *shellFD) SetReadDeadline(t time.Time) error {
	if fd == nil || fd.deadline == nil {
		return nil
	}
	return fd.deadline.SetReadDeadline(t)
}

func (fd *shellFD) Close() error {
	if fd == nil || fd.closed {
		return nil
	}
	fd.closed = true
	if fd.closer == nil {
		return nil
	}
	return fd.closer.Close()
}

func cloneFDTable(src map[int]*shellFD) map[int]*shellFD {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[int]*shellFD, len(src))
	for n, fd := range src {
		dst[n] = fd
	}
	return dst
}

func initialFDTable(stdin StdinReader, stdout, stderr io.Writer) map[int]*shellFD {
	return map[int]*shellFD{
		0: newShellInputFD(stdin),
		1: newShellOutputFD(stdout),
		2: newShellOutputFD(stderr),
	}
}

func (r *Runner) ensureFDTable() {
	if r.fds != nil {
		return
	}
	r.fds = initialFDTable(r.stdin, r.stdout, r.stderr)
}

func (r *Runner) syncStandardFDs() {
	r.ensureFDTable()

	if fd := r.fds[0]; fd != nil && fd.reader != nil {
		r.stdin = fd
	} else {
		r.stdin = nil
	}

	if fd := r.fds[1]; fd != nil && fd.writer != nil {
		r.stdout = fd
	} else {
		r.stdout = io.Discard
	}

	if fd := r.fds[2]; fd != nil && fd.writer != nil {
		r.stderr = fd
	} else {
		r.stderr = io.Discard
	}
}

func (r *Runner) setStdinReader(in StdinReader) {
	r.stdin = in
	if r.fds == nil {
		return
	}
	r.fds[0] = newShellInputFD(in)
	r.syncStandardFDs()
}

func (r *Runner) setStdoutWriter(out io.Writer) {
	if out == nil {
		out = io.Discard
	}
	r.stdout = out
	if r.fds == nil {
		return
	}
	r.fds[1] = newShellOutputFD(out)
	r.syncStandardFDs()
}

func (r *Runner) setStderrWriter(err io.Writer) {
	if err == nil {
		err = io.Discard
	}
	r.stderr = err
	if r.fds == nil {
		return
	}
	r.fds[2] = newShellOutputFD(err)
	r.syncStandardFDs()
}

func (r *Runner) allocateFD() int {
	return r.allocateFDFrom(shellNamedFDStart)
}

func (r *Runner) allocateFDFrom(start int) int {
	r.ensureFDTable()
	if start < shellNamedFDStart {
		start = shellNamedFDStart
	}
	for fd := start; ; fd++ {
		if _, ok := r.fds[fd]; !ok {
			return fd
		}
	}
}

func (r *Runner) lookupNamedFD(name string) (int, error) {
	val := r.envGet(name)
	if val == "" {
		return 0, errors.New("bad file descriptor")
	}
	fd, err := strconv.Atoi(val)
	if err != nil || fd < 0 {
		return 0, errors.New("bad file descriptor")
	}
	return fd, nil
}

func (fd *shellFD) clearWriteError() {
	if fd == nil {
		return
	}
	fd.writeErrMu.Lock()
	fd.writeErr = nil
	fd.writeErrMu.Unlock()
}

func (fd *shellFD) writeError() error {
	if fd == nil {
		return nil
	}
	fd.writeErrMu.Lock()
	defer fd.writeErrMu.Unlock()
	return fd.writeErr
}

func (r *Runner) setFD(fdNum int, fd *shellFD) {
	r.ensureFDTable()
	old := r.fds[fdNum]
	if fd == nil {
		delete(r.fds, fdNum)
	} else {
		r.fds[fdNum] = fd
	}
	if old != nil && old != fd && old.owned && !r.fdReferencedElsewhere(fdNum, old) && !r.fdReferencedInSnapshots(old) {
		_ = old.Close()
	}
	if fdNum >= 0 && fdNum <= 2 {
		r.syncStandardFDs()
	}
}

func (r *Runner) pushFDSnapshot(snapshot map[int]*shellFD) {
	if snapshot == nil {
		return
	}
	r.fdSnapshots = append(r.fdSnapshots, snapshot)
}

func (r *Runner) popFDSnapshot() {
	if len(r.fdSnapshots) == 0 {
		return
	}
	r.fdSnapshots = r.fdSnapshots[:len(r.fdSnapshots)-1]
}

func (r *Runner) closeUnusedSnapshotFDs(snapshot map[int]*shellFD) {
	if len(snapshot) == 0 {
		return
	}
	seen := make(map[*shellFD]struct{}, len(snapshot))
	for _, fd := range snapshot {
		if fd == nil || !fd.owned {
			continue
		}
		if _, ok := seen[fd]; ok {
			continue
		}
		seen[fd] = struct{}{}
		if fdReferencedInTable(r.fds, fd) || r.fdReferencedInSnapshots(fd) {
			continue
		}
		_ = fd.Close()
	}
}

func (r *Runner) fdReferencedElsewhere(exclude int, target *shellFD) bool {
	for fdNum, fd := range r.fds {
		if fdNum == exclude {
			continue
		}
		if fd == target {
			return true
		}
	}
	return false
}

func (r *Runner) fdReferencedInSnapshots(target *shellFD) bool {
	if target == nil {
		return false
	}
	for _, snapshot := range r.fdSnapshots {
		if fdReferencedInTable(snapshot, target) {
			return true
		}
	}
	return false
}

func fdReferencedInTable(table map[int]*shellFD, target *shellFD) bool {
	if target == nil {
		return false
	}
	for _, fd := range table {
		if fd == target {
			return true
		}
	}
	return false
}

func (r *Runner) getFD(fdNum int) *shellFD {
	r.ensureFDTable()
	return r.fds[fdNum]
}
