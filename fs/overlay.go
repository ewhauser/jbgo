package fs

import (
	"context"
	"errors"
	stdfs "io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// OverlayFS is a copy-on-write filesystem with a writable in-memory upper
// layer and a read-only lower layer.
type OverlayFS struct {
	lower FileSystem
	upper *MemoryFS

	mu         sync.RWMutex
	cwd        string
	tombstones map[string]struct{}
}

// NewOverlay creates a concrete overlay filesystem over lower.
func NewOverlay(lower FileSystem) *OverlayFS {
	if lower == nil {
		lower = NewMemory()
	}
	return &OverlayFS{
		lower:      lower,
		upper:      NewMemory(),
		cwd:        "/",
		tombstones: make(map[string]struct{}),
	}
}

func (o *OverlayFS) Open(ctx context.Context, name string) (File, error) {
	return o.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (o *OverlayFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	abs := o.resolve(name)
	if !hasWriteIntent(flag) {
		if _, err := o.upper.Lstat(ctx, abs); err == nil {
			return o.upper.OpenFile(ctx, abs, flag, perm)
		} else if !errors.Is(err, stdfs.ErrNotExist) {
			return nil, err
		}
		if o.isHidden(abs) {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrNotExist}
		}
		return o.lower.OpenFile(ctx, abs, flag, perm)
	}

	info, source, err := o.visibleInfo(ctx, abs)
	switch {
	case err == nil && source == overlaySourceUpper:
		return o.upper.OpenFile(ctx, abs, flag, perm)
	case err == nil && source == overlaySourceLower:
		if info.IsDir() {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrInvalid}
		}
		if err := cloneFile(ctx, o.lower, abs, o.upper, abs, info.Mode().Perm()); err != nil {
			return nil, err
		}
		return o.upper.OpenFile(ctx, abs, flag, perm)
	case err == nil:
		return o.upper.OpenFile(ctx, abs, flag, perm)
	case errors.Is(err, stdfs.ErrNotExist):
		if flag&os.O_CREATE == 0 {
			return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrNotExist}
		}
		return o.upper.OpenFile(ctx, abs, flag, perm)
	default:
		return nil, err
	}
}

func (o *OverlayFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	return o.statLike(ctx, name)
}

func (o *OverlayFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	return o.statLike(ctx, name)
}

func (o *OverlayFS) statLike(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs := o.resolve(name)
	info, _, err := o.visibleInfo(ctx, abs)
	if err != nil {
		return nil, toPathError("stat", abs, err)
	}
	return info, nil
}

func (o *OverlayFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	abs := o.resolve(name)
	upperInfo, upperExists, err := lstatMaybe(ctx, o.upper, abs)
	if err != nil {
		return nil, toPathError("readdir", abs, err)
	}
	if upperExists && !upperInfo.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	if !upperExists && o.isHidden(abs) {
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrNotExist}
	}

	lowerInfo, lowerExists, err := lstatMaybe(ctx, o.lower, abs)
	if err != nil {
		return nil, toPathError("readdir", abs, err)
	}
	if lowerExists && !lowerInfo.IsDir() && !upperExists {
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	if !upperExists && !lowerExists {
		return nil, &os.PathError{Op: "readdir", Path: abs, Err: stdfs.ErrNotExist}
	}

	entriesByName := make(map[string]stdfs.DirEntry)
	if upperExists {
		upperEntries, err := o.upper.ReadDir(ctx, abs)
		if err != nil {
			return nil, err
		}
		for _, entry := range upperEntries {
			entriesByName[entry.Name()] = entry
		}
	}

	if lowerExists && !o.isExactTombstone(abs) {
		lowerEntries, err := o.lower.ReadDir(ctx, abs)
		if err != nil {
			return nil, err
		}
		for _, entry := range lowerEntries {
			child := path.Join(abs, entry.Name())
			if o.isHidden(child) {
				continue
			}
			if _, exists := entriesByName[entry.Name()]; exists {
				continue
			}
			entriesByName[entry.Name()] = entry
		}
	}

	names := make([]string, 0, len(entriesByName))
	for name := range entriesByName {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]stdfs.DirEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, entriesByName[name])
	}
	return entries, nil
}

func (o *OverlayFS) Readlink(ctx context.Context, name string) (string, error) {
	abs := o.resolve(name)
	if _, err := o.upper.Lstat(ctx, abs); err == nil {
		return o.upper.Readlink(ctx, abs)
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return "", err
	}
	if o.isHidden(abs) {
		return "", &os.PathError{Op: "readlink", Path: abs, Err: stdfs.ErrNotExist}
	}
	target, err := o.lower.Readlink(ctx, abs)
	if err != nil {
		return "", toPathError("readlink", abs, err)
	}
	return target, nil
}

func (o *OverlayFS) Realpath(ctx context.Context, name string) (string, error) {
	abs := o.resolve(name)
	resolved, err := o.resolveRealpath(ctx, abs, 0)
	if err != nil {
		return "", &os.PathError{Op: "realpath", Path: abs, Err: err}
	}
	return resolved, nil
}

