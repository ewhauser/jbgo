package agentfsfs

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ewhauser/gbash"
	gbfs "github.com/ewhauser/gbash/fs"
	agentfs "github.com/tursodatabase/agentfs/sdk/go"
)

const maxSymlinkDepth = 40

type Factory struct {
	DBPath string
}

func (f Factory) New(ctx context.Context) (gbfs.FileSystem, error) {
	return newAgentFS(ctx, f.DBPath)
}

type FileSystem struct {
	afs *agentfs.AgentFS

	mu   sync.RWMutex
	cwd  string
	fifo map[int64]*memoryFIFO
}

func NewRuntime(ctx context.Context, dbPath, workDir string) (*gbash.Runtime, error) {
	return gbash.New(gbash.WithFileSystem(
		gbash.CustomFileSystem(Factory{DBPath: dbPath}, workDir),
	))
}

func newAgentFS(ctx context.Context, dbPath string) (*FileSystem, error) {
	afs, err := agentfs.Open(ctx, agentfs.AgentFSOptions{Path: dbPath})
	if err != nil {
		return nil, err
	}

	return &FileSystem{
		afs:  afs,
		cwd:  "/",
		fifo: make(map[int64]*memoryFIFO),
	}, nil
}

func (f *FileSystem) close() error {
	if f == nil || f.afs == nil {
		return nil
	}
	return f.afs.Close()
}

func (f *FileSystem) Close() error {
	return f.close()
}

func (f *FileSystem) Open(ctx context.Context, name string) (gbfs.File, error) {
	return f.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (f *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	abs := f.resolve(name)
	if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
		if _, err := f.afs.FS.Lstat(ctx, abs); err == nil {
			return nil, pathError("open", abs, stdfs.ErrExist)
		} else if !isNotExist(err) {
			return nil, wrapError("open", abs, err)
		}
	}

	stats, err := f.afs.FS.Stat(ctx, abs)
	if err != nil {
		if !isNotExist(err) || flag&os.O_CREATE == 0 {
			return nil, wrapError("open", abs, err)
		}
	} else {
		if stats.IsDir() {
			return nil, pathError("open", abs, stdfs.ErrInvalid)
		}
		if stats.IsFIFO() {
			return f.openFIFO(ctx, abs, stats.Ino, flag)
		}
	}

	file, err := f.afs.FS.Open(ctx, abs, openFlags(flag))
	if err != nil {
		return nil, wrapError("open", abs, err)
	}

	return &regularFile{
		file:     file,
		path:     abs,
		append:   flag&os.O_APPEND != 0,
		readable: canRead(flag),
		writable: canWrite(flag),
	}, nil
}

func (f *FileSystem) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs := f.resolve(name)
	stats, err := f.afs.FS.Stat(ctx, abs)
	if err != nil {
		return nil, wrapError("stat", abs, err)
	}
	return newFileInfo(path.Base(abs), stats), nil
}

func (f *FileSystem) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs := f.resolve(name)
	stats, err := f.afs.FS.Lstat(ctx, abs)
	if err != nil {
		return nil, wrapError("lstat", abs, err)
	}
	return newFileInfo(path.Base(abs), stats), nil
}

func (f *FileSystem) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	abs := f.resolve(name)
	entries, err := f.afs.FS.ReaddirPlus(ctx, abs)
	if err != nil {
		return nil, wrapError("readdir", abs, err)
	}
	slices.SortFunc(entries, func(a, b agentfs.DirEntry) int {
		return strings.Compare(a.Name, b.Name)
	})

	out := make([]stdfs.DirEntry, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		out = append(out, fileInfoDirEntry{info: newFileInfo(entry.Name, entry.Stats)})
	}
	return out, nil
}

func (f *FileSystem) Readlink(ctx context.Context, name string) (string, error) {
	abs := f.resolve(name)
	target, err := f.afs.FS.Readlink(ctx, abs)
	if err != nil {
		return "", wrapError("readlink", abs, err)
	}
	return target, nil
}

func (f *FileSystem) Realpath(ctx context.Context, name string) (string, error) {
	abs, _, err := f.resolveExistingPath(ctx, f.resolve(name), true, false, 0)
	if err != nil {
		return "", wrapError("realpath", f.resolve(name), err)
	}
	return abs, nil
}

