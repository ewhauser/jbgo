package sqlitefs

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	gosqlite "github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const (
	sqliteRootNodeID      int64 = 1
	sqliteSchemaVersion         = "1"
	sqliteMaxSymlinkDepth       = 40
)

var errTooManySymlinks = errors.New("too many levels of symbolic links")

type Factory struct {
	DBPath string
}

func (f Factory) New(ctx context.Context) (gbfs.FileSystem, error) {
	return newSQLiteFS(ctx, f.DBPath)
}

type sqliteFS struct {
	mu   sync.Mutex
	conn *gosqlite.Conn
	cwd  string
}

type sqliteNode struct {
	id      int64
	kind    string
	mode    stdfs.FileMode
	modTime time.Time
	data    []byte
	target  string
}

type sqliteDirEntry struct {
	name    string
	childID int64
}

func newSQLiteFS(ctx context.Context, dbPath string) (*sqliteFS, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := gosqlite.OpenContext(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	fsys := &sqliteFS{
		conn: conn,
		cwd:  "/",
	}
	if err := fsys.initialize(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return fsys, nil
}

func (s *sqliteFS) initialize() error {
	if err := s.conn.BusyTimeout(5 * time.Second); err != nil {
		return err
	}
	if err := s.conn.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return err
	}
	if err := s.conn.Exec(`
		CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS nodes (
			id INTEGER PRIMARY KEY,
			kind TEXT NOT NULL CHECK(kind IN ('dir', 'file', 'symlink')),
			mode INTEGER NOT NULL,
			mtime_ns INTEGER NOT NULL,
			data BLOB,
			target TEXT
		);
		CREATE TABLE IF NOT EXISTS dir_entries (
			parent_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			child_id INTEGER NOT NULL,
			PRIMARY KEY (parent_id, name),
			FOREIGN KEY (parent_id) REFERENCES nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (child_id) REFERENCES nodes(id)
		);
		CREATE INDEX IF NOT EXISTS idx_dir_entries_child_id
			ON dir_entries(child_id);
	`); err != nil {
		return err
	}

	now := time.Now().UTC().UnixNano()
	rootMode := int64(stdfs.ModeDir | 0o755)
	if err := s.execLocked(`INSERT OR IGNORE INTO meta(key, value) VALUES (?1, ?2)`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindText(1, "schema_version"); err != nil {
			return err
		}
		return stmt.BindText(2, sqliteSchemaVersion)
	}); err != nil {
		return err
	}
	if err := s.execLocked(`UPDATE meta SET value = ?2 WHERE key = ?1`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindText(1, "schema_version"); err != nil {
			return err
		}
		return stmt.BindText(2, sqliteSchemaVersion)
	}); err != nil {
		return err
	}
	return s.execLocked(`
		INSERT OR IGNORE INTO nodes(id, kind, mode, mtime_ns, data, target)
		VALUES (?1, 'dir', ?2, ?3, NULL, NULL)
	`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindInt64(1, sqliteRootNodeID); err != nil {
			return err
		}
		if err := stmt.BindInt64(2, rootMode); err != nil {
			return err
		}
		return stmt.BindInt64(3, now)
	})
}

func (s *sqliteFS) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Close()
}

func (s *sqliteFS) Close() error {
	return s.close()
}

func (s *sqliteFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	return s.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (s *sqliteFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	requested := gbfs.Resolve(s.cwd, name)
	var (
		abs  string
		node *sqliteNode
		err  error
	)

	mutates := flag&os.O_CREATE != 0 || (flag&os.O_TRUNC != 0 && canWrite(flag))
	if mutates {
		err = s.withWriteTxLocked(func() error {
			abs, node, err = s.prepareOpenFileLocked(name, requested, flag, perm)
			return err
		})
	} else {
		abs, node, err = s.prepareOpenFileLocked(name, requested, flag, perm)
	}
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, &os.PathError{Op: "open", Path: requested, Err: stdfs.ErrNotExist}
	}

	offset := int64(0)
	if flag&os.O_APPEND != 0 {
		offset = int64(len(node.data))
	}
	return &sqliteFile{
		fs:     s,
		path:   abs,
		flag:   flag,
		offset: offset,
	}, nil
}

