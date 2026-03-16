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

// TrieFS is an experimental in-memory filesystem optimized for read-heavy
// directory traversals and lookups.
//
// This backend is intended for explicit opt-in use in static or read-mostly
// compositions. It is not the default runtime backend, and it is not the
// recommended path for live host-backed workspace helpers.
type TrieFS struct {
	mu         sync.RWMutex
	cwd        string
	root       *trieDentry
	nextNodeID uint64
}

type trieDentry struct {
	name   string
	parent *trieDentry
	inode  *trieInode
}

type trieInode struct {
	id       uint64
	mode     stdfs.FileMode
	data     []byte
	lazy     LazyFileProvider
	target   string
	children map[string]*trieDentry
	names    []string
	dirty    bool
	atime    time.Time
	modTime  time.Time
	uid      uint32
	gid      uint32
}

type trieFactory struct{}

func (trieFactory) New(context.Context) (FileSystem, error) {
	return NewTrie(), nil
}

type seededTrieFactory struct {
	files InitialFiles
}

func (f seededTrieFactory) New(context.Context) (FileSystem, error) {
	return newSeededTrie(f.files)
}

// NewTrie creates a fresh trie-backed filesystem rooted at "/".
//
// Experimental: This backend is intended for explicit opt-in use in read-mostly
// compositions.
func NewTrie() *TrieFS {
	now := time.Now().UTC()
	root := &trieDentry{
		inode: &trieInode{
			id:       1,
			mode:     stdfs.ModeDir | 0o755,
			children: make(map[string]*trieDentry),
			atime:    now,
			modTime:  now,
			uid:      DefaultOwnerUID,
			gid:      DefaultOwnerGID,
		},
	}
	return &TrieFS{
		cwd:        "/",
		root:       root,
		nextNodeID: 1,
	}
}

// Trie returns a factory that creates a fresh trie-backed filesystem per
// session.
//
// Experimental: This backend is intended for explicit opt-in use in read-mostly
// compositions.
func Trie() Factory {
	return trieFactory{}
}

// SeededTrie returns a trie-backed filesystem factory preloaded with the
// provided files.
//
// This is the usual entry point for read-mostly trie data. The recommended
// composition is `Reusable(SeededTrie(...))`, which shares one trie-backed
// lower tree and gives each caller a fresh writable overlay. That factory can
// then be passed through `gbash.CustomFileSystem(...)`, mounted with
// `gbash.MountableFileSystem(...)`, or wrapped with [NewSearchableFactory] when
// a mounted trie tree should also expose a search provider.
//
// Example single-root usage:
//
//	gb, err := gbash.New(
//		gbash.WithFileSystem(gbash.CustomFileSystem(
//			gbfs.Reusable(gbfs.SeededTrie(gbfs.InitialFiles{
//				"/data/catalog/manifest.txt": {Content: []byte("dataset\n")},
//				"/data/catalog/index.txt": {
//					Lazy: func(ctx context.Context) ([]byte, error) {
//						return fetchLargeFixture(ctx)
//					},
//				},
//			})),
//			"/data",
//		)),
//	)
//
// Example mountable dataset plus scratch storage:
//
//	gb, err := gbash.New(
//		gbash.WithFileSystem(gbash.MountableFileSystem(gbash.MountableFileSystemOptions{
//			Base: gbfs.Memory(),
//			Mounts: []gbfs.MountConfig{
//				{
//					MountPoint: "/dataset",
//					Factory: gbfs.Reusable(gbfs.SeededTrie(gbfs.InitialFiles{
//						"/docs/guide.txt": {Content: []byte("guide\n")},
//					})),
//				},
//				{MountPoint: "/scratch", Factory: gbfs.Memory()},
//			},
//			WorkingDir: "/scratch",
//		})),
//	)
//
// Experimental: This backend is intended for explicit opt-in use in read-mostly
// compositions.
func SeededTrie(files InitialFiles) Factory {
	return seededTrieFactory{files: copyInitialFiles(files)}
}

func newSeededTrie(files InitialFiles) (*TrieFS, error) {
	fsys := NewTrie()
	if err := fsys.seedInitialFiles(files, time.Now().UTC()); err != nil {
		return nil, err
	}
	return fsys, nil
}