func (f *FileSystem) Symlink(ctx context.Context, target, linkName string) error {
	abs := f.resolve(linkName)
	return wrapError("symlink", abs, f.afs.FS.Symlink(ctx, target, abs))
}

func (f *FileSystem) Link(ctx context.Context, oldName, newName string) error {
	oldAbs := f.resolve(oldName)
	stats, err := f.afs.FS.Stat(ctx, oldAbs)
	if err != nil {
		return wrapError("link", oldAbs, err)
	}
	if stats.IsDir() {
		return pathError("link", oldAbs, stdfs.ErrInvalid)
	}
	newAbs := f.resolve(newName)
	return wrapError("link", newAbs, f.afs.FS.Link(ctx, oldAbs, newAbs))
}

func (f *FileSystem) Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error {
	abs := f.resolve(name)
	if !follow {
		return pathError("chown", abs, stdfs.ErrPermission)
	}
	return wrapError("chown", abs, f.afs.FS.Chown(ctx, abs, int64(uid), int64(gid)))
}

func (f *FileSystem) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	abs := f.resolve(name)
	return wrapError("chmod", abs, f.afs.FS.Chmod(ctx, abs, int64(mode.Perm())))
}

func (f *FileSystem) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	abs := f.resolve(name)
	if atime.IsZero() {
		atime = time.Now().UTC()
	}
	if mtime.IsZero() {
		mtime = time.Now().UTC()
	}
	return wrapError("chtimes", abs, f.afs.FS.UtimesNano(
		ctx,
		abs,
		atime.Unix(),
		int64(atime.Nanosecond()),
		mtime.Unix(),
		int64(mtime.Nanosecond()),
	))
}

func (f *FileSystem) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs := f.resolve(name)
	mode := perm.Perm()
	if mode == 0 {
		mode = 0o755
	}
	return wrapError("mkdir", abs, f.afs.FS.MkdirAll(ctx, abs, int64(mode)))
}

func (f *FileSystem) Mkfifo(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs := f.resolve(name)
	mode := int64(agentfs.S_IFIFO | int64(perm.Perm()))
	if err := f.afs.FS.MkdirAll(ctx, path.Dir(abs), 0o755); err != nil {
		return wrapError("mkfifo", abs, err)
	}
	if err := f.afs.FS.Mknod(ctx, abs, mode, 0); err != nil {
		return wrapError("mkfifo", abs, err)
	}
	return nil
}

func (f *FileSystem) Remove(ctx context.Context, name string, recursive bool) error {
	abs := f.resolve(name)
	if abs == "/" {
		return pathError("remove", abs, stdfs.ErrPermission)
	}

	stats, err := f.afs.FS.Lstat(ctx, abs)
	if err != nil {
		return wrapError("remove", abs, err)
	}

	if stats.IsDir() {
		if !recursive {
			entries, err := f.afs.FS.Readdir(ctx, abs)
			if err != nil {
				return wrapError("remove", abs, err)
			}
			if len(entries) > 0 {
				return pathError("remove", abs, stdfs.ErrInvalid)
			}
			return wrapError("remove", abs, f.afs.FS.Rmdir(ctx, abs))
		}
		return wrapError("remove", abs, f.removeTree(ctx, abs))
	}

	return wrapError("remove", abs, f.afs.FS.Unlink(ctx, abs))
}

func (f *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	oldAbs := f.resolve(oldName)
	newAbs := f.resolve(newName)
	return wrapError("rename", newAbs, f.afs.FS.Rename(ctx, oldAbs, newAbs))
}

func (f *FileSystem) Getwd() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.cwd
}

func (f *FileSystem) Chdir(name string) error {
	ctx := context.Background()
	abs, stats, err := f.resolveExistingPath(ctx, f.resolve(name), true, false, 0)
	if err != nil {
		return wrapError("chdir", f.resolve(name), err)
	}
	if !stats.IsDir() {
		return pathError("chdir", abs, stdfs.ErrInvalid)
	}
	f.mu.Lock()
	f.cwd = abs
	f.mu.Unlock()
	return nil
}

