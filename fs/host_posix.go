//go:build !windows && !js

package fs

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// HostFS exposes a read-only host directory at a virtual sandbox path.
type HostFS struct {
	mu sync.RWMutex

	root             string
	canonicalRoot    string
	virtualRoot      string
	virtualRootParts []string
	cwd              string
	maxFileReadBytes int64
}

type hostPathKind uint8

const (
	hostPathNone hostPathKind = iota
	hostPathAncestor
	hostPathMounted
)

// NewHost creates a concrete host-backed filesystem instance.
func NewHost(opts HostOptions) (*HostFS, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return nil, fmt.Errorf("host root is required")
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
		return nil, fmt.Errorf("host root %q is not a directory", root)
	}

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	canonicalRoot = filepath.Clean(canonicalRoot)

	virtualRoot := strings.TrimSpace(opts.VirtualRoot)
	switch {
	case virtualRoot == "":
		virtualRoot = defaultHostVirtualRoot
	case !strings.HasPrefix(virtualRoot, "/"):
		return nil, fmt.Errorf("virtual root must be absolute: %q", opts.VirtualRoot)
	default:
		virtualRoot = Clean(virtualRoot)
	}

	maxFileReadBytes := opts.MaxFileReadBytes
	if maxFileReadBytes == 0 {
		maxFileReadBytes = defaultHostMaxFileReadBytes
	}

	return &HostFS{
		root:             root,
		canonicalRoot:    canonicalRoot,
		virtualRoot:      virtualRoot,
		virtualRootParts: splitVirtualPath(virtualRoot),
		cwd:              virtualRoot,
		maxFileReadBytes: maxFileReadBytes,
	}, nil
}