func (o *OverlayFS) Symlink(ctx context.Context, target, linkName string) error {
	abs := o.resolve(linkName)
	if err := o.upper.Symlink(ctx, target, abs); err != nil {
		return toPathError("symlink", abs, err)
	}
	o.clearTombstonesUnder(abs)
	return nil
}

func (o *OverlayFS) Link(ctx context.Context, oldName, newName string) error {
	oldAbs := o.resolve(oldName)
	newAbs := o.resolve(newName)

	info, source, err := o.visibleInfo(ctx, oldAbs)
	if err != nil {
		return &os.PathError{Op: "link", Path: oldAbs, Err: stdfs.ErrNotExist}
	}
	if info.IsDir() {
		return &os.PathError{Op: "link", Path: oldAbs, Err: stdfs.ErrInvalid}
	}
	if _, _, err := o.visibleInfo(ctx, newAbs); err == nil {
		return &os.PathError{Op: "link", Path: newAbs, Err: stdfs.ErrExist}
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return toPathError("link", newAbs, err)
	}

	switch source {
	case overlaySourceUpper:
		if err := o.upper.Link(ctx, oldAbs, newAbs); err != nil {
			return toPathError("link", oldAbs, err)
		}
	case overlaySourceLower:
		if _, err := o.upper.Lstat(ctx, oldAbs); errors.Is(err, stdfs.ErrNotExist) {
			if err := clonePath(ctx, o.lower, oldAbs, o.upper, oldAbs); err != nil {
				return toPathError("link", oldAbs, err)
			}
		} else if err != nil {
			return toPathError("link", oldAbs, err)
		}
		if err := o.upper.Link(ctx, oldAbs, newAbs); err != nil {
			return toPathError("link", oldAbs, err)
		}
	default:
		return &os.PathError{Op: "link", Path: oldAbs, Err: stdfs.ErrNotExist}
	}
	o.clearTombstonesUnder(newAbs)
	return nil
}

func (o *OverlayFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	abs := o.resolve(name)
	_, source, err := o.visibleInfo(ctx, abs)
	if err != nil {
		return &os.PathError{Op: "chmod", Path: abs, Err: stdfs.ErrNotExist}
	}
	if source == overlaySourceLower {
		if err := clonePath(ctx, o.lower, abs, o.upper, abs); err != nil {
			return toPathError("chmod", abs, err)
		}
	}
	if err := o.upper.Chmod(ctx, abs, mode); err != nil {
		return toPathError("chmod", abs, err)
	}
	return nil
}

func (o *OverlayFS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	abs := o.resolve(name)
	_, source, err := o.visibleInfo(ctx, abs)
	if err != nil {
		return &os.PathError{Op: "chtimes", Path: abs, Err: stdfs.ErrNotExist}
	}
	if source == overlaySourceLower {
		if err := clonePath(ctx, o.lower, abs, o.upper, abs); err != nil {
			return toPathError("chtimes", abs, err)
		}
	}
	if err := o.upper.Chtimes(ctx, abs, atime, mtime); err != nil {
		return toPathError("chtimes", abs, err)
	}
	return nil
}

func (o *OverlayFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs := o.resolve(name)
	if abs == "/" {
		return nil
	}
	current := "/"
	for _, part := range strings.Split(strings.TrimPrefix(abs, "/"), "/") {
		current = Resolve(current, part)
		info, _, err := o.visibleInfo(ctx, current)
		if err == nil {
			if !info.IsDir() {
				return &os.PathError{Op: "mkdir", Path: current, Err: stdfs.ErrInvalid}
			}
			continue
		}
		if !errors.Is(err, stdfs.ErrNotExist) {
			return err
		}
	}
	return o.upper.MkdirAll(ctx, abs, perm)
}

func (o *OverlayFS) Remove(ctx context.Context, name string, recursive bool) error {
	abs := o.resolve(name)
	if abs == "/" {
		return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrPermission}
	}

	info, source, err := o.visibleInfo(ctx, abs)
	if err != nil {
		return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrNotExist}
	}
	if info.IsDir() && !recursive {
		entries, err := o.ReadDir(ctx, abs)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrInvalid}
		}
	}

	if source == overlaySourceUpper {
		if err := removeMaybe(ctx, o.upper, abs, recursive); err != nil {
			return toPathError("remove", abs, err)
		}
	}
	if _, lowerExists, err := lstatMaybe(ctx, o.lower, abs); err != nil {
		return toPathError("remove", abs, err)
	} else if lowerExists {
		o.addTombstone(abs)
	}
	return nil
}

