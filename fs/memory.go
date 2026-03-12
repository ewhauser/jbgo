package fs

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

// MemoryFS is the default mutable in-memory sandbox filesystem.
type MemoryFS struct {
	mu    sync.RWMutex
	cwd   string
	nodes map[string]*memoryNode
}

type memoryNode struct {
	mode     stdfs.FileMode
	data     []byte
	target   string
	children map[string]struct{}
	modTime  time.Time
	uid      uint32
	gid      uint32
}

const maxSymlinkDepth = 40

var errTooManySymlinks = errors.New("too many levels of symbolic links")

// NewMemory creates a fresh in-memory filesystem rooted at "/".
func NewMemory() *MemoryFS {
	now := time.Now().UTC()
	return &MemoryFS{
		cwd: "/",
		nodes: map[string]*memoryNode{
			"/": {
				mode:     stdfs.ModeDir | 0o755,
				children: make(map[string]struct{}),
				modTime:  now,
				uid:      DefaultOwnerUID,
				gid:      DefaultOwnerGID,
			},
		},
	}
}

func (m *MemoryFS) Symlink(_ context.Context, target, linkName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	abs := Resolve(m.cwd, linkName)
	if _, exists := m.nodes[abs]; exists {
		return &os.PathError{Op: "symlink", Path: abs, Err: stdfs.ErrExist}
	}
	if err := m.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
		return err
	}
	m.nodes[abs] = &memoryNode{
		mode:    stdfs.ModeSymlink | 0o777,
		target:  target,
		modTime: time.Now().UTC(),
		uid:     DefaultOwnerUID,
		gid:     DefaultOwnerGID,
	}
	m.nodes[parentDir(abs)].children[path.Base(abs)] = struct{}{}
	return nil
}

func (m *MemoryFS) Link(_ context.Context, oldName, newName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldAbs, node, err := m.resolvePathLocked(oldName, false, false)
	if err != nil {
		return &os.PathError{Op: "link", Path: Resolve(m.cwd, oldName), Err: err}
	}
	if node.mode.IsDir() {
		return &os.PathError{Op: "link", Path: oldAbs, Err: stdfs.ErrInvalid}
	}

	newAbs := Resolve(m.cwd, newName)
	if _, existing, err := m.resolvePathLocked(newName, false, true); err == nil && existing != nil {
		return &os.PathError{Op: "link", Path: newAbs, Err: stdfs.ErrExist}
	} else if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return &os.PathError{Op: "link", Path: newAbs, Err: err}
	}
	if err := m.mkdirAllLocked(parentDir(newAbs), 0o755); err != nil {
		return err
	}
	m.nodes[newAbs] = node
	m.nodes[parentDir(newAbs)].children[path.Base(newAbs)] = struct{}{}
	node.modTime = time.Now().UTC()
	return nil
}

func (m *MemoryFS) Open(ctx context.Context, name string) (File, error) {
	return m.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (m *MemoryFS) OpenFile(_ context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	requested := Resolve(m.cwd, name)
	var (
		abs  string
		node *memoryNode
		err  error
	)
	if flag&os.O_CREATE != 0 {
		abs, err = m.resolveCreatePathLocked(requested, 0)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: requested, Err: err}
		}
		node = m.nodes[abs]
	} else {
		abs, node, err = m.resolvePathLocked(name, true, false)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: requested, Err: err}
		}
	}
	if node == nil {
		if flag&os.O_CREATE == 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrNotExist}
		}
		if err := m.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
			return nil, err
		}
		if perm == 0 {
			perm = 0o644
		}
		node = &memoryNode{
			mode:    perm,
			modTime: time.Now().UTC(),
			uid:     DefaultOwnerUID,
			gid:     DefaultOwnerGID,
		}
		m.nodes[abs] = node
		m.nodes[parentDir(abs)].children[path.Base(abs)] = struct{}{}
	} else if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
	}

	if node.mode.IsDir() {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrInvalid}
	}

	if flag&os.O_TRUNC != 0 && canWrite(flag) {
		node.data = nil
		node.modTime = time.Now().UTC()
	}

	offset := int64(0)
	if flag&os.O_APPEND != 0 {
		offset = int64(len(node.data))
	}

	return &memoryFile{
		fs:     m,
		path:   abs,
		flag:   flag,
		offset: offset,
	}, nil
}