func (s *sqliteFS) prepareOpenFileLocked(name, requested string, flag int, perm stdfs.FileMode) (string, *sqliteNode, error) {
	var (
		abs  string
		node *sqliteNode
		err  error
	)

	if flag&os.O_CREATE != 0 {
		abs, err = s.resolveCreatePathLocked(requested, 0)
		if err != nil {
			return "", nil, &os.PathError{Op: "open", Path: requested, Err: err}
		}
		node, err = s.lookupExactNodeLocked(abs)
		if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return "", nil, err
		}
		if errors.Is(err, stdfs.ErrNotExist) {
			node = nil
		}
	} else {
		abs, node, err = s.resolvePathLocked(name, true, false)
		if err != nil {
			return "", nil, &os.PathError{Op: "open", Path: requested, Err: err}
		}
	}

	if node == nil {
		if flag&os.O_CREATE == 0 {
			return "", nil, &os.PathError{Op: "open", Path: requested, Err: stdfs.ErrNotExist}
		}
		if err := s.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
			return "", nil, err
		}
		if perm == 0 {
			perm = 0o644
		}
		now := time.Now().UTC()
		nodeID, err := s.insertNodeLocked("file", perm.Perm(), now, nil, "")
		if err != nil {
			return "", nil, err
		}
		parentNode, err := s.mustResolveDirLocked(parentDir(abs))
		if err != nil {
			return "", nil, err
		}
		if err := s.insertDirEntryLocked(parentNode.id, path.Base(abs), nodeID); err != nil {
			return "", nil, err
		}
		node, err = s.loadNodeLocked(nodeID)
		if err != nil {
			return "", nil, err
		}
	} else if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
		return "", nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrExist}
	}

	if node.mode.IsDir() {
		return "", nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrInvalid}
	}

	if flag&os.O_TRUNC != 0 && canWrite(flag) {
		node.data = nil
		node.modTime = time.Now().UTC()
		if err := s.updateNodeContentLocked(node.id, node.data, node.modTime); err != nil {
			return "", nil, err
		}
	}

	return abs, node, nil
}

func (s *sqliteFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	abs, node, err := s.resolvePathLocked(name, true, false)
	if err != nil {
		return nil, &os.PathError{Op: "stat", Path: gbfs.Resolve(s.cwd, name), Err: err}
	}
	return newSQLiteFileInfo(path.Base(abs), node), nil
}

func (s *sqliteFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	abs, node, err := s.resolvePathLocked(name, false, false)
	if err != nil {
		return nil, &os.PathError{Op: "lstat", Path: gbfs.Resolve(s.cwd, name), Err: err}
	}
	return newSQLiteFileInfo(path.Base(abs), node), nil
}

func (s *sqliteFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	abs, node, err := s.resolvePathLocked(name, true, false)
	if err != nil {
		return nil, &os.PathError{Op: "readdir", Path: gbfs.Resolve(s.cwd, name), Err: err}
	}
	if !node.mode.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	}

	children, err := s.listChildrenLocked(node.id)
	if err != nil {
		return nil, err
	}
	entries := make([]stdfs.DirEntry, 0, len(children))
	for _, child := range children {
		childNode, err := s.loadNodeLocked(child.childID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, stdfs.FileInfoToDirEntry(newSQLiteFileInfo(child.name, childNode)))
	}
	return entries, nil
}

func (s *sqliteFS) Readlink(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	abs, node, err := s.resolvePathLocked(name, false, false)
	if err != nil {
		return "", &os.PathError{Op: "readlink", Path: gbfs.Resolve(s.cwd, name), Err: err}
	}
	if node.mode&stdfs.ModeSymlink == 0 {
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrInvalid}
	}
	return node.target, nil
}

