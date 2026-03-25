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
	"sync/atomic"
	"time"
)

// MemoryFS is the default mutable in-memory sandbox filesystem.
type MemoryFS struct {
	mu         sync.RWMutex
	cwd        string
	nodes      map[string]*memoryNode
	nextNodeID uint64
}

type memoryNode struct {
	id       uint64
	mode     stdfs.FileMode
	data     []byte
	fifo     *memoryFIFO
	lazy     LazyFileProvider
	target   string
	children map[string]struct{}
	atime    time.Time
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
		cwd:        "/",
		nextNodeID: 1,
		nodes: map[string]*memoryNode{
			"/": {
				id:       1,
				mode:     stdfs.ModeDir | 0o755,
				children: make(map[string]struct{}),
				atime:    now,
				modTime:  now,
				uid:      DefaultOwnerUID,
				gid:      DefaultOwnerGID,
			},
		},
	}
}

// Clone returns an isolated copy of the in-memory filesystem state.
func (m *MemoryFS) Clone() *MemoryFS {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nodes := make(map[string]*memoryNode, len(m.nodes))
	for name, node := range m.nodes {
		cloned := &memoryNode{
			id:      node.id,
			mode:    node.mode,
			fifo:    node.fifo.clone(),
			lazy:    node.lazy,
			target:  node.target,
			atime:   node.atime,
			modTime: node.modTime,
			uid:     node.uid,
			gid:     node.gid,
		}
		if len(node.data) > 0 {
			cloned.data = append([]byte(nil), node.data...)
		}
		if node.children != nil {
			cloned.children = make(map[string]struct{}, len(node.children))
			for child := range node.children {
				cloned.children[child] = struct{}{}
			}
		}
		nodes[name] = cloned
	}

	return &MemoryFS{
		cwd:        m.cwd,
		nodes:      nodes,
		nextNodeID: m.nextNodeID,
	}
}

func (m *MemoryFS) seedInitialFiles(files InitialFiles, now time.Time) error {
	if len(files) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, file := range files {
		if err := m.seedInitialFileLocked(name, file, now); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemoryFS) seedInitialFileLocked(name string, file InitialFile, now time.Time) error {
	abs := Clean(name)
	if abs == "/" {
		return &os.PathError{Op: "seed", Path: abs, Err: stdfs.ErrInvalid}
	}
	if file.Lazy != nil && file.Content != nil {
		return &os.PathError{Op: "seed", Path: abs, Err: stdfs.ErrInvalid}
	}
	if err := m.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
		return err
	}

	mode := file.Mode.Perm()
	if mode == 0 {
		mode = 0o644
	}
	modTime := file.ModTime.UTC()
	if modTime.IsZero() {
		modTime = now.UTC()
	}

	node := &memoryNode{
		id:      m.newNodeIDLocked(),
		mode:    file.Mode.Type() | mode,
		lazy:    file.Lazy,
		data:    append([]byte(nil), file.Content...),
		atime:   modTime,
		modTime: modTime,
		uid:     DefaultOwnerUID,
		gid:     DefaultOwnerGID,
	}
	if file.Mode&stdfs.ModeNamedPipe != 0 {
		node.fifo = newMemoryFIFO()
		node.data = nil
		node.lazy = nil
	}
	m.nodes[abs] = node
	m.nodes[parentDir(abs)].children[path.Base(abs)] = struct{}{} //nolint:nilaway // parent guaranteed by mkdirAllLocked above
	return nil
}

func isLazyFileNode(node *memoryNode) bool {
	return node != nil && node.lazy != nil && !node.mode.IsDir() && node.mode&stdfs.ModeSymlink == 0
}

func (m *MemoryFS) materializePath(ctx context.Context, name string, followFinal bool) (string, error) {
	for {
		m.mu.RLock()
		abs, node, err := m.resolvePathLocked(name, followFinal, false)
		if err != nil {
			m.mu.RUnlock()
			return "", err
		}
		if !isLazyFileNode(node) {
			m.mu.RUnlock()
			return abs, nil
		}
		lazy := node.lazy //nolint:nilaway // isLazyFileNode guarantees node != nil
		m.mu.RUnlock()

		data, err := lazy(ctx)
		if err != nil {
			return "", err
		}
		buf := append([]byte(nil), data...)

		m.mu.Lock()
		current, ok := m.nodes[abs]
		if ok && current == node && isLazyFileNode(current) {
			current.data = buf
			current.lazy = nil
			m.mu.Unlock()
			return abs, nil
		}
		m.mu.Unlock()
	}
}