// Clone returns an isolated copy of the trie-backed filesystem while
// preserving hard-link sharing across cloned dentries.
func (f *TrieFS) Clone() *TrieFS {
	f.mu.RLock()
	defer f.mu.RUnlock()

	inodes := make(map[*trieInode]*trieInode)
	var cloneDentry func(parent *trieDentry, src *trieDentry) *trieDentry
	cloneDentry = func(parent *trieDentry, src *trieDentry) *trieDentry {
		if src == nil {
			return nil
		}
		clonedInode, ok := inodes[src.inode]
		if !ok {
			clonedInode = &trieInode{
				id:      src.inode.id,
				mode:    src.inode.mode,
				lazy:    src.inode.lazy,
				target:  src.inode.target,
				atime:   src.inode.atime,
				modTime: src.inode.modTime,
				uid:     src.inode.uid,
				gid:     src.inode.gid,
			}
			if len(src.inode.data) > 0 {
				clonedInode.data = append([]byte(nil), src.inode.data...)
			}
			if src.inode.children != nil {
				clonedInode.children = make(map[string]*trieDentry, len(src.inode.children))
			}
			if len(src.inode.names) > 0 {
				clonedInode.names = append([]string(nil), src.inode.names...)
			}
			clonedInode.dirty = src.inode.dirty
			inodes[src.inode] = clonedInode
		}
		out := &trieDentry{
			name:   src.name,
			parent: parent,
			inode:  clonedInode,
		}
		if src.inode.children != nil {
			for name, child := range src.inode.children {
				clonedChild := cloneDentry(out, child)
				clonedInode.children[name] = clonedChild
			}
		}
		return out
	}

	return &TrieFS{
		cwd:        f.cwd,
		root:       cloneDentry(nil, f.root),
		nextNodeID: f.nextNodeID,
	}
}

func (f *TrieFS) seedInitialFiles(files InitialFiles, now time.Time) error {
	if len(files) == 0 {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for name, file := range files {
		if err := f.seedInitialFileLocked(name, file, now); err != nil {
			return err
		}
	}
	return nil
}

func (f *TrieFS) seedInitialFileLocked(name string, file InitialFile, now time.Time) error {
	abs := Clean(name)
	if abs == "/" {
		return &os.PathError{Op: "seed", Path: abs, Err: stdfs.ErrInvalid}
	}
	if file.Lazy != nil && file.Content != nil {
		return &os.PathError{Op: "seed", Path: abs, Err: stdfs.ErrInvalid}
	}
	if err := f.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
		return err
	}
	parentAbs := parentDir(abs)
	_, parentEntry, err := f.resolveAbsLocked(parentAbs, true, false, 0)
	if err != nil {
		return err
	}
	if parentEntry == nil || !parentEntry.inode.mode.IsDir() {
		return &os.PathError{Op: "seed", Path: parentAbs, Err: stdfs.ErrInvalid}
	}
	if _, exists := parentEntry.inode.children[path.Base(abs)]; exists {
		return &os.PathError{Op: "seed", Path: abs, Err: stdfs.ErrExist}
	}

	mode := file.Mode.Perm()
	if mode == 0 {
		mode = 0o644
	}
	modTime := file.ModTime.UTC()
	if modTime.IsZero() {
		modTime = now.UTC()
	}
	child := &trieDentry{
		name:   path.Base(abs),
		parent: parentEntry,
		inode: &trieInode{
			id:      f.newNodeIDLocked(),
			mode:    mode,
			data:    append([]byte(nil), file.Content...),
			lazy:    file.Lazy,
			atime:   modTime,
			modTime: modTime,
			uid:     DefaultOwnerUID,
			gid:     DefaultOwnerGID,
		},
	}
	f.attachChildLocked(parentEntry, child)
	return nil
}

