//go:build !windows && !js

package fs

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ReadWriteFS exposes a mutable host directory as the sandbox root.
//
// All sandbox paths are rooted at "/", but they map onto the configured host
// directory. Path resolution follows host symlinks only when the resolved path
// remains within the configured root.
type ReadWriteFS struct {
	mu sync.RWMutex

	root             string
	canonicalRoot    string
	cwd              string
	maxFileReadBytes int64
	ownership        map[string]FileOwnership
}

type readWriteFile struct {
	file      File
	name      string
	ownership *FileOwnership
}

// NewReadWrite creates a concrete read-write host-backed filesystem instance.
func NewReadWrite(opts ReadWriteOptions) (*ReadWriteFS, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return nil, fmt.Errorf("read-write root is required")
	}

	if !filepath.IsAbs(root) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		root = absRoot
	}
	root = filepath.Clean(root)

	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("read-write root %q is not a directory", root)
	}

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	canonicalRoot = filepath.Clean(canonicalRoot)

	maxFileReadBytes := opts.MaxFileReadBytes
	if maxFileReadBytes == 0 {
		maxFileReadBytes = defaultHostMaxFileReadBytes
	}

	return &ReadWriteFS{
		root:             root,
		canonicalRoot:    canonicalRoot,
		cwd:              "/",
		maxFileReadBytes: maxFileReadBytes,
		ownership:        make(map[string]FileOwnership),
	}, nil
}

func (h *ReadWriteFS) Open(ctx context.Context, name string) (File, error) {
	return h.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (h *ReadWriteFS) OpenFile(_ context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), true, true)
	if err != nil {
		return nil, h.pathError("open", abs, err)
	}
	defer func() { _ = root.Close() }()

	file, err := root.OpenFile(rootRelativeOpenPath(resolved.rel), flag, perm)
	if err != nil {
		return nil, h.pathError("open", abs, err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, h.pathError("open", abs, err)
	}
	if !hasWriteIntent(flag) {
		if err := h.checkFileSize(info); err != nil {
			_ = file.Close()
			return nil, h.pathError("open", abs, err)
		}
	}

	return &readWriteFile{
		file:      file,
		name:      path.Base(abs),
		ownership: h.lookupOwnership(hostPathFromRootRelative(h.canonicalRoot, resolved.rel)),
	}, nil
}

func (h *ReadWriteFS) Stat(_ context.Context, name string) (stdfs.FileInfo, error) {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), true, false)
	if err != nil {
		return nil, h.pathError("stat", abs, err)
	}
	defer func() { _ = root.Close() }()

	info, err := root.Stat(rootRelativeOpenPath(resolved.rel))
	if err != nil {
		return nil, h.pathError("stat", abs, err)
	}
	return namedFileInfo{name: path.Base(abs), info: info, ownership: h.lookupOwnership(hostPathFromRootRelative(h.canonicalRoot, resolved.rel))}, nil
}

func (h *ReadWriteFS) Lstat(_ context.Context, name string) (stdfs.FileInfo, error) {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), false, false)
	if err != nil {
		return nil, h.pathError("lstat", abs, err)
	}
	defer func() { _ = root.Close() }()
	if resolved.info == nil {
		return nil, h.pathError("lstat", abs, stdfs.ErrNotExist)
	}
	return namedFileInfo{name: path.Base(abs), info: resolved.info, ownership: h.lookupOwnership(hostPathFromRootRelative(h.canonicalRoot, resolved.rel))}, nil
}

func (h *ReadWriteFS) ReadDir(_ context.Context, name string) ([]stdfs.DirEntry, error) {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), true, false)
	if err != nil {
		return nil, h.pathError("readdir", abs, err)
	}
	defer func() { _ = root.Close() }()

	file, err := root.Open(rootRelativeOpenPath(resolved.rel))
	if err != nil {
		return nil, h.pathError("readdir", abs, err)
	}
	defer func() { _ = file.Close() }()

	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, h.pathError("readdir", abs, err)
	}
	return entries, nil
}

func (h *ReadWriteFS) Readlink(_ context.Context, name string) (string, error) {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), false, false)
	if err != nil {
		return "", h.pathError("readlink", abs, err)
	}
	defer func() { _ = root.Close() }()
	if resolved.info == nil || resolved.info.Mode()&stdfs.ModeSymlink == 0 {
		return "", h.pathError("readlink", abs, stdfs.ErrInvalid)
	}

	target, err := root.Readlink(rootRelativeOpenPath(resolved.rel))
	if err != nil {
		return "", h.pathError("readlink", abs, err)
	}
	if !filepath.IsAbs(target) {
		return filepath.ToSlash(target), nil
	}

	target = filepath.Clean(target)
	if withinHostRoot(target, h.root) {
		return h.virtualFromRootPath(h.root, target), nil
	}
	if withinHostRoot(target, h.canonicalRoot) {
		return h.virtualFromCanonical(target), nil
	}

	canonical, err := filepath.EvalSymlinks(target)
	if err == nil {
		canonical = filepath.Clean(canonical)
		if withinHostRoot(canonical, h.canonicalRoot) {
			return h.virtualFromCanonical(canonical), nil
		}
	}

	return "", h.pathError("readlink", abs, stdfs.ErrPermission)
}

