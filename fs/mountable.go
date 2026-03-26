package fs

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
	"syscall"
	"time"
)

// MountConfig describes a filesystem mounted at a sandbox path.
type MountConfig struct {
	MountPoint string
	Factory    Factory
}

// MountedFS is a snapshot of a live mount table entry.
type MountedFS struct {
	MountPoint string
	FileSystem FileSystem
}

// MountableOptions configures a multi-mount filesystem factory.
type MountableOptions struct {
	Base   Factory
	Mounts []MountConfig
}

type mountableFactory struct {
	base   Factory
	mounts []MountConfig
}

func (f mountableFactory) New(ctx context.Context) (FileSystem, error) {
	baseFactory := f.base
	if baseFactory == nil {
		baseFactory = Memory()
	}
	base, err := baseFactory.New(ctx)
	if err != nil {
		return nil, err
	}

	mountable := NewMountable(base)
	for _, cfg := range f.mounts {
		if cfg.Factory == nil {
			return nil, fmt.Errorf("mount %q: factory is nil", cfg.MountPoint)
		}
		fsys, err := cfg.Factory.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("mount %q: %w", cfg.MountPoint, err)
		}
		if err := mountable.Mount(cfg.MountPoint, fsys); err != nil {
			return nil, err
		}
	}
	return mountable, nil
}

// Mountable returns a factory that provisions a multi-mount filesystem.
func Mountable(opts MountableOptions) Factory {
	mounts := make([]MountConfig, 0, len(opts.Mounts))
	for _, cfg := range opts.Mounts {
		mounts = append(mounts, MountConfig{
			MountPoint: cfg.MountPoint,
			Factory:    cfg.Factory,
		})
	}
	return mountableFactory{
		base:   opts.Base,
		mounts: mounts,
	}
}

// MountableFS routes sandbox paths across a base filesystem and mounted child
// filesystems.
type MountableFS struct {
	mu     sync.RWMutex
	base   FileSystem
	cwd    string
	mounts map[string]mountedEntry
}

type mountedEntry struct {
	mountPoint string
	fs         FileSystem
}

// NewMountable creates a mountable filesystem over base.
func NewMountable(base FileSystem) *MountableFS {
	if base == nil {
		base = NewMemory()
	}
	return &MountableFS{
		base:   base,
		cwd:    "/",
		mounts: make(map[string]mountedEntry),
	}
}

// Mount mounts fs at mountPoint.
func (m *MountableFS) Mount(mountPoint string, fsys FileSystem) error {
	if fsys == nil {
		return fmt.Errorf("mount %q: filesystem is nil", mountPoint)
	}
	if err := validateMountPath(mountPoint); err != nil {
		return err
	}
	normalized := Clean(strings.TrimSpace(mountPoint))
	if normalized == "/" {
		return fmt.Errorf("cannot mount at root %q", normalized)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateMountLocked(normalized); err != nil {
		return err
	}
	m.mounts[normalized] = mountedEntry{
		mountPoint: normalized,
		fs:         fsys,
	}
	return nil
}

// Unmount removes the filesystem mounted at mountPoint.
func (m *MountableFS) Unmount(mountPoint string) error {
	normalized := Clean(strings.TrimSpace(mountPoint))

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.mounts[normalized]
	if !ok {
		return fmt.Errorf("no filesystem mounted at %q", mountPoint)
	}
	if pathWithinMount(m.cwd, entry.mountPoint) {
		return &os.PathError{Op: "unmount", Path: normalized, Err: syscall.EBUSY}
	}
	delete(m.mounts, normalized)
	return nil
}

// Mounts returns a stable snapshot of the current mount table.
func (m *MountableFS) Mounts() []MountedFS {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]MountedFS, 0, len(m.mounts))
	for _, entry := range m.mounts {
		out = append(out, MountedFS{
			MountPoint: entry.mountPoint,
			FileSystem: entry.fs,
		})
	}
	slices.SortFunc(out, func(a, b MountedFS) int {
		return strings.Compare(a.MountPoint, b.MountPoint)
	})
	return out
}