func (s *sqliteFS) Realpath(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	abs, _, err := s.resolvePathLocked(name, true, false)
	if err != nil {
		return "", &os.PathError{Op: "realpath", Path: gbfs.Resolve(s.cwd, name), Err: err}
	}
	return abs, nil
}

func (s *sqliteFS) Symlink(ctx context.Context, target, linkName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withWriteTxLocked(func() error {
		abs := gbfs.Resolve(s.cwd, linkName)
		if _, err := s.lookupExactNodeLocked(abs); err == nil {
			return &os.PathError{Op: "symlink", Path: abs, Err: stdfs.ErrExist}
		} else if !errors.Is(err, stdfs.ErrNotExist) {
			return err
		}
		if err := s.mkdirAllLocked(parentDir(abs), 0o755); err != nil {
			return err
		}
		parentNode, err := s.mustResolveDirLocked(parentDir(abs))
		if err != nil {
			return err
		}
		nodeID, err := s.insertNodeLocked("symlink", stdfs.ModeSymlink|0o777, time.Now().UTC(), nil, target)
		if err != nil {
			return err
		}
		return s.insertDirEntryLocked(parentNode.id, path.Base(abs), nodeID)
	})
}

func (s *sqliteFS) Link(ctx context.Context, oldName, newName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withWriteTxLocked(func() error {
		oldAbs, node, err := s.resolvePathLocked(oldName, false, false)
		if err != nil {
			return &os.PathError{Op: "link", Path: gbfs.Resolve(s.cwd, oldName), Err: err}
		}
		if node.mode.IsDir() {
			return &os.PathError{Op: "link", Path: oldAbs, Err: stdfs.ErrInvalid}
		}

		newAbs, existing, err := s.resolvePathLocked(newName, false, true)
		if err != nil {
			return &os.PathError{Op: "link", Path: gbfs.Resolve(s.cwd, newName), Err: err}
		}
		if existing != nil {
			return &os.PathError{Op: "link", Path: newAbs, Err: stdfs.ErrExist}
		}

		if err := s.mkdirAllLocked(parentDir(newAbs), 0o755); err != nil {
			return err
		}
		parentNode, err := s.mustResolveDirLocked(parentDir(newAbs))
		if err != nil {
			return err
		}
		return s.insertDirEntryLocked(parentNode.id, path.Base(newAbs), node.id)
	})
}

func (s *sqliteFS) Chown(_ context.Context, name string, _, _ uint32, _ bool) error {
	return &os.PathError{Op: "chown", Path: gbfs.Resolve(s.cwd, name), Err: stdfs.ErrPermission}
}

func (s *sqliteFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withWriteTxLocked(func() error {
		_, node, err := s.resolvePathLocked(name, true, false)
		if err != nil {
			return &os.PathError{Op: "chmod", Path: gbfs.Resolve(s.cwd, name), Err: err}
		}
		typeBits := node.mode &^ stdfs.ModePerm
		node.mode = typeBits | mode.Perm()
		node.modTime = time.Now().UTC()
		return s.updateNodeMetadataLocked(node.id, node.mode, node.modTime)
	})
}

func (s *sqliteFS) Chtimes(ctx context.Context, name string, _, mtime time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withWriteTxLocked(func() error {
		_, node, err := s.resolvePathLocked(name, true, false)
		if err != nil {
			return &os.PathError{Op: "chtimes", Path: gbfs.Resolve(s.cwd, name), Err: err}
		}
		if mtime.IsZero() {
			mtime = time.Now().UTC()
		}
		return s.updateNodeMetadataLocked(node.id, node.mode, mtime.UTC())
	})
}

func (s *sqliteFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withWriteTxLocked(func() error {
		return s.mkdirAllLocked(gbfs.Resolve(s.cwd, name), perm)
	})
}

