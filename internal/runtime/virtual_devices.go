package runtime

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

const (
	virtualDeviceDir            = "/dev"
	virtualFullDevice           = "/dev/full"
	virtualNullDevice           = "/dev/null"
	virtualUrandomDevice        = "/dev/urandom"
	virtualZeroDevice           = "/dev/zero"
	virtualUrandomSeed   uint64 = 0x9e3779b97f4a7c15
)

var virtualDeviceModTime = time.Unix(0, 0).UTC()

// wrapSandboxFileSystem reserves a small runtime-owned /dev namespace above the
// configured sandbox filesystem so core character devices behave consistently
// regardless of the underlying backend.
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
	case abs == virtualFullDevice:
		if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
		}
		return newVirtualDeviceFile(abs, flag), nil
	case abs == virtualNullDevice:
		if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
		}
		return newVirtualDeviceFile(abs, flag), nil
	case abs == virtualUrandomDevice:
		if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
		}
		return newVirtualDeviceFile(abs, flag), nil
	case abs == virtualZeroDevice:
		if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
		}
		return newVirtualDeviceFile(abs, flag), nil
	case abs == virtualDeviceDir || isReservedVirtualDeviceChild(abs):
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
	case abs == virtualFullDevice:
		return virtualFullInfo(), nil
	case abs == virtualNullDevice:
		return virtualNullInfo(), nil
	case abs == virtualUrandomDevice:
		return virtualUrandomInfo(), nil
	case abs == virtualZeroDevice:
		return virtualZeroInfo(), nil
	case isReservedVirtualDeviceChild(abs):
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
	case abs == virtualFullDevice:
		return virtualFullInfo(), nil
	case abs == virtualNullDevice:
		return virtualNullInfo(), nil
	case abs == virtualUrandomDevice:
		return virtualUrandomInfo(), nil
	case abs == virtualZeroDevice:
		return virtualZeroInfo(), nil
	case isReservedVirtualDeviceChild(abs):
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
	case isReservedVirtualDevice(abs) || isReservedVirtualDeviceChild(abs):
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.ReadDir(ctx, abs)
	}
}

func (f *virtualDeviceFS) Readlink(ctx context.Context, name string) (string, error) {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir, isReservedVirtualDevice(abs):
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrInvalid}
	case isReservedVirtualDeviceChild(abs):
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.Readlink(ctx, abs)
	}
}

func (f *virtualDeviceFS) Realpath(ctx context.Context, name string) (string, error) {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir || isReservedVirtualDevice(abs):
		return abs, nil
	case isReservedVirtualDeviceChild(abs):
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
	case isReservedVirtualDevice(abs) || isReservedVirtualDeviceChild(abs):
		return &os.PathError{Op: "mkdir", Path: abs, Err: stdfs.ErrInvalid}
	default:
		return f.base.MkdirAll(ctx, abs, perm)
	}
}