func (f *FileSystem) resolve(name string) string {
	return gbfs.Resolve(f.Getwd(), name)
}

func (f *FileSystem) resolveExistingPath(ctx context.Context, abs string, followFinal, allowMissingFinal bool, depth int) (string, *agentfs.Stats, error) {
	abs = gbfs.Clean(abs)
	if depth > maxSymlinkDepth {
		return "", nil, fmt.Errorf("too many levels of symbolic links")
	}
	if abs == "/" {
		stats, err := f.afs.FS.Stat(ctx, "/")
		return "/", stats, err
	}

	current := "/"
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, part := range parts {
		isLast := i == len(parts)-1
		next := gbfs.Resolve(current, part)
		stats, err := f.afs.FS.Lstat(ctx, next)
		if err != nil {
			if isLast && allowMissingFinal && isNotExist(err) {
				return next, nil, nil
			}
			return "", nil, err
		}
		if stats.IsSymlink() && (!isLast || followFinal) {
			target, err := f.afs.FS.Readlink(ctx, next)
			if err != nil {
				return "", nil, err
			}
			targetAbs := gbfs.Resolve(path.Dir(next), target)
			if !isLast {
				targetAbs = gbfs.Resolve(targetAbs, path.Join(parts[i+1:]...))
			}
			return f.resolveExistingPath(ctx, targetAbs, true, allowMissingFinal && isLast, depth+1)
		}
		if isLast {
			return next, stats, nil
		}
		if !stats.IsDir() {
			return "", nil, pathError("realpath", next, stdfs.ErrInvalid)
		}
		current = next
	}

	return "/", nil, stdfs.ErrNotExist
}

func (f *FileSystem) removeTree(ctx context.Context, abs string) error {
	entries, err := f.afs.FS.ReaddirPlus(ctx, abs)
	if err != nil {
		return err
	}
	slices.SortFunc(entries, func(a, b agentfs.DirEntry) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, entry := range entries {
		child := gbfs.Resolve(abs, entry.Name)
		if entry.Stats != nil && entry.Stats.IsDir() {
			if err := f.removeTree(ctx, child); err != nil {
				return err
			}
			continue
		}
		if err := f.afs.FS.Unlink(ctx, child); err != nil {
			return err
		}
	}
	return f.afs.FS.Rmdir(ctx, abs)
}

func (f *FileSystem) openFIFO(ctx context.Context, abs string, ino int64, flag int) (gbfs.File, error) {
	f.mu.Lock()
	fifo := f.fifo[ino]
	if fifo == nil {
		fifo = newMemoryFIFO()
		f.fifo[ino] = fifo
	}
	f.mu.Unlock()
	return newMemoryFIFOFile(f, abs, fifo, flag), nil
}

func openFlags(flag int) int {
	var out int
	if canRead(flag) && canWrite(flag) {
		out |= agentfs.O_RDWR
	} else if canWrite(flag) {
		out |= agentfs.O_WRONLY
	} else {
		out |= agentfs.O_RDONLY
	}
	if flag&os.O_APPEND != 0 {
		out |= agentfs.O_APPEND
	}
	if flag&os.O_CREATE != 0 {
		out |= agentfs.O_CREATE
	}
	if flag&os.O_EXCL != 0 {
		out |= agentfs.O_EXCL
	}
	if flag&os.O_TRUNC != 0 {
		out |= agentfs.O_TRUNC
	}
	return out
}

func canRead(flag int) bool {
	return flag&os.O_WRONLY == 0
}

func canWrite(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR) != 0
}

func isNotExist(err error) bool {
	return agentfs.IsNotExist(err) || errors.Is(err, stdfs.ErrNotExist)
}