func (s *sqliteFS) Remove(ctx context.Context, name string, recursive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withWriteTxLocked(func() error {
		abs, node, err := s.resolvePathLocked(name, false, false)
		if err != nil {
			return &os.PathError{Op: "remove", Path: gbfs.Resolve(s.cwd, name), Err: err}
		}
		if abs == "/" {
			return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrPermission}
		}

		parentNode, err := s.mustResolveDirLocked(parentDir(abs))
		if err != nil {
			return err
		}

		if node.mode.IsDir() {
			hasChildren, err := s.hasChildrenLocked(node.id)
			if err != nil {
				return err
			}
			if hasChildren && !recursive {
				return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrInvalid}
			}
			if err := s.removeSubtreeLocked(node.id); err != nil {
				return err
			}
			if err := s.deleteDirEntryLocked(parentNode.id, path.Base(abs)); err != nil {
				return err
			}
			return s.deleteNodeLocked(node.id)
		}

		if err := s.deleteDirEntryLocked(parentNode.id, path.Base(abs)); err != nil {
			return err
		}
		return s.deleteNodeIfOrphanLocked(node.id)
	})
}

func (s *sqliteFS) Rename(ctx context.Context, oldName, newName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withWriteTxLocked(func() error {
		oldAbs, node, err := s.resolvePathLocked(oldName, false, false)
		if err != nil {
			return &os.PathError{Op: "rename", Path: gbfs.Resolve(s.cwd, oldName), Err: err}
		}

		newAbs, existing, err := s.resolvePathLocked(newName, false, true)
		if err != nil {
			return &os.PathError{Op: "rename", Path: gbfs.Resolve(s.cwd, newName), Err: err}
		}
		if existing != nil {
			return &os.PathError{Op: "rename", Path: newAbs, Err: stdfs.ErrExist}
		}
		if node.mode.IsDir() && strings.HasPrefix(newAbs, oldAbs+"/") {
			return &os.PathError{Op: "rename", Path: newAbs, Err: stdfs.ErrInvalid}
		}

		if err := s.mkdirAllLocked(parentDir(newAbs), 0o755); err != nil {
			return err
		}

		oldParent, err := s.mustResolveDirLocked(parentDir(oldAbs))
		if err != nil {
			return err
		}
		newParent, err := s.mustResolveDirLocked(parentDir(newAbs))
		if err != nil {
			return err
		}

		if err := s.deleteDirEntryLocked(oldParent.id, path.Base(oldAbs)); err != nil {
			return err
		}
		return s.insertDirEntryLocked(newParent.id, path.Base(newAbs), node.id)
	})
}

func (s *sqliteFS) Getwd() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwd
}