func (h *HostFS) Open(ctx context.Context, name string) (File, error) {
	return h.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (h *HostFS) OpenFile(_ context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	abs := h.resolve(name)
	if hasWriteIntent(flag) {
		return nil, h.readOnlyError("open", abs)
	}

	switch kind, _ := h.classify(abs); kind {
	case hostPathMounted:
	case hostPathAncestor:
		return nil, h.pathError("open", abs, stdfs.ErrInvalid)
	default:
		return nil, h.pathError("open", abs, stdfs.ErrNotExist)
	}

	canonical, err := h.resolveMountedCanonical(abs)
	if err != nil {
		return nil, h.pathError("open", abs, err)
	}

	file, err := os.OpenFile(canonical, flag, perm)
	if err != nil {
		return nil, h.pathError("open", abs, err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, h.pathError("open", abs, err)
	}
	if err := h.checkFileSize(info); err != nil {
		_ = file.Close()
		return nil, h.pathError("open", abs, err)
	}

	return file, nil
}

func (h *HostFS) Stat(_ context.Context, name string) (stdfs.FileInfo, error) {
	abs := h.resolve(name)

	switch kind, _ := h.classify(abs); kind {
	case hostPathAncestor:
		return h.syntheticDirInfo(abs), nil
	case hostPathMounted:
		canonical, err := h.resolveMountedCanonical(abs)
		if err != nil {
			return nil, h.pathError("stat", abs, err)
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return nil, h.pathError("stat", abs, err)
		}
		return namedFileInfo{name: path.Base(abs), info: info}, nil
	default:
		return nil, h.pathError("stat", abs, stdfs.ErrNotExist)
	}
}

func (h *HostFS) Lstat(_ context.Context, name string) (stdfs.FileInfo, error) {
	abs := h.resolve(name)

	switch kind, _ := h.classify(abs); kind {
	case hostPathAncestor:
		return h.syntheticDirInfo(abs), nil
	case hostPathMounted:
		if abs == h.virtualRoot {
			info, err := os.Stat(h.canonicalRoot)
			if err != nil {
				return nil, h.pathError("lstat", abs, err)
			}
			return namedFileInfo{name: path.Base(abs), info: info}, nil
		}
		leaf, err := h.resolveMountedLeaf(abs)
		if err != nil {
			return nil, h.pathError("lstat", abs, err)
		}
		info, err := os.Lstat(leaf)
		if err != nil {
			return nil, h.pathError("lstat", abs, err)
		}
		return namedFileInfo{name: path.Base(abs), info: info}, nil
	default:
		return nil, h.pathError("lstat", abs, stdfs.ErrNotExist)
	}
}

func (h *HostFS) ReadDir(_ context.Context, name string) ([]stdfs.DirEntry, error) {
	abs := h.resolve(name)

	switch kind, rel := h.classify(abs); kind {
	case hostPathAncestor:
		child, ok := h.syntheticChild(abs)
		if !ok {
			return nil, h.pathError("readdir", abs, stdfs.ErrNotExist)
		}
		entry := stdfs.FileInfoToDirEntry(staticFileInfo{
			name:    child,
			mode:    stdfs.ModeDir | 0o755,
			modTime: time.Time{},
		})
		return []stdfs.DirEntry{entry}, nil
	case hostPathMounted:
		canonical, err := h.resolveMountedCanonical(abs)
		if err != nil {
			return nil, h.pathError("readdir", abs, err)
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return nil, h.pathError("readdir", abs, err)
		}
		if !info.IsDir() {
			return nil, h.pathError("readdir", abs, stdfs.ErrInvalid)
		}
		entries, err := os.ReadDir(canonical)
		if err != nil {
			return nil, h.pathError("readdir", abs, err)
		}
		if rel == "/" {
			return entries, nil
		}
		return entries, nil
	default:
		return nil, h.pathError("readdir", abs, stdfs.ErrNotExist)
	}
}

func (h *HostFS) Readlink(_ context.Context, name string) (string, error) {
	abs := h.resolve(name)

	switch kind, _ := h.classify(abs); kind {
	case hostPathMounted:
		if abs == h.virtualRoot {
			return "", h.pathError("readlink", abs, stdfs.ErrInvalid)
		}
		leaf, err := h.resolveMountedLeaf(abs)
		if err != nil {
			return "", h.pathError("readlink", abs, err)
		}
		target, err := os.Readlink(leaf)
		if err != nil {
			return "", h.pathError("readlink", abs, err)
		}
		if !filepath.IsAbs(target) {
			return filepath.ToSlash(target), nil
		}
		virtualTarget, err := h.virtualizeAbsoluteTarget(target)
		if err != nil {
			return "", h.pathError("readlink", abs, err)
		}
		return virtualTarget, nil
	case hostPathAncestor:
		return "", h.pathError("readlink", abs, stdfs.ErrInvalid)
	default:
		return "", h.pathError("readlink", abs, stdfs.ErrNotExist)
	}
}

func (h *HostFS) Realpath(_ context.Context, name string) (string, error) {
	abs := h.resolve(name)

	switch kind, _ := h.classify(abs); kind {
	case hostPathAncestor:
		return abs, nil
	case hostPathMounted:
		canonical, err := h.resolveMountedCanonical(abs)
		if err != nil {
			return "", h.pathError("realpath", abs, err)
		}
		return h.virtualFromCanonical(canonical), nil
	default:
		return "", h.pathError("realpath", abs, stdfs.ErrNotExist)
	}
}

func (h *HostFS) Symlink(_ context.Context, _, linkName string) error {
	return h.readOnlyError("symlink", h.resolve(linkName))
}

func (h *HostFS) Link(_ context.Context, _, newName string) error {
	return h.readOnlyError("link", h.resolve(newName))
}

func (h *HostFS) Chown(_ context.Context, name string, _, _ uint32, _ bool) error {
	return h.readOnlyError("chown", h.resolve(name))
}

func (h *HostFS) Chmod(_ context.Context, name string, _ stdfs.FileMode) error {
	return h.readOnlyError("chmod", h.resolve(name))
}

func (h *HostFS) Chtimes(_ context.Context, name string, _, _ time.Time) error {
	return h.readOnlyError("chtimes", h.resolve(name))
}

func (h *HostFS) MkdirAll(_ context.Context, name string, _ stdfs.FileMode) error {
	return h.readOnlyError("mkdir", h.resolve(name))
}

func (h *HostFS) Remove(_ context.Context, name string, _ bool) error {
	return h.readOnlyError("remove", h.resolve(name))
}

func (h *HostFS) Rename(_ context.Context, oldName, _ string) error {
	return h.readOnlyError("rename", h.resolve(oldName))
}

func (h *HostFS) Getwd() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cwd
}

func (h *HostFS) Chdir(name string) error {
	abs := h.resolve(name)

	switch kind, _ := h.classify(abs); kind {
	case hostPathAncestor:
		h.mu.Lock()
		h.cwd = abs
		h.mu.Unlock()
		return nil
	case hostPathMounted:
		canonical, err := h.resolveMountedCanonical(abs)
		if err != nil {
			return h.pathError("chdir", abs, err)
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return h.pathError("chdir", abs, err)
		}
		if !info.IsDir() {
			return h.pathError("chdir", abs, stdfs.ErrInvalid)
		}
		h.mu.Lock()
		h.cwd = abs
		h.mu.Unlock()
		return nil
	default:
		return h.pathError("chdir", abs, stdfs.ErrNotExist)
	}
}

func (h *HostFS) resolve(name string) string {
	return Resolve(h.Getwd(), name)
}

func (h *HostFS) classify(abs string) (kind hostPathKind, rel string) {
	abs = Clean(abs)
	if h.virtualRoot == "/" {
		return hostPathMounted, abs
	}
	if abs == h.virtualRoot {
		return hostPathMounted, "/"
	}
	if strings.HasPrefix(abs, h.virtualRoot+"/") {
		return hostPathMounted, strings.TrimPrefix(abs, h.virtualRoot)
	}
	if abs == "/" || strings.HasPrefix(h.virtualRoot, abs+"/") {
		return hostPathAncestor, ""
	}
	return hostPathNone, ""
}

func (h *HostFS) syntheticChild(abs string) (string, bool) {
	if h.virtualRoot == "/" {
		return "", false
	}
	abs = Clean(abs)
	if abs == h.virtualRoot {
		return "", false
	}
	depth := 0
	if abs != "/" {
		depth = len(splitVirtualPath(abs))
	}
	if depth >= len(h.virtualRootParts) {
		return "", false
	}
	return h.virtualRootParts[depth], true
}

func (h *HostFS) syntheticDirInfo(abs string) stdfs.FileInfo {
	name := path.Base(abs)
	if abs == "/" {
		name = "/"
	}
	return staticFileInfo{
		name:    name,
		mode:    stdfs.ModeDir | 0o755,
		modTime: time.Time{},
	}
}

func (h *HostFS) resolveMountedCanonical(abs string) (string, error) {
	switch kind, rel := h.classify(abs); kind {
	case hostPathMounted:
		if rel == "/" {
			return h.canonicalRoot, nil
		}
		lexical := h.lexicalPath(rel)
		canonical, err := filepath.EvalSymlinks(lexical)
		if err != nil {
			return "", err
		}
		canonical = filepath.Clean(canonical)
		if !withinHostRoot(canonical, h.canonicalRoot) {
			return "", stdfs.ErrPermission
		}
		return canonical, nil
	default:
		return "", stdfs.ErrNotExist
	}
}

func (h *HostFS) resolveMountedLeaf(abs string) (string, error) {
	switch kind, rel := h.classify(abs); kind {
	case hostPathMounted:
		if rel == "/" {
			return h.canonicalRoot, nil
		}
		lexical := h.lexicalPath(rel)
		parent := filepath.Dir(lexical)
		canonicalParent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return "", err
		}
		canonicalParent = filepath.Clean(canonicalParent)
		if !withinHostRoot(canonicalParent, h.canonicalRoot) {
			return "", stdfs.ErrPermission
		}
		return filepath.Join(canonicalParent, filepath.Base(lexical)), nil
	default:
		return "", stdfs.ErrNotExist
	}
}

