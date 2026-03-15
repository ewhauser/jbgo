package runtime

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

const (
	virtualDeviceDir  = "/dev"
	virtualNullDevice = "/dev/null"
)

var virtualDeviceModTime = time.Unix(0, 0).UTC()

// wrapSandboxFileSystem reserves a small runtime-owned /dev namespace above the
// configured sandbox filesystem. The first device is /dev/null, which must
// behave consistently regardless of the underlying backend.
func wrapSandboxFileSystem(base gbfs.FileSystem) gbfs.FileSystem {
	if base == nil {
		return nil
	}
	cwd := strings.TrimSpace(base.Getwd())
	if cwd == "" {
		cwd = "/"
	}
	return &virtualDeviceFS{
		base: base,
		cwd:  gbfs.Clean(cwd),
	}
}

type virtualDeviceFS struct {
	base gbfs.FileSystem

	mu  sync.RWMutex
	cwd string
}

func (f *virtualDeviceFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	return f.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (f *virtualDeviceFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	abs := f.resolve(name)
	switch {
	case abs == virtualNullDevice:
		if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
		}
		return &nullDeviceFile{path: abs, flag: flag}, nil
	case abs == virtualDeviceDir || strings.HasPrefix(abs, virtualNullDevice+"/"):
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrInvalid}
	default:
		if isVirtualDeviceChild(abs) && openMayCreateOrWrite(flag) {
			if err := f.ensureVirtualDeviceDir(ctx); err != nil {
				return nil, err
			}
		}
		return f.base.OpenFile(ctx, abs, flag, perm)
	}
}

func (f *virtualDeviceFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir:
		return virtualDirInfo("dev"), nil
	case abs == virtualNullDevice:
		return virtualNullInfo(), nil
	case strings.HasPrefix(abs, virtualNullDevice+"/"):
		return nil, &os.PathError{Op: "stat", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.Stat(ctx, abs)
	}
}

func (f *virtualDeviceFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir:
		return virtualDirInfo("dev"), nil
	case abs == virtualNullDevice:
		return virtualNullInfo(), nil
	case strings.HasPrefix(abs, virtualNullDevice+"/"):
		return nil, &os.PathError{Op: "lstat", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.Lstat(ctx, abs)
	}
}

func (f *virtualDeviceFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	abs := f.resolve(name)
	switch {
	case abs == "/":
		return f.readRootDir(ctx)
	case abs == virtualDeviceDir:
		return f.readVirtualDeviceDir(ctx)
	case abs == virtualNullDevice || strings.HasPrefix(abs, virtualNullDevice+"/"):
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.ReadDir(ctx, abs)
	}
}

func (f *virtualDeviceFS) Readlink(ctx context.Context, name string) (string, error) {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir, abs == virtualNullDevice:
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrInvalid}
	case strings.HasPrefix(abs, virtualNullDevice+"/"):
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.Readlink(ctx, abs)
	}
}

func (f *virtualDeviceFS) Realpath(ctx context.Context, name string) (string, error) {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir || abs == virtualNullDevice:
		return abs, nil
	case strings.HasPrefix(abs, virtualNullDevice+"/"):
		return "", &os.PathError{Op: "realpath", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.Realpath(ctx, abs)
	}
}

func (f *virtualDeviceFS) Symlink(ctx context.Context, target, linkName string) error {
	abs := f.resolve(linkName)
	if err := rejectVirtualDeviceMutation("symlink", abs); err != nil {
		return err
	}
	if isVirtualDeviceChild(abs) {
		if err := f.ensureVirtualDeviceDir(ctx); err != nil {
			return err
		}
	}
	return f.base.Symlink(ctx, target, abs)
}

func (f *virtualDeviceFS) Link(ctx context.Context, oldName, newName string) error {
	oldAbs := f.resolve(oldName)
	newAbs := f.resolve(newName)
	if err := rejectVirtualDeviceMutation("link", oldAbs); err != nil {
		return err
	}
	if err := rejectVirtualDeviceMutation("link", newAbs); err != nil {
		return err
	}
	if isVirtualDeviceChild(newAbs) {
		if err := f.ensureVirtualDeviceDir(ctx); err != nil {
			return err
		}
	}
	return f.base.Link(ctx, oldAbs, newAbs)
}