func (f *TrieFS) Open(ctx context.Context, name string) (File, error) {
	return f.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (f *TrieFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	if (flag&os.O_TRUNC == 0 || !canWrite(flag)) && flag&(os.O_CREATE|os.O_EXCL) != (os.O_CREATE|os.O_EXCL) {
		if _, _, err := f.materializePath(ctx, name, true); err != nil {
			if flag&os.O_CREATE == 0 || !errors.Is(err, stdfs.ErrNotExist) {
				return nil, &os.PathError{Op: "open", Path: Resolve(f.Getwd(), name), Err: err}
			}
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	requested := Resolve(f.cwd, name)
	var (
		abs   string
		entry *trieDentry
		err   error
	)
	if flag&os.O_CREATE != 0 {
		abs, err = f.resolveCreatePathLocked(requested, 0)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: requested, Err: err}
		}
		entry, _ = f.lookupAbsNoFollowLocked(abs)
	} else {
		abs, entry, err = f.resolveAbsLocked(requested, true, false, 0)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: requested, Err: err}
		}
	}
	if entry == nil {
		if flag&os.O_CREATE == 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrNotExist}
		}
		if err := f.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
			return nil, err
		}
		if perm == 0 {
			perm = 0o644
		}
		parentAbs := parentDir(abs)
		_, parentEntry, err := f.resolveAbsLocked(parentAbs, true, false, 0)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: parentAbs, Err: err}
		}
		entry = &trieDentry{
			name:   path.Base(abs),
			parent: parentEntry,
			inode: &trieInode{
				id:      f.newNodeIDLocked(),
				mode:    perm,
				atime:   time.Now().UTC(),
				modTime: time.Now().UTC(),
				uid:     DefaultOwnerUID,
				gid:     DefaultOwnerGID,
			},
		}
		f.attachChildLocked(parentEntry, entry)
	} else if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
	}

	if entry.inode.mode.IsDir() {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrInvalid}
	}
	if flag&os.O_TRUNC != 0 && canWrite(flag) {
		entry.inode.lazy = nil
		entry.inode.data = nil
		entry.inode.modTime = time.Now().UTC()
	}

	offset := int64(0)
	if flag&os.O_APPEND != 0 {
		offset = int64(len(entry.inode.data))
	}
	return &trieFile{
		fs:     f,
		path:   abs,
		flag:   flag,
		offset: offset,
	}, nil
}

func (f *TrieFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs, entry, err := f.materializePath(ctx, name, true)
	if err != nil {
		return nil, &os.PathError{Op: "stat", Path: Resolve(f.cwd, name), Err: err}
	}
	return newTrieFileInfo(path.Base(abs), entry.inode), nil
}

func (f *TrieFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs, entry, err := f.materializePath(ctx, name, false)
	if err != nil {
		return nil, &os.PathError{Op: "lstat", Path: Resolve(f.cwd, name), Err: err}
	}
	return newTrieFileInfo(path.Base(abs), entry.inode), nil
}