func (m *MemoryFS) Stat(_ context.Context, name string) (stdfs.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	abs, node, err := m.resolvePathLocked(name, true, false)
	if err != nil {
		return nil, &os.PathError{Op: "stat", Path: Resolve(m.cwd, name), Err: err}
	}
	return newFileInfo(path.Base(abs), node), nil
}

func (m *MemoryFS) Lstat(_ context.Context, name string) (stdfs.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	abs, node, err := m.resolvePathLocked(name, false, false)
	if err != nil {
		return nil, &os.PathError{Op: "lstat", Path: Resolve(m.cwd, name), Err: err}
	}
	return newFileInfo(path.Base(abs), node), nil
}

func (m *MemoryFS) ReadDir(_ context.Context, name string) ([]stdfs.DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	abs, node, err := m.resolvePathLocked(name, true, false)
	if err != nil {
		return nil, &os.PathError{Op: "readdir", Path: Resolve(m.cwd, name), Err: err}
	}
	if !node.mode.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	}

	names := make([]string, 0, len(node.children))
	for child := range node.children {
		names = append(names, child)
	}
	slices.Sort(names)

	entries := make([]stdfs.DirEntry, 0, len(names))
	for _, child := range names {
		childPath := Resolve(abs, child)
		childNode := m.nodes[childPath]
		info := newFileInfo(child, childNode)
		entries = append(entries, stdfs.FileInfoToDirEntry(info))
	}
	return entries, nil
}

func (m *MemoryFS) Readlink(_ context.Context, name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	abs, node, err := m.resolvePathLocked(name, false, false)
	if err != nil {
		return "", &os.PathError{Op: "readlink", Path: Resolve(m.cwd, name), Err: err}
	}
	if node.mode&stdfs.ModeSymlink == 0 {
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrInvalid}
	}
	return node.target, nil
}

func (m *MemoryFS) Realpath(_ context.Context, name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	abs, _, err := m.resolvePathLocked(name, true, false)
	if err != nil {
		return "", &os.PathError{Op: "realpath", Path: Resolve(m.cwd, name), Err: err}
	}
	return abs, nil
}

func (m *MemoryFS) Chmod(_ context.Context, name string, mode stdfs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	abs, node, err := m.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chmod", Path: Resolve(m.cwd, name), Err: err}
	}
	typeBits := node.mode &^ stdfs.ModePerm
	node.mode = typeBits | mode.Perm()
	node.modTime = time.Now().UTC()
	m.nodes[abs] = node
	return nil
}

func (m *MemoryFS) Chown(_ context.Context, name string, uid, gid uint32, follow bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	abs, node, err := m.resolvePathLocked(name, follow, false)
	if err != nil {
		return &os.PathError{Op: "chown", Path: Resolve(m.cwd, name), Err: err}
	}
	node.uid = uid
	node.gid = gid
	m.nodes[abs] = node
	return nil
}

func (m *MemoryFS) Chtimes(_ context.Context, name string, _, mtime time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, node, err := m.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chtimes", Path: Resolve(m.cwd, name), Err: err}
	}
	if mtime.IsZero() {
		mtime = time.Now().UTC()
	}
	node.modTime = mtime.UTC()
	return nil
}

func (m *MemoryFS) MkdirAll(_ context.Context, name string, perm stdfs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.mkdirAllLocked(Resolve(m.cwd, name), perm)
}

func (m *MemoryFS) Remove(_ context.Context, name string, recursive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	abs, node, err := m.resolvePathLocked(name, false, false)
	if err != nil {
		return &os.PathError{Op: "remove", Path: Resolve(m.cwd, name), Err: err}
	}
	if abs == "/" {
		return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrPermission}
	}

	if node.mode.IsDir() && len(node.children) > 0 && !recursive {
		return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrInvalid}
	}

	for candidate := range m.nodes {
		if candidate == abs || strings.HasPrefix(candidate, abs+"/") {
			delete(m.nodes, candidate)
		}
	}
	delete(m.nodes[parentDir(abs)].children, path.Base(abs))
	return nil
}