// IsMountPoint reports whether path is an exact mount point.
func (m *MountableFS) IsMountPoint(pathValue string) bool {
	normalized := Clean(pathValue)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.mounts[normalized]
	return ok
}

func (m *MountableFS) Open(ctx context.Context, name string) (File, error) {
	return m.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (m *MountableFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	abs := m.resolve(name)
	fsys, rel := m.routedView(abs)
	return fsys.OpenFile(ctx, rel, flag, perm)
}

func (m *MountableFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs := m.resolve(name)
	entry, rel, mounted, synthetic := m.route(abs)
	switch {
	case mounted && rel == "/":
		info, err := entry.fs.Stat(ctx, "/")
		if err == nil {
			return remapFileInfoName(path.Base(abs), info), nil
		}
		return syntheticDirInfo(abs), nil
	case mounted:
		return namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}.Stat(ctx, rel)
	case synthetic:
		info, err := m.base.Stat(ctx, abs)
		if err == nil {
			return info, nil
		}
		if errors.Is(err, stdfs.ErrNotExist) {
			return syntheticDirInfo(abs), nil
		}
		return nil, err
	default:
		return m.base.Stat(ctx, abs)
	}
}

func (m *MountableFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs := m.resolve(name)
	entry, rel, mounted, synthetic := m.route(abs)
	switch {
	case mounted && rel == "/":
		info, err := entry.fs.Lstat(ctx, "/")
		if err == nil {
			return remapFileInfoName(path.Base(abs), info), nil
		}
		return syntheticDirInfo(abs), nil
	case mounted:
		return namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}.Lstat(ctx, rel)
	case synthetic:
		info, err := m.base.Lstat(ctx, abs)
		if err == nil {
			return info, nil
		}
		if errors.Is(err, stdfs.ErrNotExist) {
			return syntheticDirInfo(abs), nil
		}
		return nil, err
	default:
		return m.base.Lstat(ctx, abs)
	}
}

func (m *MountableFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	abs := m.resolve(name)
	entry, rel, mounted, synthetic := m.route(abs)
	switch {
	case mounted:
		return namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}.ReadDir(ctx, rel)
	case !synthetic:
		return m.base.ReadDir(ctx, abs)
	}

	entriesByName := make(map[string]stdfs.DirEntry)
	baseEntries, err := m.base.ReadDir(ctx, abs)
	switch {
	case err == nil:
		for _, dirEntry := range baseEntries {
			entriesByName[dirEntry.Name()] = dirEntry
		}
	case errors.Is(err, stdfs.ErrNotExist):
	default:
		return nil, err
	}

	for _, child := range m.childMountNames(abs) {
		if _, ok := entriesByName[child]; ok {
			continue
		}
		entriesByName[child] = stdfs.FileInfoToDirEntry(syntheticDirInfo(joinChildPath(abs, child)))
	}

	names := make([]string, 0, len(entriesByName))
	for name := range entriesByName {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]stdfs.DirEntry, 0, len(names))
	for _, name := range names {
		out = append(out, entriesByName[name])
	}
	return out, nil
}

func (m *MountableFS) Readlink(ctx context.Context, name string) (string, error) {
	abs := m.resolve(name)
	entry, rel, mounted, _ := m.route(abs)
	if mounted {
		return namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}.Readlink(ctx, rel)
	}
	return m.base.Readlink(ctx, abs)
}

func (m *MountableFS) Realpath(ctx context.Context, name string) (string, error) {
	abs := m.resolve(name)
	entry, rel, mounted, synthetic := m.route(abs)
	switch {
	case mounted && rel == "/":
		return entry.mountPoint, nil
	case mounted:
		return namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}.Realpath(ctx, rel)
	case synthetic:
		resolved, err := m.base.Realpath(ctx, abs)
		if err == nil {
			return resolved, nil
		}
		if errors.Is(err, stdfs.ErrNotExist) {
			return abs, nil
		}
		return "", err
	default:
		return m.base.Realpath(ctx, abs)
	}
}