func (f *TrieFS) ReadDir(_ context.Context, name string) ([]stdfs.DirEntry, error) {
	requested := Resolve(f.Getwd(), name)

	f.mu.RLock()
	abs, entry, err := f.resolveAbsLocked(requested, true, false, 0)
	if err != nil {
		f.mu.RUnlock()
		return nil, &os.PathError{Op: "readdir", Path: requested, Err: err}
	}
	if !entry.inode.mode.IsDir() {
		f.mu.RUnlock()
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	if !entry.inode.dirty && len(entry.inode.names) == len(entry.inode.children) {
		entries := f.dirEntriesLocked(abs, entry.inode, entry.inode.names)
		f.mu.RUnlock()
		return entries, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	abs, entry, err = f.resolveAbsLocked(requested, true, false, 0)
	if err != nil {
		return nil, &os.PathError{Op: "readdir", Path: requested, Err: err}
	}
	if !entry.inode.mode.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	names := f.sortedChildNamesLocked(entry.inode)
	return f.dirEntriesLocked(abs, entry.inode, names), nil
}

func (f *TrieFS) Readlink(_ context.Context, name string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	abs, entry, err := f.resolvePathLocked(name, false, false)
	if err != nil {
		return "", &os.PathError{Op: "readlink", Path: Resolve(f.cwd, name), Err: err}
	}
	if entry.inode.mode&stdfs.ModeSymlink == 0 {
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrInvalid}
	}
	return entry.inode.target, nil
}

func (f *TrieFS) Realpath(_ context.Context, name string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	abs, _, err := f.resolvePathLocked(name, true, false)
	if err != nil {
		return "", &os.PathError{Op: "realpath", Path: Resolve(f.cwd, name), Err: err}
	}
	return abs, nil
}

func (f *TrieFS) Symlink(_ context.Context, target, linkName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	requested := Resolve(f.cwd, linkName)
	abs, entry, err := f.resolvePathLocked(linkName, false, true)
	switch {
	case err == nil && entry != nil:
		return &os.PathError{Op: "symlink", Path: abs, Err: stdfs.ErrExist}
	case err == nil:
		// Resolved symlinked parents and found a missing final leaf.
	case errors.Is(err, stdfs.ErrNotExist):
		parentAbs, parentErr := f.resolveCreatePathLocked(parentDir(requested), 0)
		if parentErr != nil {
			return &os.PathError{Op: "symlink", Path: requested, Err: parentErr}
		}
		abs = Resolve(parentAbs, path.Base(requested))
	default:
		return &os.PathError{Op: "symlink", Path: requested, Err: err}
	}
	if entry, _ := f.lookupAbsNoFollowLocked(abs); entry != nil {
		return &os.PathError{Op: "symlink", Path: abs, Err: stdfs.ErrExist}
	}
	if err := f.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
		return err
	}
	parentAbs := parentDir(abs)
	_, parentEntry, err := f.resolveAbsLocked(parentAbs, true, false, 0)
	if err != nil {
		return err
	}
	child := &trieDentry{
		name:   path.Base(abs),
		parent: parentEntry,
		inode: &trieInode{
			id:      f.newNodeIDLocked(),
			mode:    stdfs.ModeSymlink | 0o777,
			target:  target,
			atime:   time.Now().UTC(),
			modTime: time.Now().UTC(),
			uid:     DefaultOwnerUID,
			gid:     DefaultOwnerGID,
		},
	}
	f.attachChildLocked(parentEntry, child)
	return nil
}

func (f *TrieFS) Link(_ context.Context, oldName, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	oldAbs, oldEntry, err := f.resolvePathLocked(oldName, false, false)
	if err != nil {
		return &os.PathError{Op: "link", Path: Resolve(f.cwd, oldName), Err: err}
	}
	if oldEntry.inode.mode.IsDir() {
		return &os.PathError{Op: "link", Path: oldAbs, Err: stdfs.ErrInvalid}
	}

	newAbs := Resolve(f.cwd, newName)
	if _, existing, err := f.resolvePathLocked(newName, false, true); err == nil && existing != nil {
		return &os.PathError{Op: "link", Path: newAbs, Err: stdfs.ErrExist}
	} else if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return &os.PathError{Op: "link", Path: newAbs, Err: err}
	}
	if err := f.mkdirAllLocked(parentDir(newAbs), 0o755); err != nil {
		return err
	}
	_, parentEntry, err := f.resolveAbsLocked(parentDir(newAbs), true, false, 0)
	if err != nil {
		return err
	}
	child := &trieDentry{
		name:   path.Base(newAbs),
		parent: parentEntry,
		inode:  oldEntry.inode,
	}
	f.attachChildLocked(parentEntry, child)
	oldEntry.inode.modTime = time.Now().UTC()
	return nil
}

func (f *TrieFS) Chown(_ context.Context, name string, uid, gid uint32, follow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	abs, entry, err := f.resolvePathLocked(name, follow, false)
	if err != nil {
		return &os.PathError{Op: "chown", Path: Resolve(f.cwd, name), Err: err}
	}
	entry.inode.uid = uid
	entry.inode.gid = gid
	_ = abs
	return nil
}

func (f *TrieFS) Chmod(_ context.Context, name string, mode stdfs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	abs, entry, err := f.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chmod", Path: Resolve(f.cwd, name), Err: err}
	}
	typeBits := entry.inode.mode &^ stdfs.ModePerm
	entry.inode.mode = typeBits | mode.Perm()
	entry.inode.modTime = time.Now().UTC()
	_ = abs
	return nil
}