func (f *virtualDeviceFS) Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error {
	abs := f.resolve(name)
	if err := rejectVirtualDeviceMutation("chown", abs); err != nil {
		return err
	}
	return f.base.Chown(ctx, abs, uid, gid, follow)
}

func (f *virtualDeviceFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	abs := f.resolve(name)
	if err := rejectVirtualDeviceMutation("chmod", abs); err != nil {
		return err
	}
	return f.base.Chmod(ctx, abs, mode)
}

func (f *virtualDeviceFS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	abs := f.resolve(name)
	if err := rejectVirtualDeviceMutation("chtimes", abs); err != nil {
		return err
	}
	return f.base.Chtimes(ctx, abs, atime, mtime)
}

func (f *virtualDeviceFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir:
		return f.ensureVirtualDeviceDir(ctx)
	case abs == virtualNullDevice || strings.HasPrefix(abs, virtualNullDevice+"/"):
		return &os.PathError{Op: "mkdir", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.MkdirAll(ctx, abs, perm)
	}
}

func (f *virtualDeviceFS) Remove(ctx context.Context, name string, recursive bool) error {
	abs := f.resolve(name)
	if err := rejectVirtualDeviceMutation("remove", abs); err != nil {
		return err
	}
	return f.base.Remove(ctx, abs, recursive)
}

func (f *virtualDeviceFS) Rename(ctx context.Context, oldName, newName string) error {
	oldAbs := f.resolve(oldName)
	newAbs := f.resolve(newName)
	if err := rejectVirtualDeviceMutation("rename", oldAbs); err != nil {
		return err
	}
	if err := rejectVirtualDeviceMutation("rename", newAbs); err != nil {
		return err
	}
	if isVirtualDeviceChild(newAbs) {
		if err := f.ensureVirtualDeviceDir(ctx); err != nil {
			return err
		}
	}
	return f.base.Rename(ctx, oldAbs, newAbs)
}

func (f *virtualDeviceFS) Getwd() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.cwd
}

func (f *virtualDeviceFS) Chdir(name string) error {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir:
		f.mu.Lock()
		f.cwd = abs
		f.mu.Unlock()
		return nil
	case abs == virtualNullDevice || strings.HasPrefix(abs, virtualNullDevice+"/"):
		return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
	default:
	}
	if err := f.base.Chdir(abs); err != nil {
		return err
	}
	f.mu.Lock()
	f.cwd = gbfs.Clean(f.base.Getwd())
	f.mu.Unlock()
	return nil
}

func (f *virtualDeviceFS) SearchProviderForPath(name string) (gbfs.SearchProvider, bool) {
	abs := f.resolve(name)
	switch {
	case abs == "/", abs == virtualDeviceDir, abs == virtualNullDevice:
		return nil, false
	case strings.HasPrefix(abs, virtualDeviceDir+"/"):
		return nil, false
	default:
	}
	capable, ok := f.base.(gbfs.SearchCapable)
	if !ok {
		return nil, false
	}
	return capable.SearchProviderForPath(abs)
}

func (f *virtualDeviceFS) resolve(name string) string {
	return gbfs.Resolve(f.Getwd(), name)
}

func (f *virtualDeviceFS) ensureVirtualDeviceDir(ctx context.Context) error {
	return f.base.MkdirAll(ctx, virtualDeviceDir, 0o755)
}

func isVirtualDeviceChild(abs string) bool {
	return strings.HasPrefix(abs, virtualDeviceDir+"/") &&
		abs != virtualNullDevice &&
		!strings.HasPrefix(abs, virtualNullDevice+"/")
}

func openMayCreateOrWrite(flag int) bool {
	return flag&os.O_CREATE != 0 || flag&(os.O_WRONLY|os.O_RDWR) != 0
}

func (f *virtualDeviceFS) readRootDir(ctx context.Context) ([]stdfs.DirEntry, error) {
	entries, err := f.base.ReadDir(ctx, "/")
	if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return nil, err
	}
	byName := make(map[string]stdfs.DirEntry, len(entries)+1)
	for _, entry := range entries {
		if entry == nil || entry.Name() == "dev" {
			continue
		}
		byName[entry.Name()] = entry
	}
	byName["dev"] = stdfs.FileInfoToDirEntry(virtualDirInfo("dev"))
	return sortedDirEntries(byName), nil
}