func (m *MountableFS) Symlink(ctx context.Context, target, linkName string) error {
	abs := m.resolve(linkName)
	fsys, rel := m.routedView(abs)
	return fsys.Symlink(ctx, target, rel)
}

func (m *MountableFS) Link(ctx context.Context, oldName, newName string) error {
	oldAbs := m.resolve(oldName)
	newAbs := m.resolve(newName)
	oldEntry, oldRel, oldMounted, _ := m.route(oldAbs)
	newEntry, newRel, newMounted, _ := m.route(newAbs)
	if oldMounted != newMounted || (oldMounted && oldEntry.mountPoint != newEntry.mountPoint) {
		return &os.LinkError{Op: "link", Old: oldAbs, New: newAbs, Err: syscall.EXDEV}
	}
	if oldMounted {
		return namespacedFS{mountPoint: oldEntry.mountPoint, inner: oldEntry.fs}.Link(ctx, oldRel, newRel)
	}
	return m.base.Link(ctx, oldRel, newRel)
}

func (m *MountableFS) Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error {
	abs := m.resolve(name)
	fsys, rel := m.routedView(abs)
	return fsys.Chown(ctx, rel, uid, gid, follow)
}

func (m *MountableFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	abs := m.resolve(name)
	fsys, rel := m.routedView(abs)
	return fsys.Chmod(ctx, rel, mode)
}

func (m *MountableFS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	abs := m.resolve(name)
	fsys, rel := m.routedView(abs)
	return fsys.Chtimes(ctx, rel, atime, mtime)
}

func (m *MountableFS) Lchtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	abs := m.resolve(name)
	fsys, rel := m.routedView(abs)
	raw, ok := fsys.(interface {
		Lchtimes(context.Context, string, time.Time, time.Time) error
	})
	if !ok {
		return fsys.Chtimes(ctx, rel, atime, mtime)
	}
	return raw.Lchtimes(ctx, rel, atime, mtime)
}

func (m *MountableFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs := m.resolve(name)
	_, _, mounted, synthetic := m.route(abs)
	if synthetic {
		if info, err := m.base.Stat(ctx, abs); err == nil {
			if !info.IsDir() {
				return &os.PathError{Op: "mkdir", Path: abs, Err: stdfs.ErrInvalid}
			}
		} else if !errors.Is(err, stdfs.ErrNotExist) {
			return err
		}
		return nil
	}
	fsys, rel := m.routedView(abs)
	if mounted && rel == "/" {
		return nil
	}
	return fsys.MkdirAll(ctx, rel, perm)
}

func (m *MountableFS) Mkfifo(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs := m.resolve(name)
	entry, rel, mounted, synthetic := m.route(abs)
	if synthetic {
		if _, err := m.Stat(ctx, abs); err == nil {
			return &os.PathError{Op: "mkfifo", Path: abs, Err: stdfs.ErrExist}
		} else if !errors.Is(err, stdfs.ErrNotExist) {
			return err
		}
	}
	if mounted && rel == "/" {
		return &os.PathError{Op: "mkfifo", Path: abs, Err: stdfs.ErrExist}
	}
	if mounted {
		return namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}.Mkfifo(ctx, rel, perm)
	}
	return mkfifoViaOptional(ctx, m.base, abs, perm)
}

func (m *MountableFS) Remove(ctx context.Context, name string, recursive bool) error {
	abs := m.resolve(name)
	if m.pathContainsMount(abs) {
		return &os.PathError{Op: "remove", Path: abs, Err: syscall.EBUSY}
	}
	fsys, rel := m.routedView(abs)
	return fsys.Remove(ctx, rel, recursive)
}