func (s *sqliteFS) Chdir(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	abs, node, err := s.resolvePathLocked(name, true, false)
	if err != nil {
		return &os.PathError{Op: "chdir", Path: gbfs.Resolve(s.cwd, name), Err: err}
	}
	if !node.mode.IsDir() {
		return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	s.cwd = abs
	return nil
}

func (s *sqliteFS) resolvePathLocked(name string, followFinal, allowMissingFinal bool) (string, *sqliteNode, error) {
	return s.resolveAbsLocked(gbfs.Resolve(s.cwd, name), followFinal, allowMissingFinal, 0)
}

func (s *sqliteFS) resolveAbsLocked(abs string, followFinal, allowMissingFinal bool, depth int) (string, *sqliteNode, error) {
	abs = gbfs.Clean(abs)
	if depth > sqliteMaxSymlinkDepth {
		return "", nil, errTooManySymlinks
	}
	if abs == "/" {
		node, err := s.loadNodeLocked(sqliteRootNodeID)
		if err != nil {
			return "", nil, err
		}
		return "/", node, nil
	}

	currentPath := "/"
	currentID := sqliteRootNodeID
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, part := range parts {
		childID, ok, err := s.lookupChildLocked(currentID, part)
		isLast := i == len(parts)-1
		if err != nil {
			return "", nil, err
		}
		if !ok {
			if isLast && allowMissingFinal {
				return gbfs.Resolve(currentPath, part), nil, nil
			}
			return "", nil, stdfs.ErrNotExist
		}

		nextPath := gbfs.Resolve(currentPath, part)
		node, err := s.loadNodeLocked(childID)
		if err != nil {
			return "", nil, err
		}
		if node.mode&stdfs.ModeSymlink != 0 && (!isLast || followFinal) {
			target := gbfs.Resolve(parentDir(nextPath), node.target)
			if !isLast {
				target = gbfs.Resolve(target, path.Join(parts[i+1:]...))
			}
			return s.resolveAbsLocked(target, true, allowMissingFinal && isLast, depth+1)
		}
		if isLast {
			return nextPath, node, nil
		}
		if !node.mode.IsDir() {
			return "", nil, stdfs.ErrInvalid
		}

		currentPath = nextPath
		currentID = node.id
	}

	return "/", nil, stdfs.ErrNotExist
}

func (s *sqliteFS) resolveCreatePathLocked(abs string, depth int) (string, error) {
	abs = gbfs.Clean(abs)
	if depth > sqliteMaxSymlinkDepth {
		return "", errTooManySymlinks
	}
	if abs == "/" {
		return abs, nil
	}

	currentPath := "/"
	currentID := sqliteRootNodeID
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, part := range parts {
		isLast := i == len(parts)-1
		nextPath := gbfs.Resolve(currentPath, part)

		childID, ok, err := s.lookupChildLocked(currentID, part)
		if err != nil {
			return "", err
		}
		if !ok {
			return gbfs.Resolve(currentPath, path.Join(parts[i:]...)), nil
		}

		node, err := s.loadNodeLocked(childID)
		if err != nil {
			return "", err
		}
		if node.mode&stdfs.ModeSymlink != 0 {
			target := gbfs.Resolve(parentDir(nextPath), node.target)
			if !isLast {
				target = gbfs.Resolve(target, path.Join(parts[i+1:]...))
			}
			return s.resolveCreatePathLocked(target, depth+1)
		}
		if isLast {
			return nextPath, nil
		}
		if !node.mode.IsDir() {
			return "", stdfs.ErrInvalid
		}

		currentPath = nextPath
		currentID = node.id
	}

	return abs, nil
}

func (s *sqliteFS) mkdirAllLocked(name string, perm stdfs.FileMode) error {
	name = gbfs.Clean(name)
	if name == "/" {
		return nil
	}

	currentPath := "/"
	currentID := sqliteRootNodeID
	for part := range strings.SplitSeq(strings.TrimPrefix(name, "/"), "/") {
		nextPath := gbfs.Resolve(currentPath, part)

		childID, ok, err := s.lookupChildLocked(currentID, part)
		if err != nil {
			return err
		}
		if ok {
			node, err := s.loadNodeLocked(childID)
			if err != nil {
				return err
			}
			if node.mode&stdfs.ModeSymlink != 0 {
				resolved, resolvedNode, err := s.resolveAbsLocked(nextPath, true, false, 0)
				if err != nil {
					return &os.PathError{Op: "mkdir", Path: nextPath, Err: err}
				}
				if !resolvedNode.mode.IsDir() {
					return &os.PathError{Op: "mkdir", Path: nextPath, Err: stdfs.ErrInvalid}
				}
				currentPath = resolved
				currentID = resolvedNode.id
				continue
			}
			if !node.mode.IsDir() {
				return &os.PathError{Op: "mkdir", Path: nextPath, Err: stdfs.ErrInvalid}
			}
			currentPath = nextPath
			currentID = node.id
			continue
		}

		if perm == 0 {
			perm = 0o755
		}
		now := time.Now().UTC()
		nodeID, err := s.insertNodeLocked("dir", stdfs.ModeDir|perm.Perm(), now, nil, "")
		if err != nil {
			return err
		}
		if err := s.insertDirEntryLocked(currentID, part, nodeID); err != nil {
			return err
		}

		currentPath = nextPath
		currentID = nodeID
	}

	return nil
}

func (s *sqliteFS) mustResolveDirLocked(abs string) (*sqliteNode, error) {
	_, node, err := s.resolveAbsLocked(abs, true, false, 0)
	if err != nil {
		return nil, err
	}
	if !node.mode.IsDir() {
		return nil, &os.PathError{Op: "stat", Path: abs, Err: stdfs.ErrInvalid}
	}
	return node, nil
}

func (s *sqliteFS) removeSubtreeLocked(dirID int64) error {
	children, err := s.listChildrenLocked(dirID)
	if err != nil {
		return err
	}
	for _, child := range children {
		node, err := s.loadNodeLocked(child.childID)
		if err != nil {
			return err
		}
		if node.mode.IsDir() {
			if err := s.removeSubtreeLocked(node.id); err != nil {
				return err
			}
			if err := s.deleteDirEntryLocked(dirID, child.name); err != nil {
				return err
			}
			if err := s.deleteNodeLocked(node.id); err != nil {
				return err
			}
			continue
		}
		if err := s.deleteDirEntryLocked(dirID, child.name); err != nil {
			return err
		}
		if err := s.deleteNodeIfOrphanLocked(node.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteFS) lookupExactNodeLocked(abs string) (*sqliteNode, error) {
	_, node, err := s.resolveAbsLocked(abs, false, false, 0)
	return node, err
}

func (s *sqliteFS) lookupChildLocked(parentID int64, name string) (id int64, found bool, err error) {
	var cid int64
	ok, qerr := s.queryLocked(`SELECT child_id FROM dir_entries WHERE parent_id = ?1 AND name = ?2`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindInt64(1, parentID); err != nil {
			return err
		}
		return stmt.BindText(2, name)
	}, func(stmt *gosqlite.Stmt) error {
		cid = stmt.ColumnInt64(0)
		return nil
	})
	if qerr != nil {
		return 0, false, qerr
	}
	return cid, ok, nil
}

func (s *sqliteFS) loadNodeLocked(nodeID int64) (*sqliteNode, error) {
	var node sqliteNode
	ok, err := s.queryLocked(`
		SELECT id, kind, mode, mtime_ns, data, target
		FROM nodes
		WHERE id = ?1
	`, func(stmt *gosqlite.Stmt) error {
		return stmt.BindInt64(1, nodeID)
	}, func(stmt *gosqlite.Stmt) error {
		node.id = stmt.ColumnInt64(0)
		node.kind = stmt.ColumnText(1)
		node.mode = stdfs.FileMode(stmt.ColumnInt64(2))
		node.modTime = time.Unix(0, stmt.ColumnInt64(3)).UTC()
		if stmt.ColumnType(4) != gosqlite.NULL {
			node.data = append([]byte(nil), stmt.ColumnBlob(4, nil)...)
		}
		if stmt.ColumnType(5) != gosqlite.NULL {
			node.target = stmt.ColumnText(5)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, stdfs.ErrNotExist
	}
	return &node, nil
}

func (s *sqliteFS) listChildrenLocked(parentID int64) ([]sqliteDirEntry, error) {
	stmt, _, err := s.conn.Prepare(`
		SELECT name, child_id
		FROM dir_entries
		WHERE parent_id = ?1
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stmt.Close() }()

	if err := stmt.BindInt64(1, parentID); err != nil {
		return nil, err
	}

	var entries []sqliteDirEntry
	for stmt.Step() {
		entries = append(entries, sqliteDirEntry{
			name:    stmt.ColumnText(0),
			childID: stmt.ColumnInt64(1),
		})
	}
	if err := stmt.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *sqliteFS) hasChildrenLocked(parentID int64) (bool, error) {
	var childID int64
	ok, err := s.queryLocked(`SELECT child_id FROM dir_entries WHERE parent_id = ?1 LIMIT 1`, func(stmt *gosqlite.Stmt) error {
		return stmt.BindInt64(1, parentID)
	}, func(stmt *gosqlite.Stmt) error {
		childID = stmt.ColumnInt64(0)
		return nil
	})
	if err != nil {
		return false, err
	}
	return ok && childID != 0, nil
}

func (s *sqliteFS) insertNodeLocked(kind string, mode stdfs.FileMode, modTime time.Time, data []byte, target string) (int64, error) {
	stmt, _, err := s.conn.Prepare(`
		INSERT INTO nodes(kind, mode, mtime_ns, data, target)
		VALUES (?1, ?2, ?3, ?4, ?5)
	`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()

	if err := stmt.BindText(1, kind); err != nil {
		return 0, err
	}
	if err := stmt.BindInt64(2, int64(mode)); err != nil {
		return 0, err
	}
	if err := stmt.BindInt64(3, modTime.UTC().UnixNano()); err != nil {
		return 0, err
	}
	if len(data) == 0 {
		if err := stmt.BindNull(4); err != nil {
			return 0, err
		}
	} else if err := stmt.BindBlob(4, data); err != nil {
		return 0, err
	}
	if target == "" {
		if err := stmt.BindNull(5); err != nil {
			return 0, err
		}
	} else if err := stmt.BindText(5, target); err != nil {
		return 0, err
	}
	if err := stmt.Exec(); err != nil {
		return 0, err
	}
	return s.conn.LastInsertRowID(), nil
}

func (s *sqliteFS) insertDirEntryLocked(parentID int64, name string, childID int64) error {
	return s.execLocked(`
		INSERT INTO dir_entries(parent_id, name, child_id)
		VALUES (?1, ?2, ?3)
	`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindInt64(1, parentID); err != nil {
			return err
		}
		if err := stmt.BindText(2, name); err != nil {
			return err
		}
		return stmt.BindInt64(3, childID)
	})
}

func (s *sqliteFS) updateNodeContentLocked(nodeID int64, data []byte, modTime time.Time) error {
	return s.execLocked(`
		UPDATE nodes
		SET data = ?2, mtime_ns = ?3
		WHERE id = ?1
	`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindInt64(1, nodeID); err != nil {
			return err
		}
		if len(data) == 0 {
			if err := stmt.BindNull(2); err != nil {
				return err
			}
		} else if err := stmt.BindBlob(2, data); err != nil {
			return err
		}
		return stmt.BindInt64(3, modTime.UTC().UnixNano())
	})
}

func (s *sqliteFS) updateNodeMetadataLocked(nodeID int64, mode stdfs.FileMode, modTime time.Time) error {
	return s.execLocked(`
		UPDATE nodes
		SET mode = ?2, mtime_ns = ?3
		WHERE id = ?1
	`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindInt64(1, nodeID); err != nil {
			return err
		}
		if err := stmt.BindInt64(2, int64(mode)); err != nil {
			return err
		}
		return stmt.BindInt64(3, modTime.UTC().UnixNano())
	})
}

func (s *sqliteFS) deleteDirEntryLocked(parentID int64, name string) error {
	return s.execLocked(`DELETE FROM dir_entries WHERE parent_id = ?1 AND name = ?2`, func(stmt *gosqlite.Stmt) error {
		if err := stmt.BindInt64(1, parentID); err != nil {
			return err
		}
		return stmt.BindText(2, name)
	})
}

func (s *sqliteFS) deleteNodeLocked(nodeID int64) error {
	return s.execLocked(`DELETE FROM nodes WHERE id = ?1`, func(stmt *gosqlite.Stmt) error {
		return stmt.BindInt64(1, nodeID)
	})
}

func (s *sqliteFS) deleteNodeIfOrphanLocked(nodeID int64) error {
	if nodeID == sqliteRootNodeID {
		return nil
	}
	count, err := s.incomingLinkCountLocked(nodeID)
	if err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	return s.deleteNodeLocked(nodeID)
}

func (s *sqliteFS) incomingLinkCountLocked(nodeID int64) (int64, error) {
	var count int64
	ok, err := s.queryLocked(`SELECT COUNT(*) FROM dir_entries WHERE child_id = ?1`, func(stmt *gosqlite.Stmt) error {
		return stmt.BindInt64(1, nodeID)
	}, func(stmt *gosqlite.Stmt) error {
		count = stmt.ColumnInt64(0)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return count, nil
}

func (s *sqliteFS) execLocked(sql string, bind func(*gosqlite.Stmt) error) error {
	stmt, _, err := s.conn.Prepare(sql)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	if bind != nil {
		if err := bind(stmt); err != nil {
			return err
		}
	}
	return stmt.Exec()
}

func (s *sqliteFS) queryLocked(sql string, bind, scan func(*gosqlite.Stmt) error) (bool, error) {
	stmt, _, err := s.conn.Prepare(sql)
	if err != nil {
		return false, err
	}
	defer func() { _ = stmt.Close() }()

	if bind != nil {
		if err := bind(stmt); err != nil {
			return false, err
		}
	}
	if !stmt.Step() {
		return false, stmt.Err()
	}
	if scan != nil {
		if err := scan(stmt); err != nil {
			return false, err
		}
	}
	return true, stmt.Err()
}

func (s *sqliteFS) withWriteTxLocked(fn func() error) error {
	tx, err := s.conn.BeginImmediate()
	if err != nil {
		return err
	}
	if err := fn(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type sqliteFile struct {
	fs     *sqliteFS
	path   string
	flag   int
	offset int64
	closed atomic.Bool
}

func (f *sqliteFile) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !canRead(f.flag) {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: stdfs.ErrPermission}
	}

	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	node, err := f.fs.lookupExactNodeLocked(f.path)
	if err != nil {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: err}
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

func (f *sqliteFile) Write(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if !canWrite(f.flag) {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrPermission}
	}

	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	var wrote int
	err := f.fs.withWriteTxLocked(func() error {
		node, err := f.fs.lookupExactNodeLocked(f.path)
		if err != nil {
			return &os.PathError{Op: "write", Path: f.path, Err: err}
		}
		if node.mode.IsDir() {
			return &os.PathError{Op: "write", Path: f.path, Err: stdfs.ErrInvalid}
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
		if err := f.fs.updateNodeContentLocked(node.id, node.data, node.modTime); err != nil {
			return err
		}
		wrote = len(p)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return wrote, nil
}

func (f *sqliteFile) Close() error {
	f.closed.Store(true)
	return nil
}

func (f *sqliteFile) Stat() (stdfs.FileInfo, error) {
	if f.closed.Load() {
		return nil, stdfs.ErrClosed
	}
	return f.fs.Stat(context.Background(), f.path)
}

type sqliteFileInfo struct {
	name    string
	size    int64
	mode    stdfs.FileMode
	modTime time.Time
	nodeID  int64
}

func newSQLiteFileInfo(name string, node *sqliteNode) sqliteFileInfo {
	size := int64(len(node.data))
	if node.mode.IsDir() {
		size = 0
	} else if node.mode&stdfs.ModeSymlink != 0 {
		size = int64(len(node.target))
	}
	return sqliteFileInfo{
		name:    name,
		size:    size,
		mode:    node.mode,
		modTime: node.modTime,
		nodeID:  node.id,
	}
}

func (fi sqliteFileInfo) Name() string         { return fi.name }
func (fi sqliteFileInfo) Size() int64          { return fi.size }
func (fi sqliteFileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi sqliteFileInfo) ModTime() time.Time   { return fi.modTime }
func (fi sqliteFileInfo) IsDir() bool          { return fi.mode.IsDir() }
func (fi sqliteFileInfo) Sys() any             { return sqliteStat{NodeID: fi.nodeID} }

type sqliteStat struct {
	NodeID int64
}

func parentDir(name string) string {
	if name == "/" {
		return "/"
	}
	return gbfs.Clean(path.Dir(name))
}

func canRead(flag int) bool {
	return flag&(os.O_RDONLY|os.O_WRONLY|os.O_RDWR) != os.O_WRONLY
}

func canWrite(flag int) bool {
	switch flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY, os.O_RDWR:
		return true
	default:
		return false
	}
}

var _ gbfs.Factory = Factory{}
var _ gbfs.FileSystem = (*sqliteFS)(nil)
var _ gbfs.File = (*sqliteFile)(nil)
var _ stdfs.FileInfo = sqliteFileInfo{}