func (h *ReadWriteFS) Realpath(_ context.Context, name string) (string, error) {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), true, false)
	if err != nil {
		return "", h.pathError("realpath", abs, err)
	}
	_ = root.Close()
	return h.virtualFromCanonical(hostPathFromRootRelative(h.canonicalRoot, resolved.rel)), nil
}

func (h *ReadWriteFS) Symlink(_ context.Context, target, linkName string) error {
	abs := h.resolve(linkName)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), false, true)
	if err != nil {
		return h.pathError("symlink", abs, err)
	}
	defer func() { _ = root.Close() }()

	if err := root.Symlink(h.sanitizeSymlinkTarget(target), rootRelativeOpenPath(resolved.rel)); err != nil {
		return h.pathError("symlink", abs, err)
	}
	return nil
}

func (h *ReadWriteFS) Link(_ context.Context, oldName, newName string) error {
	oldAbs := h.resolve(oldName)
	newAbs := h.resolve(newName)
	root, oldResolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(oldAbs, "/"), true, false)
	if err != nil {
		return h.pathError("link", oldAbs, err)
	}
	defer func() { _ = root.Close() }()

	newResolved, err := resolveWithinRoot(root, h.root, h.canonicalRoot, strings.TrimPrefix(newAbs, "/"), false, true)
	if err != nil {
		return h.pathError("link", newAbs, err)
	}
	if err := root.Link(rootRelativeOpenPath(oldResolved.rel), rootRelativeOpenPath(newResolved.rel)); err != nil {
		return h.pathError("link", oldAbs, err)
	}
	return nil
}

func (h *ReadWriteFS) Chown(_ context.Context, name string, uid, gid uint32, follow bool) error {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), follow, false)
	if err != nil {
		return h.pathError("chown", abs, err)
	}
	defer func() { _ = root.Close() }()

	target := hostPathFromRootRelative(h.canonicalRoot, resolved.rel)
	h.recordOwnership(target, FileOwnership{UID: uid, GID: gid})
	return nil
}

func (h *ReadWriteFS) Chmod(_ context.Context, name string, mode stdfs.FileMode) error {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), true, false)
	if err != nil {
		return h.pathError("chmod", abs, err)
	}
	defer func() { _ = root.Close() }()

	if err := root.Chmod(rootRelativeOpenPath(resolved.rel), mode); err != nil {
		return h.pathError("chmod", abs, err)
	}
	return nil
}

func (h *ReadWriteFS) Chtimes(_ context.Context, name string, atime, mtime time.Time) error {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), true, false)
	if err != nil {
		return h.pathError("chtimes", abs, err)
	}
	defer func() { _ = root.Close() }()

	if err := root.Chtimes(rootRelativeOpenPath(resolved.rel), atime, mtime); err != nil {
		return h.pathError("chtimes", abs, err)
	}
	return nil
}

func (h *ReadWriteFS) Lchtimes(_ context.Context, name string, atime, mtime time.Time) error {
	abs := h.resolve(name)
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), false, false)
	if err != nil {
		return h.pathError("lchtimes", abs, err)
	}
	defer func() { _ = root.Close() }()

	parent, base, err := openResolvedParentFile(root, resolved.rel)
	if err != nil {
		return h.pathError("lchtimes", abs, err)
	}
	defer func() { _ = parent.Close() }()

	times := []unix.Timespec{
		unix.NsecToTimespec(atime.UnixNano()),
		unix.NsecToTimespec(mtime.UnixNano()),
	}
	if err := unix.UtimesNanoAt(int(parent.Fd()), base, times, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return h.pathError("lchtimes", abs, err)
	}
	return nil
}

func (h *ReadWriteFS) MkdirAll(_ context.Context, name string, perm stdfs.FileMode) error {
	abs := h.resolve(name)
	if abs == "/" {
		return nil
	}
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), false, true)
	if err != nil {
		return h.pathError("mkdir", abs, err)
	}
	defer func() { _ = root.Close() }()

	if err := root.MkdirAll(rootRelativeOpenPath(resolved.rel), perm); err != nil {
		return h.pathError("mkdir", abs, err)
	}
	return nil
}