func (m *MountableFS) Rename(ctx context.Context, oldName, newName string) error {
	oldAbs := m.resolve(oldName)
	newAbs := m.resolve(newName)
	if m.pathContainsMount(oldAbs) {
		return &os.PathError{Op: "rename", Path: oldAbs, Err: syscall.EBUSY}
	}
	if m.pathContainsMount(newAbs) {
		return &os.PathError{Op: "rename", Path: newAbs, Err: syscall.EBUSY}
	}

	oldEntry, oldRel, oldMounted, _ := m.route(oldAbs)
	newEntry, newRel, newMounted, _ := m.route(newAbs)
	if oldMounted == newMounted && (!oldMounted || oldEntry.mountPoint == newEntry.mountPoint) {
		if oldMounted {
			return namespacedFS{mountPoint: oldEntry.mountPoint, inner: oldEntry.fs}.Rename(ctx, oldRel, newRel)
		}
		return m.base.Rename(ctx, oldRel, newRel)
	}

	src := m.base
	if oldMounted {
		src = namespacedFS{mountPoint: oldEntry.mountPoint, inner: oldEntry.fs}
	}
	dst := m.base
	if newMounted {
		dst = namespacedFS{mountPoint: newEntry.mountPoint, inner: newEntry.fs}
	}
	if err := copyPathInto(ctx, src, oldRel, dst, newRel); err != nil {
		return err
	}
	return src.Remove(ctx, oldRel, true)
}

func (m *MountableFS) Getwd() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cwd
}

func (m *MountableFS) Chdir(name string) error {
	abs := m.resolve(name)
	entry, rel, mounted, synthetic := m.route(abs)

	switch {
	case mounted:
		info, err := namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}.Stat(context.Background(), rel)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
		}
	case synthetic:
		if info, err := m.base.Stat(context.Background(), abs); err == nil {
			if !info.IsDir() {
				return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
			}
		} else if !errors.Is(err, stdfs.ErrNotExist) {
			return err
		}
	default:
		info, err := m.base.Stat(context.Background(), abs)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cwd = abs
	return nil
}

func (m *MountableFS) resolve(name string) string {
	return Resolve(m.Getwd(), name)
}

func (m *MountableFS) route(abs string) (entry mountedEntry, rel string, mounted, synthetic bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, rel, mounted = m.routeLocked(abs)
	synthetic = m.isSyntheticParentLocked(abs)
	return entry, rel, mounted, synthetic
}

func (m *MountableFS) routedView(abs string) (fsys FileSystem, rel string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, rel, mounted := m.routeLocked(abs)
	if mounted {
		fsys = namespacedFS{mountPoint: entry.mountPoint, inner: entry.fs}
		return fsys, rel
	}
	return m.base, rel
}

func (m *MountableFS) routeLocked(abs string) (mountedEntry, string, bool) {
	abs = Clean(abs)
	if entry, ok := m.mounts[abs]; ok {
		return entry, "/", true
	}
	for mountPoint, entry := range m.mounts {
		if strings.HasPrefix(abs, mountPoint+"/") {
			return entry, strings.TrimPrefix(abs, mountPoint), true
		}
	}
	return mountedEntry{}, abs, false
}

func (m *MountableFS) validateMountLocked(mountPoint string) error {
	for existing := range m.mounts {
		if existing == mountPoint {
			continue
		}
		if strings.HasPrefix(mountPoint, existing+"/") {
			return fmt.Errorf("cannot mount at %q: inside existing mount %q", mountPoint, existing)
		}
		if strings.HasPrefix(existing, mountPoint+"/") {
			return fmt.Errorf("cannot mount at %q: would contain existing mount %q", mountPoint, existing)
		}
	}
	return nil
}

func (m *MountableFS) childMountNames(abs string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.childMountNamesLocked(abs)
}

func (m *MountableFS) childMountNamesLocked(abs string) []string {
	abs = Clean(abs)
	prefix := abs
	if prefix == "/" {
		prefix = ""
	}
	children := make(map[string]struct{})
	for mountPoint := range m.mounts {
		if prefix != "" && !strings.HasPrefix(mountPoint, prefix+"/") {
			continue
		}
		remainder := strings.TrimPrefix(mountPoint, prefix+"/")
		if prefix == "" {
			remainder = strings.TrimPrefix(mountPoint, "/")
		}
		child := strings.SplitN(remainder, "/", 2)[0]
		if child != "" {
			children[child] = struct{}{}
		}
	}
	names := make([]string, 0, len(children))
	for child := range children {
		names = append(names, child)
	}
	slices.Sort(names)
	return names
}