func (h *HostFS) lexicalPath(rel string) string {
	if rel == "/" {
		return h.root
	}
	return filepath.Clean(filepath.Join(h.root, filepath.FromSlash(strings.TrimPrefix(rel, "/"))))
}

func (h *HostFS) virtualFromCanonical(canonical string) string {
	rel, err := filepath.Rel(h.canonicalRoot, canonical)
	if err != nil || rel == "." {
		return h.virtualRoot
	}
	return Resolve(h.virtualRoot, filepath.ToSlash(rel))
}

func (h *HostFS) virtualFromRootPath(rootPath, target string) string {
	rel, err := filepath.Rel(rootPath, target)
	if err != nil || rel == "." {
		return h.virtualRoot
	}
	return Resolve(h.virtualRoot, filepath.ToSlash(rel))
}

func (h *HostFS) virtualizeAbsoluteTarget(target string) (string, error) {
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

	return "", stdfs.ErrPermission
}

func (h *HostFS) checkFileSize(info stdfs.FileInfo) error {
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

func (h *HostFS) readOnlyError(op, name string) error {
	return h.pathError(op, name, stdfs.ErrPermission)
}

func (h *HostFS) pathError(op, name string, err error) error {
	if err == nil {
		return nil
	}
	return &os.PathError{
		Op:   op,
		Path: Clean(name),
		Err:  sanitizeHostErr(err),
	}
}

func sanitizeHostErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, stdfs.ErrNotExist), errors.Is(err, os.ErrNotExist):
		return stdfs.ErrNotExist
	case errors.Is(err, stdfs.ErrPermission), errors.Is(err, os.ErrPermission):
		return stdfs.ErrPermission
	case errors.Is(err, stdfs.ErrInvalid):
		return stdfs.ErrInvalid
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.EISDIR):
		return stdfs.ErrInvalid
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return sanitizeHostErr(pathErr.Err)
	}

	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return sanitizeHostErr(linkErr.Err)
	}

	return err
}