func (m *MemoryFS) Rename(_ context.Context, oldName, newName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldAbs, _, err := m.resolvePathLocked(oldName, false, false)
	if err != nil {
		return &os.PathError{Op: "rename", Path: Resolve(m.cwd, oldName), Err: err}
	}
	if oldAbs == "/" {
		return &os.PathError{Op: "rename", Path: oldAbs, Err: stdfs.ErrPermission}
	}
	newAbs, node, err := m.resolvePathLocked(newName, false, true)
	if err != nil {
		return &os.PathError{Op: "rename", Path: Resolve(m.cwd, newName), Err: err}
	}
	if node != nil {
		return &os.PathError{Op: "rename", Path: newAbs, Err: stdfs.ErrExist}
	}
	if err := m.mkdirAllLocked(parentDir(newAbs), 0o755); err != nil {
		return err
	}

	toMove := make(map[string]*memoryNode)
	for candidate, candidateNode := range m.nodes {
		if candidate == oldAbs || strings.HasPrefix(candidate, oldAbs+"/") {
			toMove[candidate] = candidateNode
		}
	}

	delete(m.nodes[parentDir(oldAbs)].children, path.Base(oldAbs))
	for candidate := range toMove {
		delete(m.nodes, candidate)
	}

	for oldPath, moveNode := range toMove {
		newPath := strings.Replace(oldPath, oldAbs, newAbs, 1)
		m.nodes[newPath] = moveNode
	}
	m.nodes[parentDir(newAbs)].children[path.Base(newAbs)] = struct{}{}
	m.rebuildDirectoryChildrenLocked()
	return nil
}

func (m *MemoryFS) Getwd() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.cwd
}

func (m *MemoryFS) Chdir(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	abs, node, err := m.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chdir", Path: Resolve(m.cwd, name), Err: err}
	}
	if !node.mode.IsDir() {
		return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	m.cwd = abs
	return nil
}

func (m *MemoryFS) mkdirAllLocked(name string, perm stdfs.FileMode) error {
	name = Clean(name)
	if name == "/" {
		return nil
	}

	parts := strings.Split(strings.TrimPrefix(name, "/"), "/")
	current := "/"
	for _, part := range parts {
		next := Resolve(current, part)
		node, ok := m.nodes[next]
		if ok {
			if node.mode&stdfs.ModeSymlink != 0 {
				resolved, resolvedNode, err := m.resolveAbsLocked(next, true, false, 0)
				if err != nil {
					return &os.PathError{Op: "mkdir", Path: next, Err: err}
				}
				if !resolvedNode.mode.IsDir() {
					return &os.PathError{Op: "mkdir", Path: next, Err: stdfs.ErrInvalid}
				}
				current = resolved
				continue
			}
			if !node.mode.IsDir() {
				return &os.PathError{Op: "mkdir", Path: next, Err: stdfs.ErrInvalid}
			}
			current = next
			continue
		}
		if perm == 0 {
			perm = 0o755
		}
		m.nodes[next] = &memoryNode{
			mode:     stdfs.ModeDir | perm,
			children: make(map[string]struct{}),
			modTime:  time.Now().UTC(),
			uid:      DefaultOwnerUID,
			gid:      DefaultOwnerGID,
		}
		m.nodes[current].children[part] = struct{}{}
		current = next
	}
	return nil
}

func (m *MemoryFS) resolvePathLocked(name string, followFinal, allowMissingFinal bool) (string, *memoryNode, error) {
	return m.resolveAbsLocked(Resolve(m.cwd, name), followFinal, allowMissingFinal, 0)
}

func (m *MemoryFS) resolveCreatePathLocked(abs string, depth int) (string, error) {
	abs = Clean(abs)
	if depth > maxSymlinkDepth {
		return "", errTooManySymlinks
	}
	if abs == "/" {
		return abs, nil
	}

	current := "/"
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, part := range parts {
		next := Resolve(current, part)
		node, ok := m.nodes[next]
		isLast := i == len(parts)-1
		if !ok {
			return Resolve(current, path.Join(parts[i:]...)), nil
		}
		if node.mode&stdfs.ModeSymlink != 0 {
			target := Resolve(parentDir(next), node.target)
			if !isLast {
				target = Resolve(target, path.Join(parts[i+1:]...))
			}
			return m.resolveCreatePathLocked(target, depth+1)
		}
		if isLast {
			return next, nil
		}
		if !node.mode.IsDir() {
			return "", stdfs.ErrInvalid
		}
		current = next
	}
	return abs, nil
}