func (f *virtualDeviceFS) readVirtualDeviceDir(ctx context.Context) ([]stdfs.DirEntry, error) {
	baseEntries, err := f.base.ReadDir(ctx, virtualDeviceDir)
	switch {
	case err == nil:
	case errors.Is(err, stdfs.ErrNotExist), errors.Is(err, stdfs.ErrInvalid):
		baseEntries = nil
	default:
		return nil, err
	}
	byName := make(map[string]stdfs.DirEntry, len(baseEntries)+1)
	for _, entry := range baseEntries {
		if entry == nil || entry.Name() == "null" {
			continue
		}
		byName[entry.Name()] = entry
	}
	byName["null"] = stdfs.FileInfoToDirEntry(virtualNullInfo())
	return sortedDirEntries(byName), nil
}

func sortedDirEntries(entries map[string]stdfs.DirEntry) []stdfs.DirEntry {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]stdfs.DirEntry, 0, len(names))
	for _, name := range names {
		out = append(out, entries[name])
	}
	return out
}

func rejectVirtualDeviceMutation(op, abs string) error {
	switch {
	case abs == virtualDeviceDir || abs == virtualNullDevice:
		return &os.PathError{Op: op, Path: abs, Err: stdfs.ErrPermission}
	case strings.HasPrefix(abs, virtualNullDevice+"/"):
		return &os.PathError{Op: op, Path: abs, Err: stdfs.ErrInvalid}
	default:
		return nil
	}
}

func virtualDirInfo(name string) stdfs.FileInfo {
	return virtualFileInfo{
		name:    name,
		mode:    stdfs.ModeDir | 0o755,
		modTime: virtualDeviceModTime,
		uid:     gbfs.DefaultOwnerUID,
		gid:     gbfs.DefaultOwnerGID,
	}
}

func virtualNullInfo() stdfs.FileInfo {
	return virtualFileInfo{
		name:    "null",
		mode:    stdfs.ModeDevice | stdfs.ModeCharDevice | 0o666,
		modTime: virtualDeviceModTime,
		uid:     gbfs.DefaultOwnerUID,
		gid:     gbfs.DefaultOwnerGID,
	}
}

type virtualFileInfo struct {
	name    string
	mode    stdfs.FileMode
	modTime time.Time
	uid     uint32
	gid     uint32
}

func (fi virtualFileInfo) Name() string         { return fi.name }
func (fi virtualFileInfo) Size() int64          { return 0 }
func (fi virtualFileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi virtualFileInfo) ModTime() time.Time   { return fi.modTime }
func (fi virtualFileInfo) IsDir() bool          { return fi.mode.IsDir() }
func (fi virtualFileInfo) Sys() any             { return gbfs.FileOwnership{UID: fi.uid, GID: fi.gid} }
func (fi virtualFileInfo) Ownership() (gbfs.FileOwnership, bool) {
	return gbfs.FileOwnership{UID: fi.uid, GID: fi.gid}, true
}

type nullDeviceFile struct {
	path   string
	flag   int
	closed bool
}

func (f *nullDeviceFile) Read(_ []byte) (int, error) {
	if f.closed {
		return 0, stdfs.ErrClosed
	}
	if !canReadVirtualDevice(f.flag) {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}
	return 0, io.EOF
}

func (f *nullDeviceFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, stdfs.ErrClosed
	}
	if !canWriteVirtualDevice(f.flag) {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrPermission}
	}
	return len(p), nil
}

func (f *nullDeviceFile) Close() error {
	f.closed = true
	return nil
}

func (f *nullDeviceFile) Stat() (stdfs.FileInfo, error) {
	if f.closed {
		return nil, stdfs.ErrClosed
	}
	return virtualNullInfo(), nil
}

func canReadVirtualDevice(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY
}

func canWriteVirtualDevice(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR) != 0
}

var _ gbfs.FileSystem = (*virtualDeviceFS)(nil)
var _ gbfs.File = (*nullDeviceFile)(nil)