func (f *virtualDeviceFS) Mkfifo(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs := f.resolve(name)
	switch {
	case abs == virtualDeviceDir, isReservedVirtualDevice(abs), isReservedVirtualDeviceChild(abs):
		return &os.PathError{Op: "mkfifo", Path: abs, Err: stdfs.ErrInvalid}
	default:
		if isVirtualDeviceChild(abs) {
			if err := f.ensureVirtualDeviceDir(ctx); err != nil {
				return err
			}
		}
		return gbfsMkfifo(ctx, f.base, abs, perm)
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
	case isReservedVirtualDevice(abs) || isReservedVirtualDeviceChild(abs):
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

func (f *virtualDeviceFS) resolve(name string) string {
	return gbfs.Resolve(f.Getwd(), name)
}

func (f *virtualDeviceFS) ensureVirtualDeviceDir(ctx context.Context) error {
	return f.base.MkdirAll(ctx, virtualDeviceDir, 0o755)
}

func isVirtualDeviceChild(abs string) bool {
	return strings.HasPrefix(abs, virtualDeviceDir+"/") &&
		!isReservedVirtualDevice(abs) &&
		!isReservedVirtualDeviceChild(abs)
}

func isReservedVirtualDevice(abs string) bool {
	return abs == virtualFullDevice || abs == virtualNullDevice || abs == virtualUrandomDevice || abs == virtualZeroDevice
}

func isReservedVirtualDeviceChild(abs string) bool {
	return strings.HasPrefix(abs, virtualFullDevice+"/") ||
		strings.HasPrefix(abs, virtualNullDevice+"/") ||
		strings.HasPrefix(abs, virtualUrandomDevice+"/") ||
		strings.HasPrefix(abs, virtualZeroDevice+"/")
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
		if entry == nil || entry.Name() == "full" || entry.Name() == "null" {
			continue
		}
		byName[entry.Name()] = entry
	}
	byName["full"] = stdfs.FileInfoToDirEntry(virtualFullInfo())
	byName["null"] = stdfs.FileInfoToDirEntry(virtualNullInfo())
	byName["urandom"] = stdfs.FileInfoToDirEntry(virtualUrandomInfo())
	byName["zero"] = stdfs.FileInfoToDirEntry(virtualZeroInfo())
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
	case abs == virtualDeviceDir || isReservedVirtualDevice(abs):
		return &os.PathError{Op: op, Path: abs, Err: stdfs.ErrPermission}
	case isReservedVirtualDeviceChild(abs):
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

func virtualFullInfo() stdfs.FileInfo {
	return virtualFileInfo{
		name:    "full",
		mode:    stdfs.ModeDevice | stdfs.ModeCharDevice | 0o666,
		modTime: virtualDeviceModTime,
		uid:     gbfs.DefaultOwnerUID,
		gid:     gbfs.DefaultOwnerGID,
	}
}

func virtualUrandomInfo() stdfs.FileInfo {
	return virtualFileInfo{
		name:    "urandom",
		mode:    stdfs.ModeDevice | stdfs.ModeCharDevice | 0o666,
		modTime: virtualDeviceModTime,
		uid:     gbfs.DefaultOwnerUID,
		gid:     gbfs.DefaultOwnerGID,
	}
}

func virtualZeroInfo() stdfs.FileInfo {
	return virtualFileInfo{
		name:    "zero",
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

type virtualDeviceFile struct {
	path         string
	flag         int
	urandomState uint64
	closed       atomic.Bool
}

func newVirtualDeviceFile(path string, flag int) *virtualDeviceFile {
	file := &virtualDeviceFile{path: path, flag: flag}
	if path == virtualUrandomDevice {
		file.urandomState = virtualUrandomSeed
	}
	return file
}

func (f *virtualDeviceFile) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !canReadVirtualDevice(f.flag) {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}
	switch f.path {
	case virtualFullDevice:
		clear(p)
		return len(p), nil
	case virtualNullDevice:
		return 0, io.EOF
	case virtualUrandomDevice:
		f.fillUrandom(p)
		return len(p), nil
	case virtualZeroDevice:
		clear(p)
		return len(p), nil
	default:
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrInvalid}
	}
}

func (f *virtualDeviceFile) Write(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !canWriteVirtualDevice(f.flag) {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrPermission}
	}
	if f.path == virtualFullDevice {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: virtualFullWriteErrno()}
	}
	return len(p), nil
}

func (f *virtualDeviceFile) Close() error {
	f.closed.Store(true)
	return nil
}

func (f *virtualDeviceFile) Stat() (stdfs.FileInfo, error) {
	if f.closed.Load() {
		return nil, stdfs.ErrClosed
	}
	switch f.path {
	case virtualFullDevice:
		return virtualFullInfo(), nil
	case virtualNullDevice:
		return virtualNullInfo(), nil
	case virtualUrandomDevice:
		return virtualUrandomInfo(), nil
	case virtualZeroDevice:
		return virtualZeroInfo(), nil
	default:
		return nil, &os.PathError{Op: "stat", Path: f.path, Err: stdfs.ErrInvalid}
	}
}

func canReadVirtualDevice(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY
}

func canWriteVirtualDevice(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR) != 0
}

func virtualFullWriteErrno() error {
	if runtime.GOOS == "darwin" {
		return syscall.EPERM
	}
	return syscall.ENOSPC
}

func (f *virtualDeviceFile) fillUrandom(p []byte) {
	state := f.urandomState
	if state == 0 {
		state = virtualUrandomSeed
	}
	for i := range p {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		p[i] = byte(state >> 56)
	}
	f.urandomState = state
}

var _ gbfs.FileSystem = (*virtualDeviceFS)(nil)

func gbfsMkfifo(ctx context.Context, fsys gbfs.FileSystem, name string, perm stdfs.FileMode) error {
	fifoFS, ok := fsys.(gbfs.FIFOFileSystem)
	if !ok {
		return &os.PathError{Op: "mkfifo", Path: gbfs.Clean(name), Err: stdfs.ErrPermission}
	}
	return fifoFS.Mkfifo(ctx, name, perm)
}

var _ gbfs.File = (*virtualDeviceFile)(nil)