func (m *MemoryFS) materializeResolvedPath(ctx context.Context, abs string) error {
	for {
		m.mu.RLock()
		node, ok := m.nodes[abs]
		if !ok {
			m.mu.RUnlock()
			return stdfs.ErrNotExist
		}
		if !isLazyFileNode(node) {
			m.mu.RUnlock()
			return nil
		}
		lazy := node.lazy
		m.mu.RUnlock()

		data, err := lazy(ctx)
		if err != nil {
			return err
		}
		buf := append([]byte(nil), data...)

		m.mu.Lock()
		current, ok := m.nodes[abs]
		if ok && current == node && isLazyFileNode(current) {
			current.data = buf
			current.lazy = nil
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
	}
}

func (m *MemoryFS) Symlink(_ context.Context, target, linkName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	requested := Resolve(m.cwd, linkName)
	abs, node, err := m.resolvePathLocked(linkName, false, true)
	switch {
	case err == nil && node != nil:
		return &os.PathError{Op: "symlink", Path: abs, Err: stdfs.ErrExist}
	case err == nil:
		// Resolved symlinked parents and found a missing final leaf.
	case errors.Is(err, stdfs.ErrNotExist):
		parentAbs, parentErr := m.resolveCreatePathLocked(parentDir(requested), 0)
		if parentErr != nil {
			return &os.PathError{Op: "symlink", Path: requested, Err: parentErr}
		}
		abs = Resolve(parentAbs, path.Base(requested))
	default:
		return &os.PathError{Op: "symlink", Path: requested, Err: err}
	}
	if _, exists := m.nodes[abs]; exists {
		return &os.PathError{Op: "symlink", Path: abs, Err: stdfs.ErrExist}
	}
	if err := m.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
		return err
	}
	m.nodes[abs] = &memoryNode{
		id:      m.newNodeIDLocked(),
		mode:    stdfs.ModeSymlink | 0o777,
		target:  target,
		atime:   time.Now().UTC(),
		modTime: time.Now().UTC(),
		uid:     DefaultOwnerUID,
		gid:     DefaultOwnerGID,
	}
	m.nodes[parentDir(abs)].children[path.Base(abs)] = struct{}{} //nolint:nilaway // parent guaranteed by mkdirAllLocked above
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
	m.nodes[parentDir(newAbs)].children[path.Base(newAbs)] = struct{}{} //nolint:nilaway // parent guaranteed by mkdirAllLocked above
	node.modTime = time.Now().UTC()
	return nil
}