func (m *MountableFS) isSyntheticParentLocked(abs string) bool {
	if _, ok := m.mounts[abs]; ok {
		return false
	}
	for mountPoint := range m.mounts {
		if strings.HasPrefix(mountPoint, abs+"/") || abs == "/" {
			if abs == "/" || strings.HasPrefix(mountPoint, abs+"/") {
				return true
			}
		}
	}
	return false
}

func (m *MountableFS) pathContainsMount(abs string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if abs == "/" {
		return len(m.mounts) > 0
	}
	for mountPoint := range m.mounts {
		if mountPoint == abs || strings.HasPrefix(mountPoint, abs+"/") {
			return true
		}
	}
	return false
}

func pathWithinMount(pathValue, mountPoint string) bool {
	return pathValue == mountPoint || strings.HasPrefix(pathValue, mountPoint+"/")
}

func validateMountPath(mountPoint string) error {
	mountPoint = strings.TrimSpace(mountPoint)
	if mountPoint == "" {
		return fmt.Errorf("mount point must be absolute: %q", mountPoint)
	}
	if !strings.HasPrefix(mountPoint, "/") {
		return fmt.Errorf("mount point must be absolute: %q", mountPoint)
	}
	for segment := range strings.SplitSeq(mountPoint, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("invalid mount point %q: contains '.' or '..' segments", mountPoint)
		}
	}
	if Clean(mountPoint) == "/" {
		return fmt.Errorf("cannot mount at root %q", mountPoint)
	}
	return nil
}

type namespacedFS struct {
	mountPoint string
	inner      FileSystem
}

func (f namespacedFS) Open(ctx context.Context, name string) (File, error) {
	file, err := f.inner.Open(ctx, name)
	return file, namespaceError(err, f.mountPoint)
}

func (f namespacedFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	file, err := f.inner.OpenFile(ctx, name, flag, perm)
	return file, namespaceError(err, f.mountPoint)
}

func (f namespacedFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	info, err := f.inner.Stat(ctx, name)
	return info, namespaceError(err, f.mountPoint)
}

func (f namespacedFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	info, err := f.inner.Lstat(ctx, name)
	return info, namespaceError(err, f.mountPoint)
}

func (f namespacedFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	entries, err := f.inner.ReadDir(ctx, name)
	return entries, namespaceError(err, f.mountPoint)
}

func (f namespacedFS) Readlink(ctx context.Context, name string) (string, error) {
	target, err := f.inner.Readlink(ctx, name)
	return target, namespaceError(err, f.mountPoint)
}

func (f namespacedFS) Realpath(ctx context.Context, name string) (string, error) {
	resolved, err := f.inner.Realpath(ctx, name)
	if err != nil {
		return "", namespaceError(err, f.mountPoint)
	}
	return prefixMountPath(f.mountPoint, resolved), nil
}

func (f namespacedFS) Symlink(ctx context.Context, target, linkName string) error {
	return namespaceError(f.inner.Symlink(ctx, target, linkName), f.mountPoint)
}

func (f namespacedFS) Link(ctx context.Context, oldName, newName string) error {
	return namespaceError(f.inner.Link(ctx, oldName, newName), f.mountPoint)
}

func (f namespacedFS) Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error {
	return namespaceError(f.inner.Chown(ctx, name, uid, gid, follow), f.mountPoint)
}

func (f namespacedFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	return namespaceError(f.inner.Chmod(ctx, name, mode), f.mountPoint)
}

func (f namespacedFS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	return namespaceError(f.inner.Chtimes(ctx, name, atime, mtime), f.mountPoint)
}

