package runtime

import (
	"context"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/commandutil"
	"golang.org/x/term"
)

type executionTTYContextKey struct{}

type executionTTY struct {
	reader  io.Reader
	writer  io.Writer
	readMu  sync.Mutex
	writeMu sync.Mutex
}

type executionTTYProvider interface {
	executionTTYSource() *executionTTY
}

type executionTTYFile struct {
	path   string
	flag   int
	source *executionTTY
	closed atomic.Bool
}

func withExecutionTTY(ctx context.Context, source *executionTTY) context.Context {
	if ctx == nil || source == nil {
		return ctx
	}
	return context.WithValue(ctx, executionTTYContextKey{}, source)
}

func executionTTYFromContext(ctx context.Context) *executionTTY {
	if ctx == nil {
		return nil
	}
	source, _ := ctx.Value(executionTTYContextKey{}).(*executionTTY)
	return source
}

func bindExecutionTTY(ctx context.Context, stdin io.Reader, stdout io.Writer) context.Context {
	if source := executionTTYFromReader(stdin); source != nil {
		return withExecutionTTY(ctx, source)
	}
	if executionTTYFromContext(ctx) != nil {
		return ctx
	}
	if !readerLooksLikeTTY(stdin) {
		return ctx
	}
	return withExecutionTTY(ctx, &executionTTY{
		reader: stdin,
		writer: stdout,
	})
}

func forceExecutionTTY(ctx context.Context, stdin io.Reader, stdout io.Writer) context.Context {
	if source := executionTTYFromReader(stdin); source != nil {
		return withExecutionTTY(ctx, source)
	}
	if stdin == nil {
		return ctx
	}
	return withExecutionTTY(ctx, &executionTTY{
		reader: stdin,
		writer: stdout,
	})
}

func executionTTYFromReader(reader io.Reader) *executionTTY {
	reader = resolveTTYReader(reader)
	if reader == nil {
		return nil
	}
	sourceProvider, ok := reader.(executionTTYProvider)
	if !ok {
		return nil
	}
	return sourceProvider.executionTTYSource()
}

func readerLooksLikeTTY(reader io.Reader) bool {
	reader = resolveTTYReader(reader)
	if reader == nil {
		return false
	}
	if meta, ok := reader.(commandutil.RedirectMetadata); ok && recognizedTTYPath(meta.RedirectPath()) {
		return true
	}
	if statter, ok := reader.(interface {
		Stat() (stdfs.FileInfo, error)
	}); ok {
		if info, err := statter.Stat(); err == nil && info.Mode()&stdfs.ModeCharDevice != 0 {
			return true
		}
	}
	if fd, ok := reader.(interface{ Fd() uintptr }); ok {
		if descriptor := fd.Fd(); descriptor != 0 && term.IsTerminal(int(descriptor)) {
			return true
		}
	}
	return false
}

func resolveTTYReader(reader io.Reader) io.Reader {
	type underlyingReader interface {
		UnderlyingReader() io.Reader
	}
	for reader != nil {
		provider, ok := reader.(underlyingReader)
		if !ok {
			return reader
		}
		next := provider.UnderlyingReader()
		if next == nil || next == reader {
			return reader
		}
		reader = next
	}
	return nil
}

func recognizedTTYPath(name string) bool {
	cleaned := path.Clean(strings.TrimSpace(name))
	switch {
	case cleaned == virtualTTYDevice, cleaned == virtualConsoleDevice:
		return true
	case path.Dir(cleaned) == "/dev" && strings.HasPrefix(path.Base(cleaned), "tty"):
		return true
	case path.Dir(cleaned) == "/dev/pts":
		base := path.Base(cleaned)
		return base != "" && base != "." && base != ".."
	default:
		return false
	}
}

func virtualTTYInfo(name string) stdfs.FileInfo {
	return virtualFileInfo{
		name:    name,
		mode:    stdfs.ModeDevice | stdfs.ModeCharDevice | 0o666,
		modTime: virtualDeviceModTime,
		uid:     gbfs.DefaultOwnerUID,
		gid:     gbfs.DefaultOwnerGID,
	}
}

func newExecutionTTYFile(devicePath string, flag int, source *executionTTY) *executionTTYFile {
	return &executionTTYFile{
		path:   devicePath,
		flag:   flag,
		source: source,
	}
}

func (f *executionTTYFile) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !canReadVirtualDevice(f.flag) {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}
	if f.source == nil || f.source.reader == nil {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: syscall.ENXIO}
	}
	f.source.readMu.Lock()
	defer f.source.readMu.Unlock()
	return f.source.reader.Read(p)
}

func (f *executionTTYFile) Write(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !canWriteVirtualDevice(f.flag) {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrPermission}
	}
	if f.source == nil || f.source.writer == nil {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: syscall.ENXIO}
	}
	f.source.writeMu.Lock()
	defer f.source.writeMu.Unlock()
	return f.source.writer.Write(p)
}

func (f *executionTTYFile) Close() error {
	f.closed.Store(true)
	return nil
}

func (f *executionTTYFile) Stat() (stdfs.FileInfo, error) {
	if f.closed.Load() {
		return nil, stdfs.ErrClosed
	}
	return virtualTTYInfo(path.Base(f.path)), nil
}

func (f *executionTTYFile) executionTTYSource() *executionTTY {
	if f == nil {
		return nil
	}
	return f.source
}

var _ gbfs.File = (*executionTTYFile)(nil)