func (f *TrieFS) Chtimes(_ context.Context, name string, atime, mtime time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, entry, err := f.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chtimes", Path: Resolve(f.cwd, name), Err: err}
	}
	now := time.Now().UTC()
	if atime.IsZero() {
		atime = now
	}
	if mtime.IsZero() {
		mtime = now
	}
	entry.inode.atime = atime.UTC()
	entry.inode.modTime = mtime.UTC()
	return nil
}

func (f *TrieFS) MkdirAll(_ context.Context, name string, perm stdfs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.mkdirAllLocked(Resolve(f.cwd, name), perm)
}

func (f *TrieFS) Remove(_ context.Context, name string, recursive bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	abs, entry, err := f.resolvePathLocked(name, false, false)
	if err != nil {
		return &os.PathError{Op: "remove", Path: Resolve(f.cwd, name), Err: err}
	}
	if abs == "/" {
		return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrPermission}
	}
	if entry.inode.mode.IsDir() && len(entry.inode.children) > 0 && !recursive {
		return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrInvalid}
	}
	f.detachChildLocked(entry.parent, entry.name)
	return nil
}

func (f *TrieFS) Rename(_ context.Context, oldName, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	oldAbs, oldEntry, err := f.resolvePathLocked(oldName, false, false)
	if err != nil {
		return &os.PathError{Op: "rename", Path: Resolve(f.cwd, oldName), Err: err}
	}
	if oldAbs == "/" {
		return &os.PathError{Op: "rename", Path: oldAbs, Err: stdfs.ErrPermission}
	}
	newAbs, newEntry, err := f.resolvePathLocked(newName, false, true)
	if err != nil {
		return &os.PathError{Op: "rename", Path: Resolve(f.cwd, newName), Err: err}
	}
	if newEntry != nil {
		return &os.PathError{Op: "rename", Path: newAbs, Err: stdfs.ErrExist}
	}
	if err := f.mkdirAllLocked(parentDir(newAbs), 0o755); err != nil {
		return err
	}
	_, newParent, err := f.resolveAbsLocked(parentDir(newAbs), true, false, 0)
	if err != nil {
		return err
	}
	if oldEntry.inode.mode.IsDir() && trieIsAncestor(oldEntry, newParent) {
		return &os.PathError{Op: "rename", Path: newAbs, Err: stdfs.ErrInvalid}
	}

	oldParent := oldEntry.parent
	f.detachChildLocked(oldParent, oldEntry.name)
	oldEntry.name = path.Base(newAbs)
	oldEntry.parent = newParent
	f.attachChildLocked(newParent, oldEntry)
	return nil
}

func (f *TrieFS) Getwd() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.cwd
}

func (f *TrieFS) Chdir(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	abs, entry, err := f.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chdir", Path: Resolve(f.cwd, name), Err: err}
	}
	if !entry.inode.mode.IsDir() {
		return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	f.cwd = abs
	return nil
}

func (f *TrieFS) materializePath(ctx context.Context, name string, followFinal bool) (string, *trieDentry, error) {
	for {
		f.mu.RLock()
		abs, entry, err := f.resolvePathLocked(name, followFinal, false)
		if err != nil {
			f.mu.RUnlock()
			return "", nil, err
		}
		if !isLazyTrieFileNode(entry.inode) {
			f.mu.RUnlock()
			return abs, entry, nil
		}
		lazy := entry.inode.lazy
		inodeRef := entry.inode
		f.mu.RUnlock()

		data, err := lazy(ctx)
		if err != nil {
			return "", nil, err
		}
		buf := append([]byte(nil), data...)

		f.mu.Lock()
		current, ok := f.lookupAbsNoFollowLocked(abs)
		if ok && current != nil && current.inode == inodeRef && isLazyTrieFileNode(current.inode) {
			current.inode.data = buf
			current.inode.lazy = nil
			f.mu.Unlock()
			return abs, current, nil
		}
		f.mu.Unlock()
	}
}