func (m *MemoryFS) Open(ctx context.Context, name string) (File, error) {
	return m.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (m *MemoryFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	if (flag&os.O_TRUNC == 0 || !canWrite(flag)) && flag&(os.O_CREATE|os.O_EXCL) != (os.O_CREATE|os.O_EXCL) {
		if _, err := m.materializePath(ctx, name, true); err != nil {
			if flag&os.O_CREATE == 0 || !errors.Is(err, stdfs.ErrNotExist) {
				return nil, &os.PathError{Op: "open", Path: Resolve(m.Getwd(), name), Err: err}
			}
		}
	}

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
			id:      m.newNodeIDLocked(),
			mode:    perm,
			atime:   time.Now().UTC(),
			modTime: time.Now().UTC(),
			uid:     DefaultOwnerUID,
			gid:     DefaultOwnerGID,
		}
		m.nodes[abs] = node
		m.nodes[parentDir(abs)].children[path.Base(abs)] = struct{}{} //nolint:nilaway // parent guaranteed by mkdirAllLocked above
	} else if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
	}

	if node.mode.IsDir() {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrInvalid}
	}
	if node.mode&stdfs.ModeNamedPipe != 0 {
		if node.fifo == nil {
			node.fifo = newMemoryFIFO()
		}
		return newMemoryFIFOFile(m, abs, node.fifo, flag), nil
	}

	if flag&os.O_TRUNC != 0 && canWrite(flag) {
		node.lazy = nil
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

func (m *MemoryFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	requested := Resolve(m.Getwd(), name)
	abs, err := m.materializePath(ctx, name, true)
	if err != nil {
		return nil, &os.PathError{Op: "stat", Path: requested, Err: err}
	}

	m.mu.RLock()
	node, ok := m.nodes[abs]
	if !ok {
		m.mu.RUnlock()
		return nil, &os.PathError{Op: "stat", Path: abs, Err: stdfs.ErrNotExist}
	}
	info := newFileInfo(path.Base(abs), node)
	m.mu.RUnlock()
	return info, nil
}

func (m *MemoryFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	requested := Resolve(m.Getwd(), name)
	abs, err := m.materializePath(ctx, name, false)
	if err != nil {
		return nil, &os.PathError{Op: "lstat", Path: requested, Err: err}
	}

	m.mu.RLock()
	node, ok := m.nodes[abs]
	if !ok {
		m.mu.RUnlock()
		return nil, &os.PathError{Op: "lstat", Path: abs, Err: stdfs.ErrNotExist}
	}
	info := newFileInfo(path.Base(abs), node)
	m.mu.RUnlock()
	return info, nil
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
		childPath := joinClean(abs, child)
		childNode := m.nodes[childPath] //nolint:nilaway // children are registered via mkdirAllLocked/OpenFile, so node always exists
		entries = append(entries, memoryDirEntry{
			fs:   m,
			name: child,
			path: childPath,
			mode: childNode.mode, //nolint:nilaway // node existence validated above
		})
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
	const mutableModeBits = stdfs.ModePerm | stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky
	typeBits := node.mode &^ mutableModeBits
	node.mode = typeBits | (mode & mutableModeBits)
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

func (m *MemoryFS) Chtimes(_ context.Context, name string, atime, mtime time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, node, err := m.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chtimes", Path: Resolve(m.cwd, name), Err: err}
	}
	now := time.Now().UTC()
	if atime.IsZero() {
		atime = now
	}
	if mtime.IsZero() {
		mtime = now
	}
	node.atime = atime.UTC()
	node.modTime = mtime.UTC()
	return nil
}

func (m *MemoryFS) MkdirAll(_ context.Context, name string, perm stdfs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.mkdirAllLocked(Resolve(m.cwd, name), perm)
}

func (m *MemoryFS) Mkfifo(_ context.Context, name string, perm stdfs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	requested := Resolve(m.cwd, name)
	abs, node, err := m.resolvePathLocked(name, false, true)
	if err != nil {
		return &os.PathError{Op: "mkfifo", Path: requested, Err: err}
	}
	if node != nil {
		return &os.PathError{Op: "mkfifo", Path: abs, Err: stdfs.ErrExist}
	}

	parentAbs, parentNode, err := m.resolveAbsLocked(parentDir(abs), true, false, 0)
	if err != nil {
		return &os.PathError{Op: "mkfifo", Path: requested, Err: err}
	}
	if parentNode == nil || !parentNode.mode.IsDir() {
		return &os.PathError{Op: "mkfifo", Path: parentAbs, Err: stdfs.ErrInvalid}
	}
	if perm == 0 {
		perm = 0o666
	}
	now := time.Now().UTC()
	m.nodes[abs] = &memoryNode{
		id:      m.newNodeIDLocked(),
		mode:    stdfs.ModeNamedPipe | perm.Perm(),
		fifo:    newMemoryFIFO(),
		atime:   now,
		modTime: now,
		uid:     DefaultOwnerUID,
		gid:     DefaultOwnerGID,
	}
	parentNode.children[path.Base(abs)] = struct{}{}
	return nil
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
	delete(m.nodes[parentDir(abs)].children, path.Base(abs)) //nolint:nilaway // abs was resolved successfully above, so parent exists
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

	delete(m.nodes[parentDir(oldAbs)].children, path.Base(oldAbs)) //nolint:nilaway // oldAbs was resolved successfully above, so parent exists
	for candidate := range toMove {
		delete(m.nodes, candidate)
	}

	for oldPath, moveNode := range toMove {
		newPath := strings.Replace(oldPath, oldAbs, newAbs, 1)
		m.nodes[newPath] = moveNode
	}
	m.nodes[parentDir(newAbs)].children[path.Base(newAbs)] = struct{}{} //nolint:nilaway // parent guaranteed by mkdirAllLocked above
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

	current := "/"
	remaining := name[1:] // strip leading "/"
	for remaining != "" {
		var part string
		i := strings.IndexByte(remaining, '/')
		if i < 0 {
			part = remaining
			remaining = ""
		} else {
			part = remaining[:i]
			remaining = remaining[i+1:]
		}
		next := joinClean(current, part)
		node, ok := m.nodes[next]
		if ok {
			if node.mode&stdfs.ModeSymlink != 0 {
				resolved, resolvedNode, err := m.resolveAbsLocked(next, true, false, 0)
				if err != nil {
					return &os.PathError{Op: "mkdir", Path: next, Err: err}
				}
				if resolvedNode == nil {
					return &os.PathError{Op: "mkdir", Path: next, Err: stdfs.ErrNotExist}
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
			id:       m.newNodeIDLocked(),
			mode:     stdfs.ModeDir | perm,
			children: make(map[string]struct{}),
			atime:    time.Now().UTC(),
			modTime:  time.Now().UTC(),
			uid:      DefaultOwnerUID,
			gid:      DefaultOwnerGID,
		}
		m.nodes[current].children[part] = struct{}{} //nolint:nilaway // current was verified as a directory node in prior loop iterations
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
	remaining := abs[1:] // strip leading "/"
	for remaining != "" {
		var part string
		i := strings.IndexByte(remaining, '/')
		if i < 0 {
			part = remaining
			remaining = ""
		} else {
			part = remaining[:i]
			remaining = remaining[i+1:]
		}
		next := joinClean(current, part)
		node, ok := m.nodes[next]
		isLast := remaining == ""
		if !ok {
			if isLast {
				return next, nil
			}
			return Resolve(next, remaining), nil
		}
		if node.mode&stdfs.ModeSymlink != 0 {
			target := Resolve(parentDir(next), node.target)
			if !isLast {
				target = Resolve(target, remaining)
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
	remaining := abs[1:] // strip leading "/"
	for remaining != "" {
		var part string
		i := strings.IndexByte(remaining, '/')
		if i < 0 {
			part = remaining
			remaining = ""
		} else {
			part = remaining[:i]
			remaining = remaining[i+1:]
		}
		next := joinClean(current, part)
		node, ok := m.nodes[next]
		isLast := remaining == ""
		if !ok {
			if isLast && allowMissingFinal {
				return next, nil, nil
			}
			return "", nil, stdfs.ErrNotExist
		}
		if node.mode&stdfs.ModeSymlink != 0 && (!isLast || followFinal) {
			target := Resolve(parentDir(next), node.target)
			if !isLast {
				target = Resolve(target, remaining)
			}
			return m.resolveAbsLocked(target, true, allowMissingFinal, depth+1)
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

func (m *MemoryFS) newNodeIDLocked() uint64 {
	m.nextNodeID++
	return m.nextNodeID
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
	closed atomic.Bool
}

func (f *memoryFile) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !canRead(f.flag) {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}

	var (
		node *memoryNode
		ok   bool
	)
	for {
		f.fs.mu.RLock()
		node, ok = f.fs.nodes[f.path]
		if !ok {
			f.fs.mu.RUnlock()
			return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrNotExist}
		}
		if isLazyFileNode(node) {
			f.fs.mu.RUnlock()
			if err := f.fs.materializeResolvedPath(context.Background(), f.path); err != nil {
				return 0, &os.PathError{Op: "read", Path: f.path, Err: err}
			}
			continue
		}
		if node.mode.IsDir() {
			f.fs.mu.RUnlock()
			return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrInvalid}
		}
		if f.offset >= int64(len(node.data)) {
			f.fs.mu.RUnlock()
			return 0, io.EOF
		}
		n := copy(p, node.data[f.offset:])
		f.offset += int64(n)
		f.fs.mu.RUnlock()
		return n, nil
	}
}

func (f *memoryFile) Write(p []byte) (int, error) {
	if f.closed.Load() {
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
	if isLazyFileNode(node) {
		f.fs.mu.Unlock()
		if err := f.fs.materializeResolvedPath(context.Background(), f.path); err != nil {
			f.fs.mu.Lock()
			return 0, &os.PathError{Op: "write", Path: f.path, Err: err}
		}
		f.fs.mu.Lock()
		node, ok = f.fs.nodes[f.path]
		if !ok {
			return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrNotExist}
		}
	}

	if f.flag&os.O_APPEND != 0 {
		f.offset = int64(len(node.data))
	}

	end := int(f.offset) + len(p)
	if end > len(node.data) {
		node.data = growBytes(node.data, end)
	}
	copy(node.data[int(f.offset):], p)
	f.offset += int64(len(p))
	node.modTime = time.Now().UTC()
	return len(p), nil
}

func (f *memoryFile) ReadFrom(r io.Reader) (int64, error) {
	var buf [32 * 1024]byte
	var total int64
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			written, writeErr := f.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		return total, err
	}
}

func (f *memoryFile) WriteTo(w io.Writer) (int64, error) {
	var buf [32 * 1024]byte
	var total int64
	for {
		n, err := f.Read(buf[:])
		if n > 0 {
			written, writeErr := w.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		return total, err
	}
}

func (f *memoryFile) Close() error {
	f.closed.Store(true)
	return nil
}

func (f *memoryFile) Stat() (stdfs.FileInfo, error) {
	if f.closed.Load() {
		return nil, stdfs.ErrClosed
	}
	return f.fs.Stat(context.Background(), f.path)
}

func growBytes(data []byte, size int) []byte {
	if size <= len(data) {
		return data
	}
	if size <= cap(data) {
		return data[:size]
	}

	newCap := cap(data)
	if newCap == 0 {
		newCap = size
	}
	for newCap < size {
		if newCap < 1<<20 {
			newCap *= 2
			continue
		}
		newCap += newCap / 2
	}

	grown := make([]byte, size, newCap)
	copy(grown, data)
	return grown
}

type fileInfo struct {
	name    string
	size    int64
	mode    stdfs.FileMode
	atime   time.Time
	modTime time.Time
	uid     uint32
	gid     uint32
	nodeID  uint64
}

type memoryDirEntry struct {
	fs   *MemoryFS
	name string
	path string
	mode stdfs.FileMode
}

func (e memoryDirEntry) Name() string         { return e.name }
func (e memoryDirEntry) IsDir() bool          { return e.mode.IsDir() }
func (e memoryDirEntry) Type() stdfs.FileMode { return e.mode.Type() }
func (e memoryDirEntry) Info() (stdfs.FileInfo, error) {
	return e.fs.Lstat(context.Background(), e.path)
}

func newFileInfo(name string, node *memoryNode) *fileInfo {
	size := int64(len(node.data))
	switch {
	case node.mode.IsDir():
		size = 0
	case node.mode&stdfs.ModeNamedPipe != 0:
		size = 0
	case node.mode&stdfs.ModeSymlink != 0:
		size = int64(len(node.target))
	}
	return &fileInfo{
		name:    name,
		size:    size,
		mode:    node.mode,
		atime:   firstNonZeroTime(node.atime, node.modTime),
		modTime: node.modTime,
		uid:     node.uid,
		gid:     node.gid,
		nodeID:  node.id,
	}
}

func (fi *fileInfo) Name() string         { return fi.name }
func (fi *fileInfo) Size() int64          { return fi.size }
func (fi *fileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi *fileInfo) ModTime() time.Time   { return fi.modTime }
func (fi *fileInfo) IsDir() bool          { return fi.mode.IsDir() }
func (fi *fileInfo) Sys() any {
	return memoryStat{
		Atime:     fi.atime.Unix(),
		AtimeNsec: int64(fi.atime.Nanosecond()),
		NodeID:    fi.nodeID,
	}
}
func (fi *fileInfo) Ownership() (FileOwnership, bool) {
	return FileOwnership{UID: fi.uid, GID: fi.gid}, true
}

type memoryStat struct {
	Atime     int64
	AtimeNsec int64
	NodeID    uint64
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

var _ FileSystem = (*MemoryFS)(nil)
var _ File = (*memoryFile)(nil)
var _ stdfs.FileInfo = (*fileInfo)(nil)
var _ FIFOFileSystem = (*MemoryFS)(nil)

var _ = errors.New