func (h *ReadWriteFS) Mkfifo(_ context.Context, name string, perm stdfs.FileMode) error {
	abs := h.resolve(name)
	target, err := h.resolveLeaf(abs)
	if err != nil {
		return h.pathError("mkfifo", abs, err)
	}

	if perm == 0 {
		perm = 0o666
	}
	if err := syscall.Mkfifo(target, uint32(perm.Perm())); err != nil {
		return h.pathError("mkfifo", abs, err)
	}
	return nil
}

func (h *ReadWriteFS) Remove(_ context.Context, name string, recursive bool) error {
	abs := h.resolve(name)
	if abs == "/" {
		return h.pathError("remove", abs, stdfs.ErrPermission)
	}
	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), false, false)
	if err != nil {
		return h.pathError("remove", abs, err)
	}
	defer func() { _ = root.Close() }()

	target := hostPathFromRootRelative(h.canonicalRoot, resolved.rel)
	if recursive {
		if err := root.RemoveAll(rootRelativeOpenPath(resolved.rel)); err != nil {
			return h.pathError("remove", abs, err)
		}
		h.clearOwnershipSubtree(target)
		return nil
	}
	if err := root.Remove(rootRelativeOpenPath(resolved.rel)); err != nil {
		return h.pathError("remove", abs, err)
	}
	h.clearOwnershipTarget(target)
	return nil
}

func (h *ReadWriteFS) Rename(_ context.Context, oldName, newName string) error {
	oldAbs := h.resolve(oldName)
	newAbs := h.resolve(newName)
	if oldAbs == "/" || newAbs == "/" {
		return h.pathError("rename", oldAbs, stdfs.ErrPermission)
	}
	root, oldResolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(oldAbs, "/"), false, false)
	if err != nil {
		return h.pathError("rename", oldAbs, err)
	}
	defer func() { _ = root.Close() }()

	newResolved, err := resolveWithinRoot(root, h.root, h.canonicalRoot, strings.TrimPrefix(newAbs, "/"), false, true)
	if err != nil {
		return h.pathError("rename", newAbs, err)
	}
	oldTarget := hostPathFromRootRelative(h.canonicalRoot, oldResolved.rel)
	newTarget := hostPathFromRootRelative(h.canonicalRoot, newResolved.rel)
	if err := root.Rename(rootRelativeOpenPath(oldResolved.rel), rootRelativeOpenPath(newResolved.rel)); err != nil {
		return h.pathError("rename", oldAbs, err)
	}
	h.moveOwnership(oldTarget, newTarget)
	return nil
}

func (h *ReadWriteFS) Getwd() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cwd
}

func (h *ReadWriteFS) Chdir(name string) error {
	abs := h.resolve(name)
	if current := h.Getwd(); current != "" && Clean(current) == abs {
		return nil
	}

	root, resolved, err := openResolvedRoot(h.root, h.canonicalRoot, strings.TrimPrefix(abs, "/"), true, false)
	if err != nil {
		if h.canAssumeCurrentProcessDir(abs, err) {
			h.mu.Lock()
			h.cwd = abs
			h.mu.Unlock()
			return nil
		}
		return h.pathError("chdir", abs, err)
	}
	defer func() { _ = root.Close() }()
	if resolved.info == nil || !resolved.info.IsDir() {
		return h.pathError("chdir", abs, stdfs.ErrInvalid)
	}
	h.mu.Lock()
	h.cwd = abs
	h.mu.Unlock()
	return nil
}

func (h *ReadWriteFS) lookupOwnership(target string) *FileOwnership {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ownership, ok := h.ownership[target]
	if !ok {
		return nil
	}
	ownershipCopy := ownership
	return &ownershipCopy
}

func (h *ReadWriteFS) recordOwnership(target string, ownership FileOwnership) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ownership[target] = ownership
}

func (h *ReadWriteFS) clearOwnershipTarget(target string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ownership, filepath.Clean(target))
}

func (h *ReadWriteFS) clearOwnershipSubtree(target string) {
	target = filepath.Clean(target)
	prefix := target + string(os.PathSeparator)

	h.mu.Lock()
	defer h.mu.Unlock()
	for key := range h.ownership {
		if key == target || strings.HasPrefix(key, prefix) {
			delete(h.ownership, key)
		}
	}
}

func (h *ReadWriteFS) moveOwnership(oldTarget, newTarget string) {
	oldTarget = filepath.Clean(oldTarget)
	newTarget = filepath.Clean(newTarget)
	oldPrefix := oldTarget + string(os.PathSeparator)
	newPrefix := newTarget + string(os.PathSeparator)

	h.mu.Lock()
	defer h.mu.Unlock()

	for key := range h.ownership {
		if key == newTarget || strings.HasPrefix(key, newPrefix) {
			delete(h.ownership, key)
		}
	}

	moved := make(map[string]FileOwnership)
	for key, ownership := range h.ownership {
		switch {
		case key == oldTarget:
			moved[newTarget] = ownership
			delete(h.ownership, key)
		case strings.HasPrefix(key, oldPrefix):
			suffix := strings.TrimPrefix(key, oldPrefix)
			moved[filepath.Join(newTarget, suffix)] = ownership
			delete(h.ownership, key)
		}
	}
	maps.Copy(h.ownership, moved)
}
func (h *ReadWriteFS) resolve(name string) string {
	return Resolve(h.Getwd(), name)
}