func splitVirtualPath(name string) []string {
	name = strings.TrimPrefix(Clean(name), "/")
	if name == "" {
		return nil
	}
	return strings.Split(name, "/")
}

func withinHostRoot(candidate, root string) bool {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	if candidate == root {
		return true
	}
	if root == string(os.PathSeparator) {
		return filepath.IsAbs(candidate)
	}
	return strings.HasPrefix(candidate, root+string(os.PathSeparator))
}

type namedFileInfo struct {
	name string
	info stdfs.FileInfo
}

func (i namedFileInfo) Name() string         { return i.name }
func (i namedFileInfo) Size() int64          { return i.info.Size() }
func (i namedFileInfo) Mode() stdfs.FileMode { return i.info.Mode() }
func (i namedFileInfo) ModTime() time.Time   { return i.info.ModTime() }
func (i namedFileInfo) IsDir() bool          { return i.info.IsDir() }
func (i namedFileInfo) Sys() any             { return i.info.Sys() }
func (i namedFileInfo) Ownership() (FileOwnership, bool) {
	return OwnershipFromSys(i.info.Sys())
}

type staticFileInfo struct {
	name    string
	size    int64
	mode    stdfs.FileMode
	modTime time.Time
}

func (i staticFileInfo) Name() string         { return i.name }
func (i staticFileInfo) Size() int64          { return i.size }
func (i staticFileInfo) Mode() stdfs.FileMode { return i.mode }
func (i staticFileInfo) ModTime() time.Time   { return i.modTime }
func (i staticFileInfo) IsDir() bool          { return i.mode.IsDir() }
func (i staticFileInfo) Sys() any             { return nil }
func (i staticFileInfo) Ownership() (FileOwnership, bool) {
	return DefaultOwnership(), true
}

type fileTooLargeError struct {
	size int64
	max  int64
}

func (e fileTooLargeError) Error() string {
	return fmt.Sprintf("file too large (%d bytes, max %d)", e.size, e.max)
}

func (e fileTooLargeError) Unwrap() error {
	return syscall.EFBIG
}