func (f *TrieFS) materializeResolvedPath(ctx context.Context, abs string) error {
	for {
		f.mu.RLock()
		entry, ok := f.lookupAbsNoFollowLocked(abs)
		if !ok || entry == nil {
			f.mu.RUnlock()
			return stdfs.ErrNotExist
		}
		if !isLazyTrieFileNode(entry.inode) {
			f.mu.RUnlock()
			return nil
		}
		lazy := entry.inode.lazy
		inodeRef := entry.inode
		f.mu.RUnlock()

		data, err := lazy(ctx)
		if err != nil {
			return err
		}
		buf := append([]byte(nil), data...)

		f.mu.Lock()
		current, ok := f.lookupAbsNoFollowLocked(abs)
		if ok && current != nil && current.inode == inodeRef && isLazyTrieFileNode(current.inode) {
			current.inode.data = buf
			current.inode.lazy = nil
			f.mu.Unlock()
			return nil
		}
		f.mu.Unlock()
	}
}

func (f *TrieFS) resolvePathLocked(name string, followFinal, allowMissingFinal bool) (string, *trieDentry, error) {
	return f.resolveAbsLocked(Resolve(f.cwd, name), followFinal, allowMissingFinal, 0)
}

func (f *TrieFS) resolveAbsLocked(abs string, followFinal, allowMissingFinal bool, depth int) (string, *trieDentry, error) {
	abs = Clean(abs)
	if depth > maxSymlinkDepth {
		return "", nil, errTooManySymlinks
	}
	if abs == "/" {
		return "/", f.root, nil
	}

	parts := trieSplitPath(abs)
	current := f.root
	currentAbs := "/"
	for i, part := range parts {
		child, ok := current.inode.children[part]
		isLast := i == len(parts)-1
		if !ok {
			if isLast && allowMissingFinal {
				return joinChildPath(currentAbs, part), nil, nil
			}
			return "", nil, stdfs.ErrNotExist
		}
		nextAbs := joinChildPath(currentAbs, part)
		if child.inode.mode&stdfs.ModeSymlink != 0 && (!isLast || followFinal) {
			target := Resolve(currentAbs, child.inode.target)
			if !isLast {
				target = Resolve(target, path.Join(parts[i+1:]...))
			}
			return f.resolveAbsLocked(target, true, allowMissingFinal, depth+1)
		}
		if isLast {
			return nextAbs, child, nil
		}
		if !child.inode.mode.IsDir() {
			return "", nil, stdfs.ErrInvalid
		}
		current = child
		currentAbs = nextAbs
	}
	return "/", f.root, nil
}

func (f *TrieFS) resolveCreatePathLocked(abs string, depth int) (string, error) {
	abs = Clean(abs)
	if depth > maxSymlinkDepth {
		return "", errTooManySymlinks
	}
	if abs == "/" {
		return abs, nil
	}

	parts := trieSplitPath(abs)
	current := f.root
	currentAbs := "/"
	for i, part := range parts {
		child, ok := current.inode.children[part]
		isLast := i == len(parts)-1
		if !ok {
			return Resolve(currentAbs, path.Join(parts[i:]...)), nil
		}
		nextAbs := joinChildPath(currentAbs, part)
		if child.inode.mode&stdfs.ModeSymlink != 0 {
			target := Resolve(currentAbs, child.inode.target)
			if !isLast {
				target = Resolve(target, path.Join(parts[i+1:]...))
			}
			return f.resolveCreatePathLocked(target, depth+1)
		}
		if isLast {
			return nextAbs, nil
		}
		if !child.inode.mode.IsDir() {
			return "", stdfs.ErrInvalid
		}
		current = child
		currentAbs = nextAbs
	}
	return abs, nil
}