func wrapError(op, path string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var fsErr *agentfs.FSError
	if errors.As(err, &fsErr) {
		switch fsErr.Code {
		case agentfs.ENOENT:
			return pathError(op, path, stdfs.ErrNotExist)
		case agentfs.EEXIST:
			return pathError(op, path, stdfs.ErrExist)
		case agentfs.EPERM, agentfs.EACCES:
			return pathError(op, path, stdfs.ErrPermission)
		case agentfs.ENOTDIR, agentfs.EISDIR, agentfs.EINVAL, agentfs.ENOTEMPTY:
			return pathError(op, path, stdfs.ErrInvalid)
		case agentfs.ELOOP:
			return pathError(op, path, fmt.Errorf("too many levels of symbolic links"))
		}
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return &os.PathError{Op: op, Path: path, Err: pathErr.Err}
	}

	return &os.PathError{Op: op, Path: path, Err: err}
}

func pathError(op, path string, err error) error {
	return &os.PathError{Op: op, Path: path, Err: err}
}

type regularFile struct {
	file *agentfs.File
	path string

	append   bool
	readable bool
	writable bool
}

func (f *regularFile) Read(p []byte) (int, error) {
	if !f.readable {
		return 0, pathError("read", f.path, stdfs.ErrPermission)
	}
	return f.file.Read(p)
}

func (f *regularFile) Write(p []byte) (int, error) {
	if !f.writable {
		return 0, pathError("write", f.path, stdfs.ErrPermission)
	}
	if !f.append {
		return f.file.Write(p)
	}
	size, err := f.file.Size()
	if err != nil {
		return 0, err
	}
	return f.file.WriteAt(p, size)
}

func (f *regularFile) Close() error {
	return f.file.Close()
}

func (f *regularFile) Stat() (stdfs.FileInfo, error) {
	stats, err := f.file.Stat(context.Background())
	if err != nil {
		return nil, err
	}
	return newFileInfo(path.Base(f.path), stats), nil
}

func (f *regularFile) Seek(offset int64, whence int) (int64, error) {
	return f.file.Seek(offset, whence)
}

func (f *regularFile) ReadAt(p []byte, off int64) (int, error) {
	return f.file.ReadAt(p, off)
}

func (f *regularFile) WriteAt(p []byte, off int64) (int, error) {
	if !f.writable {
		return 0, pathError("write", f.path, stdfs.ErrPermission)
	}
	return f.file.WriteAt(p, off)
}

type fileInfo struct {
	name  string
	stats *agentfs.Stats
}

func newFileInfo(name string, stats *agentfs.Stats) fileInfo {
	if name == "" {
		name = "."
	}
	return fileInfo{name: name, stats: stats}
}

func (f fileInfo) Name() string {
	return f.name
}

func (f fileInfo) Size() int64 {
	if f.stats == nil {
		return 0
	}
	return f.stats.Size
}

func (f fileInfo) Mode() stdfs.FileMode {
	if f.stats == nil {
		return 0
	}
	mode := stdfs.FileMode(f.stats.Permissions())
	switch f.stats.FileType() {
	case agentfs.S_IFDIR:
		mode |= stdfs.ModeDir
	case agentfs.S_IFLNK:
		mode |= stdfs.ModeSymlink
	case agentfs.S_IFIFO:
		mode |= stdfs.ModeNamedPipe
	case agentfs.S_IFSOCK:
		mode |= stdfs.ModeSocket
	case agentfs.S_IFCHR:
		mode |= stdfs.ModeDevice | stdfs.ModeCharDevice
	case agentfs.S_IFBLK:
		mode |= stdfs.ModeDevice
	case agentfs.S_IFREG:
	default:
		mode |= stdfs.ModeIrregular
	}
	return mode
}

func (f fileInfo) ModTime() time.Time {
	if f.stats == nil {
		return time.Time{}
	}
	return f.stats.MtimeTime().UTC()
}

func (f fileInfo) IsDir() bool {
	return f.stats != nil && f.stats.IsDir()
}

func (f fileInfo) Sys() any {
	return f.stats
}

type fileInfoDirEntry struct {
	info fileInfo
}

func (d fileInfoDirEntry) Name() string {
	return d.info.Name()
}

func (d fileInfoDirEntry) IsDir() bool {
	return d.info.IsDir()
}

func (d fileInfoDirEntry) Type() stdfs.FileMode {
	return d.info.Mode().Type()
}

func (d fileInfoDirEntry) Info() (stdfs.FileInfo, error) {
	return d.info, nil
}