func (m *MemoryFS) resolveAbsLocked(abs string, followFinal, allowMissingFinal bool, depth int) (string, *memoryNode, error) {
	abs = Clean(abs)
	if depth > maxSymlinkDepth {
		return "", nil, errTooManySymlinks
	}
	if abs == "/" {
		node := m.nodes["/"]
		if node == nil {
			return "", nil, stdfs.ErrNotExist
		}
		return "/", node, nil
	}

	current := "/"
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, part := range parts {
		next := Resolve(current, part)
		node, ok := m.nodes[next]
		isLast := i == len(parts)-1
		if !ok {
			if isLast && allowMissingFinal {
				return next, nil, nil
			}
			return "", nil, stdfs.ErrNotExist
		}
		if node.mode&stdfs.ModeSymlink != 0 && (!isLast || followFinal) {
			target := Resolve(parentDir(next), node.target)
			if !isLast {
				target = Resolve(target, path.Join(parts[i+1:]...))
			}
			return m.resolveAbsLocked(target, true, allowMissingFinal && isLast, depth+1)
		}
		if isLast {
			return next, node, nil
		}
		if !node.mode.IsDir() {
			return "", nil, stdfs.ErrInvalid
		}
		current = next
	}

	node := m.nodes["/"]
	if node == nil {
		return "", nil, stdfs.ErrNotExist
	}
	return "/", node, nil
}

func (m *MemoryFS) rebuildDirectoryChildrenLocked() {
	for _, node := range m.nodes {
		if node.mode.IsDir() {
			node.children = make(map[string]struct{})
		}
	}
	for pathName := range m.nodes {
		if pathName == "/" {
			continue
		}
		parent := parentDir(pathName)
		parentNode := m.nodes[parent]
		if parentNode != nil && parentNode.mode.IsDir() {
			parentNode.children[path.Base(pathName)] = struct{}{}
		}
	}
}

func parentDir(name string) string {
	if name == "/" {
		return "/"
	}
	return Clean(path.Dir(name))
}

func canWrite(flag int) bool {
	switch flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY, os.O_RDWR:
		return true
	default:
		return false
	}
}

func canRead(flag int) bool {
	return flag&(os.O_RDONLY|os.O_WRONLY|os.O_RDWR) != os.O_WRONLY
}

type memoryFile struct {
	fs     *MemoryFS
	path   string
	flag   int
	offset int64
	closed bool
}

func (f *memoryFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, stdfs.ErrClosed
	}
	if !canRead(f.flag) {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}

	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	node, ok := f.fs.nodes[f.path]
	if !ok {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrNotExist}
	}
	if node.mode.IsDir() {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrInvalid}
	}

	if f.offset >= int64(len(node.data)) {
		return 0, io.EOF
	}
	n := copy(p, node.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *memoryFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, stdfs.ErrClosed
	}
	if !canWrite(f.flag) {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrPermission}
	}

	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	node, ok := f.fs.nodes[f.path]
	if !ok {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrNotExist}
	}
	if node.mode.IsDir() {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrInvalid}
	}

	if f.flag&os.O_APPEND != 0 {
		f.offset = int64(len(node.data))
	}

	end := int(f.offset) + len(p)
	if end > len(node.data) {
		grown := make([]byte, end)
		copy(grown, node.data)
		node.data = grown
	}
	copy(node.data[int(f.offset):], p)
	f.offset += int64(len(p))
	node.modTime = time.Now().UTC()
	return len(p), nil
}

func (f *memoryFile) Close() error {
	f.closed = true
	return nil
}

func (f *memoryFile) Stat() (stdfs.FileInfo, error) {
	if f.closed {
		return nil, stdfs.ErrClosed
	}
	return f.fs.Stat(context.Background(), f.path)
}

type fileInfo struct {
	name    string
	size    int64
	mode    stdfs.FileMode
	modTime time.Time
	uid     uint32
	gid     uint32
}

func newFileInfo(name string, node *memoryNode) fileInfo {
	size := int64(len(node.data))
	if node.mode.IsDir() {
		size = 0
	} else if node.mode&stdfs.ModeSymlink != 0 {
		size = int64(len(node.target))
	}
	return fileInfo{
		name:    name,
		size:    size,
		mode:    node.mode,
		modTime: node.modTime,
		uid:     node.uid,
		gid:     node.gid,
	}
}

func (fi fileInfo) Name() string         { return fi.name }
func (fi fileInfo) Size() int64          { return fi.size }
func (fi fileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi fileInfo) ModTime() time.Time   { return fi.modTime }
func (fi fileInfo) IsDir() bool          { return fi.mode.IsDir() }
func (fi fileInfo) Sys() any             { return nil }
func (fi fileInfo) Ownership() (FileOwnership, bool) {
	return FileOwnership{UID: fi.uid, GID: fi.gid}, true
}

var _ FileSystem = (*MemoryFS)(nil)
var _ File = (*memoryFile)(nil)
var _ stdfs.FileInfo = fileInfo{}

var _ = errors.New