func (f *TrieFS) lookupAbsNoFollowLocked(abs string) (*trieDentry, bool) {
	abs = Clean(abs)
	if abs == "/" {
		return f.root, true
	}
	current := f.root
	for _, part := range trieSplitPath(abs) {
		next, ok := current.inode.children[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func (f *TrieFS) mkdirAllLocked(name string, perm stdfs.FileMode) error {
	name = Clean(name)
	if name == "/" {
		return nil
	}
	if perm == 0 {
		perm = 0o755
	}

	current := f.root
	currentAbs := "/"
	for _, part := range trieSplitPath(name) {
		nextAbs := joinChildPath(currentAbs, part)
		if child, ok := current.inode.children[part]; ok {
			if child.inode.mode&stdfs.ModeSymlink != 0 {
				resolvedAbs, resolvedEntry, err := f.resolveAbsLocked(nextAbs, true, false, 0)
				if err != nil {
					return &os.PathError{Op: "mkdir", Path: nextAbs, Err: err}
				}
				if !resolvedEntry.inode.mode.IsDir() {
					return &os.PathError{Op: "mkdir", Path: nextAbs, Err: stdfs.ErrInvalid}
				}
				current = resolvedEntry
				currentAbs = resolvedAbs
				continue
			}
			if !child.inode.mode.IsDir() {
				return &os.PathError{Op: "mkdir", Path: nextAbs, Err: stdfs.ErrInvalid}
			}
			current = child
			currentAbs = nextAbs
			continue
		}
		now := time.Now().UTC()
		child := &trieDentry{
			name:   part,
			parent: current,
			inode: &trieInode{
				id:       f.newNodeIDLocked(),
				mode:     stdfs.ModeDir | perm,
				children: make(map[string]*trieDentry),
				atime:    now,
				modTime:  now,
				uid:      DefaultOwnerUID,
				gid:      DefaultOwnerGID,
			},
		}
		f.attachChildLocked(current, child)
		current = child
		currentAbs = nextAbs
	}
	return nil
}

func (f *TrieFS) attachChildLocked(parent, child *trieDentry) {
	if parent == nil || parent.inode == nil || parent.inode.children == nil {
		return
	}
	parent.inode.children[child.name] = child
	parent.inode.dirty = true
}

func (f *TrieFS) detachChildLocked(parent *trieDentry, name string) {
	if parent == nil || parent.inode == nil || parent.inode.children == nil {
		return
	}
	delete(parent.inode.children, name)
	parent.inode.dirty = true
}

func (f *TrieFS) sortedChildNamesLocked(node *trieInode) []string {
	if node == nil || node.children == nil {
		return nil
	}
	if !node.dirty && node.names != nil {
		return node.names
	}
	node.names = node.names[:0]
	for name := range node.children {
		node.names = append(node.names, name)
	}
	slices.Sort(node.names)
	node.dirty = false
	return node.names
}

func (f *TrieFS) dirEntriesLocked(abs string, node *trieInode, names []string) []stdfs.DirEntry {
	entries := make([]stdfs.DirEntry, 0, len(names))
	for _, childName := range names {
		child := node.children[childName]
		if child == nil {
			continue
		}
		entries = append(entries, trieDirEntry{
			fs:   f,
			name: childName,
			path: joinChildPath(abs, childName),
			mode: child.inode.mode,
		})
	}
	return entries
}

func (f *TrieFS) newNodeIDLocked() uint64 {
	f.nextNodeID++
	return f.nextNodeID
}

func trieSplitPath(abs string) []string {
	if abs == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(abs, "/"), "/")
}

func trieIsAncestor(candidate, target *trieDentry) bool {
	for current := target; current != nil; current = current.parent {
		if current == candidate {
			return true
		}
	}
	return false
}

func isLazyTrieFileNode(node *trieInode) bool {
	return node != nil && node.lazy != nil && !node.mode.IsDir() && node.mode&stdfs.ModeSymlink == 0
}

type trieFile struct {
	fs     *TrieFS
	path   string
	flag   int
	offset int64
	closed bool
}

func (f *trieFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, stdfs.ErrClosed
	}
	if !canRead(f.flag) {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}

	for {
		f.fs.mu.RLock()
		entry, ok := f.fs.lookupAbsNoFollowLocked(f.path)
		if !ok || entry == nil {
			f.fs.mu.RUnlock()
			return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrNotExist}
		}
		if isLazyTrieFileNode(entry.inode) {
			f.fs.mu.RUnlock()
			if err := f.fs.materializeResolvedPath(context.Background(), f.path); err != nil {
				return 0, &os.PathError{Op: "read", Path: f.path, Err: err}
			}
			continue
		}
		if entry.inode.mode.IsDir() {
			f.fs.mu.RUnlock()
			return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrInvalid}
		}
		if f.offset >= int64(len(entry.inode.data)) {
			f.fs.mu.RUnlock()
			return 0, io.EOF
		}
		n := copy(p, entry.inode.data[f.offset:])
		f.offset += int64(n)
		f.fs.mu.RUnlock()
		return n, nil
	}
}