func (o *OverlayFS) Rename(ctx context.Context, oldName, newName string) error {
	oldAbs := o.resolve(oldName)
	newAbs := o.resolve(newName)
	if oldAbs == "/" || newAbs == "/" {
		return &os.PathError{Op: "rename", Path: oldAbs, Err: stdfs.ErrPermission}
	}
	if oldAbs == newAbs {
		return nil
	}
	info, _, err := o.visibleInfo(ctx, oldAbs)
	if err != nil {
		return &os.PathError{Op: "rename", Path: oldAbs, Err: stdfs.ErrNotExist}
	}
	if _, _, err := o.visibleInfo(ctx, newAbs); err == nil {
		return &os.PathError{Op: "rename", Path: newAbs, Err: stdfs.ErrExist}
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return toPathError("rename", newAbs, err)
	}
	if info.IsDir() && isDescendantPath(newAbs, oldAbs) {
		return &os.PathError{Op: "rename", Path: newAbs, Err: stdfs.ErrInvalid}
	}

	if err := clonePath(ctx, o, oldAbs, o.upper, newAbs); err != nil {
		return toPathError("rename", oldAbs, err)
	}
	if err := o.Remove(ctx, oldAbs, true); err != nil {
		return err
	}
	return nil
}

func (o *OverlayFS) Getwd() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.cwd
}

func (o *OverlayFS) Chdir(name string) error {
	abs := o.resolve(name)
	info, _, err := o.visibleInfo(context.Background(), abs)
	if err != nil {
		return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrNotExist}
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrInvalid}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cwd = abs
	return nil
}

type overlaySource uint8

const (
	overlaySourceNone overlaySource = iota
	overlaySourceUpper
	overlaySourceLower
)

func (o *OverlayFS) visibleInfo(ctx context.Context, abs string) (stdfs.FileInfo, overlaySource, error) {
	if info, err := o.upper.Lstat(ctx, abs); err == nil {
		return info, overlaySourceUpper, nil
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return nil, overlaySourceNone, err
	}
	if o.isHidden(abs) {
		return nil, overlaySourceNone, stdfs.ErrNotExist
	}
	info, err := o.lower.Lstat(ctx, abs)
	if err != nil {
		return nil, overlaySourceNone, err
	}
	return info, overlaySourceLower, nil
}

func (o *OverlayFS) resolveRealpath(ctx context.Context, abs string, depth int) (string, error) {
	abs = Clean(abs)
	if depth > maxSymlinkDepth {
		return "", errTooManySymlinks
	}
	if abs == "/" {
		return "/", nil
	}

	current := "/"
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, part := range parts {
		next := Resolve(current, part)
		info, source, err := o.visibleInfo(ctx, next)
		if err != nil {
			return "", err
		}
		isLast := i == len(parts)-1
		if info.Mode()&stdfs.ModeSymlink != 0 {
			target, err := o.readlinkAt(ctx, source, next)
			if err != nil {
				return "", err
			}
			resolved := Resolve(parentDir(next), target)
			if !isLast {
				resolved = Resolve(resolved, path.Join(parts[i+1:]...))
			}
			return o.resolveRealpath(ctx, resolved, depth+1)
		}
		if isLast {
			return next, nil
		}
		if !info.IsDir() {
			return "", stdfs.ErrInvalid
		}
		current = next
	}

	return "/", nil
}

func (o *OverlayFS) readlinkAt(ctx context.Context, source overlaySource, abs string) (string, error) {
	switch source {
	case overlaySourceUpper:
		return o.upper.Readlink(ctx, abs)
	case overlaySourceLower:
		return o.lower.Readlink(ctx, abs)
	default:
		return "", stdfs.ErrNotExist
	}
}

func (o *OverlayFS) resolve(name string) string {
	return Resolve(o.Getwd(), name)
}

func (o *OverlayFS) isHidden(name string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for current := Clean(name); ; current = parentDir(current) {
		if _, ok := o.tombstones[current]; ok {
			return true
		}
		if current == "/" {
			return false
		}
	}
}

func (o *OverlayFS) isExactTombstone(name string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, ok := o.tombstones[Clean(name)]
	return ok
}

func (o *OverlayFS) addTombstone(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tombstones[Clean(name)] = struct{}{}
}

func (o *OverlayFS) clearTombstonesUnder(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	name = Clean(name)
	for candidate := range o.tombstones {
		if candidate == name || strings.HasPrefix(candidate, name+"/") {
			delete(o.tombstones, candidate)
		}
	}
}

func lstatMaybe(ctx context.Context, fsys FileSystem, name string) (stdfs.FileInfo, bool, error) {
	info, err := fsys.Lstat(ctx, name)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return info, true, nil
}

func removeMaybe(ctx context.Context, fsys FileSystem, name string, recursive bool) error {
	err := fsys.Remove(ctx, name, recursive)
	if err != nil && errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	return err
}

func toPathError(op, name string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr
	}
	return &os.PathError{Op: op, Path: name, Err: err}
}

func isDescendantPath(name, parent string) bool {
	parent = Clean(parent)
	name = Clean(name)
	return name != parent && strings.HasPrefix(name, parent+"/")
}

var _ FileSystem = (*OverlayFS)(nil)