func (h *ReadWriteFS) resolveLeaf(abs string) (string, error) {
	abs = Clean(abs)
	if abs == "/" {
		return h.canonicalRoot, nil
	}

	lexical := h.lexicalPath(abs)
	parent := filepath.Dir(lexical)
	missingParts := make([]string, 0, 4)
	for {
		canonicalParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			canonicalParent = filepath.Clean(canonicalParent)
			if !withinHostRoot(canonicalParent, h.canonicalRoot) {
				return "", stdfs.ErrPermission
			}
			target := canonicalParent
			for i := len(missingParts) - 1; i >= 0; i-- {
				target = filepath.Join(target, missingParts[i])
			}
			return filepath.Join(target, filepath.Base(lexical)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Clean(parent) == h.root {
			if !withinHostRoot(h.canonicalRoot, h.canonicalRoot) {
				return "", stdfs.ErrPermission
			}
			target := h.canonicalRoot
			for i := len(missingParts) - 1; i >= 0; i-- {
				target = filepath.Join(target, missingParts[i])
			}
			return filepath.Join(target, filepath.Base(lexical)), nil
		}
		missingParts = append(missingParts, filepath.Base(parent))
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		parent = next
	}
}

func (h *ReadWriteFS) lexicalPath(abs string) string {
	if abs == "/" {
		return h.root
	}
	return filepath.Clean(filepath.Join(h.root, filepath.FromSlash(strings.TrimPrefix(abs, "/"))))
}

func (h *ReadWriteFS) canAssumeCurrentProcessDir(abs string, err error) bool {
	if !errors.Is(err, syscall.ENAMETOOLONG) || h.root != h.canonicalRoot {
		return false
	}
	current, getwdErr := os.Getwd()
	if getwdErr != nil {
		return false
	}
	return filepath.Clean(h.lexicalPath(abs)) == filepath.Clean(current)
}

func (h *ReadWriteFS) virtualFromCanonical(canonical string) string {
	rel, err := filepath.Rel(h.canonicalRoot, canonical)
	if err != nil || rel == "." {
		return "/"
	}
	return Resolve("/", filepath.ToSlash(rel))
}

func (h *ReadWriteFS) virtualFromRootPath(rootPath, target string) string {
	rel, err := filepath.Rel(rootPath, target)
	if err != nil || rel == "." {
		return "/"
	}
	return Resolve("/", filepath.ToSlash(rel))
}

func (h *ReadWriteFS) sanitizeSymlinkTarget(target string) string {
	if !strings.HasPrefix(target, "/") {
		return filepath.FromSlash(target)
	}

	virtualTarget := Clean(target)
	if virtualTarget == "/" {
		return h.canonicalRoot
	}
	return filepath.Join(h.canonicalRoot, filepath.FromSlash(strings.TrimPrefix(virtualTarget, "/")))
}

func (f *readWriteFile) Read(p []byte) (int, error) {
	return f.file.Read(p)
}

func (f *readWriteFile) Write(p []byte) (int, error) {
	return f.file.Write(p)
}

func (f *readWriteFile) Close() error {
	return f.file.Close()
}

func (f *readWriteFile) Seek(offset int64, whence int) (int64, error) {
	seeker, ok := f.file.(interface {
		Seek(offset int64, whence int) (int64, error)
	})
	if !ok {
		return 0, stdfs.ErrInvalid
	}
	return seeker.Seek(offset, whence)
}

func (f *readWriteFile) Stat() (stdfs.FileInfo, error) {
	info, err := f.file.Stat()
	if err != nil {
		return nil, err
	}
	return namedFileInfo{name: f.name, info: info, ownership: f.ownership}, nil
}
func (h *ReadWriteFS) checkFileSize(info stdfs.FileInfo) error {
	if h.maxFileReadBytes <= 0 || info == nil || !info.Mode().IsRegular() {
		return nil
	}
	if info.Size() <= h.maxFileReadBytes {
		return nil
	}
	return fileTooLargeError{
		size: info.Size(),
		max:  h.maxFileReadBytes,
	}
}

func (h *ReadWriteFS) pathError(op, name string, err error) error {
	if err == nil {
		return nil
	}
	return &os.PathError{
		Op:   op,
		Path: Clean(name),
		Err:  sanitizeHostErr(err),
	}
}

var _ FileSystem = (*ReadWriteFS)(nil)
var _ FIFOFileSystem = (*ReadWriteFS)(nil)