func (f namespacedFS) Lchtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	raw, ok := f.inner.(interface {
		Lchtimes(context.Context, string, time.Time, time.Time) error
	})
	if !ok {
		return namespaceError(f.inner.Chtimes(ctx, name, atime, mtime), f.mountPoint)
	}
	return namespaceError(raw.Lchtimes(ctx, name, atime, mtime), f.mountPoint)
}

func (f namespacedFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	return namespaceError(f.inner.MkdirAll(ctx, name, perm), f.mountPoint)
}

func (f namespacedFS) Mkfifo(ctx context.Context, name string, perm stdfs.FileMode) error {
	return namespaceError(mkfifoViaOptional(ctx, f.inner, name, perm), f.mountPoint)
}

func (f namespacedFS) Remove(ctx context.Context, name string, recursive bool) error {
	return namespaceError(f.inner.Remove(ctx, name, recursive), f.mountPoint)
}

func (f namespacedFS) Rename(ctx context.Context, oldName, newName string) error {
	return namespaceError(f.inner.Rename(ctx, oldName, newName), f.mountPoint)
}

func (f namespacedFS) Getwd() string {
	return prefixMountPath(f.mountPoint, f.inner.Getwd())
}

func (f namespacedFS) Chdir(name string) error {
	return namespaceError(f.inner.Chdir(name), f.mountPoint)
}

func namespaceError(err error, mountPoint string) error {
	if err == nil || mountPoint == "" {
		return err
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return &os.PathError{
			Op:   pathErr.Op,
			Path: prefixMountPath(mountPoint, pathErr.Path),
			Err:  pathErr.Err,
		}
	}

	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return &os.LinkError{
			Op:  linkErr.Op,
			Old: prefixMountPath(mountPoint, linkErr.Old),
			New: prefixMountPath(mountPoint, linkErr.New),
			Err: linkErr.Err,
		}
	}

	return err
}

func prefixMountPath(mountPoint, pathValue string) string {
	pathValue = Clean(pathValue)
	if pathValue == "/" {
		return mountPoint
	}
	return Resolve(mountPoint, strings.TrimPrefix(pathValue, "/"))
}

func syntheticDirInfo(abs string) stdfs.FileInfo {
	name := path.Base(abs)
	if abs == "/" {
		name = "/"
	}
	return mountableStaticInfo{
		name:    name,
		mode:    stdfs.ModeDir | 0o755,
		modTime: time.Time{},
	}
}

func remapFileInfoName(name string, info stdfs.FileInfo) stdfs.FileInfo {
	return mountableNamedInfo{name: name, info: info}
}

type mountableNamedInfo struct {
	name string
	info stdfs.FileInfo
}

func (i mountableNamedInfo) Name() string         { return i.name }
func (i mountableNamedInfo) Size() int64          { return i.info.Size() }
func (i mountableNamedInfo) Mode() stdfs.FileMode { return i.info.Mode() }
func (i mountableNamedInfo) ModTime() time.Time   { return i.info.ModTime() }
func (i mountableNamedInfo) IsDir() bool          { return i.info.IsDir() }
func (i mountableNamedInfo) Sys() any             { return i.info.Sys() }
func (i mountableNamedInfo) Ownership() (FileOwnership, bool) {
	return OwnershipFromFileInfo(i.info)
}

type mountableStaticInfo struct {
	name    string
	size    int64
	mode    stdfs.FileMode
	modTime time.Time
}

func (i mountableStaticInfo) Name() string         { return i.name }
func (i mountableStaticInfo) Size() int64          { return i.size }
func (i mountableStaticInfo) Mode() stdfs.FileMode { return i.mode }
func (i mountableStaticInfo) ModTime() time.Time   { return i.modTime }
func (i mountableStaticInfo) IsDir() bool          { return i.mode.IsDir() }
func (i mountableStaticInfo) Sys() any             { return nil }
func (i mountableStaticInfo) Ownership() (FileOwnership, bool) {
	return DefaultOwnership(), true
}

var _ FileSystem = (*MountableFS)(nil)
var _ FIFOFileSystem = (*MountableFS)(nil)