func (f *trieFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, stdfs.ErrClosed
	}
	if !canWrite(f.flag) {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrPermission}
	}

	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	entry, ok := f.fs.lookupAbsNoFollowLocked(f.path)
	if !ok || entry == nil {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrNotExist}
	}
	if entry.inode.mode.IsDir() {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrInvalid}
	}
	if isLazyTrieFileNode(entry.inode) {
		f.fs.mu.Unlock()
		if err := f.fs.materializeResolvedPath(context.Background(), f.path); err != nil {
			f.fs.mu.Lock()
			return 0, &os.PathError{Op: "write", Path: f.path, Err: err}
		}
		f.fs.mu.Lock()
		entry, ok = f.fs.lookupAbsNoFollowLocked(f.path)
		if !ok || entry == nil {
			return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrNotExist}
		}
	}

	if f.flag&os.O_APPEND != 0 {
		f.offset = int64(len(entry.inode.data))
	}
	end := int(f.offset) + len(p)
	if end > len(entry.inode.data) {
		entry.inode.data = growBytes(entry.inode.data, end)
	}
	copy(entry.inode.data[int(f.offset):], p)
	f.offset += int64(len(p))
	entry.inode.modTime = time.Now().UTC()
	return len(p), nil
}

func (f *trieFile) ReadFrom(r io.Reader) (int64, error) {
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
		if err == io.EOF {
			return total, nil
		}
		return total, err
	}
}

func (f *trieFile) WriteTo(w io.Writer) (int64, error) {
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
		if err == io.EOF {
			return total, nil
		}
		return total, err
	}
}

func (f *trieFile) Close() error {
	f.closed = true
	return nil
}

func (f *trieFile) Stat() (stdfs.FileInfo, error) {
	if f.closed {
		return nil, stdfs.ErrClosed
	}
	return f.fs.Stat(context.Background(), f.path)
}

type trieFileInfo struct {
	name    string
	size    int64
	mode    stdfs.FileMode
	atime   time.Time
	modTime time.Time
	uid     uint32
	gid     uint32
	nodeID  uint64
}

func newTrieFileInfo(name string, node *trieInode) *trieFileInfo {
	size := int64(len(node.data))
	if node.mode.IsDir() {
		size = 0
	} else if node.mode&stdfs.ModeSymlink != 0 {
		size = int64(len(node.target))
	}
	return &trieFileInfo{
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

func (fi *trieFileInfo) Name() string         { return fi.name }
func (fi *trieFileInfo) Size() int64          { return fi.size }
func (fi *trieFileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi *trieFileInfo) ModTime() time.Time   { return fi.modTime }
func (fi *trieFileInfo) IsDir() bool          { return fi.mode.IsDir() }
func (fi *trieFileInfo) Sys() any {
	return trieStat{
		Atime:     fi.atime.Unix(),
		AtimeNsec: int64(fi.atime.Nanosecond()),
		NodeID:    fi.nodeID,
	}
}

func (fi *trieFileInfo) Ownership() (FileOwnership, bool) {
	return FileOwnership{UID: fi.uid, GID: fi.gid}, true
}

type trieStat struct {
	Atime     int64
	AtimeNsec int64
	NodeID    uint64
}

type trieDirEntry struct {
	fs   *TrieFS
	name string
	path string
	mode stdfs.FileMode
}

func (e trieDirEntry) Name() string         { return e.name }
func (e trieDirEntry) IsDir() bool          { return e.mode.IsDir() }
func (e trieDirEntry) Type() stdfs.FileMode { return e.mode.Type() }
func (e trieDirEntry) Info() (stdfs.FileInfo, error) {
	return e.fs.Lstat(context.Background(), e.path)
}

var _ FileSystem = (*TrieFS)(nil)
var _ File = (*trieFile)(nil)
var _ stdfs.FileInfo = (*trieFileInfo)(nil)
