package fs

import (
	"context"
	"io"
	stdfs "io/fs"
	"path"
	"strings"
	"time"
)

type File interface {
	io.ReadWriteCloser
	Stat() (stdfs.FileInfo, error)
}

// FileSystem is the project-owned filesystem contract used by the runtime.
type FileSystem interface {
	Open(ctx context.Context, name string) (File, error)
	OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error)
	Stat(ctx context.Context, name string) (stdfs.FileInfo, error)
	Lstat(ctx context.Context, name string) (stdfs.FileInfo, error)
	ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error)
	Readlink(ctx context.Context, name string) (string, error)
	Realpath(ctx context.Context, name string) (string, error)
	Symlink(ctx context.Context, target, linkName string) error
	Link(ctx context.Context, oldName, newName string) error
	Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error
	Chmod(ctx context.Context, name string, mode stdfs.FileMode) error
	Chtimes(ctx context.Context, name string, atime, mtime time.Time) error
	MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error
	Remove(ctx context.Context, name string, recursive bool) error
	Rename(ctx context.Context, oldName, newName string) error
	Getwd() string
	Chdir(name string) error
}

// Factory creates a fresh filesystem instance for a runtime session.
type Factory interface {
	New(ctx context.Context) (FileSystem, error)
}

// FactoryFunc adapts a function into a Factory.
type FactoryFunc func(ctx context.Context) (FileSystem, error)

func (f FactoryFunc) New(ctx context.Context) (FileSystem, error) {
	return f(ctx)
}

type memoryFactory struct{}

func (memoryFactory) New(context.Context) (FileSystem, error) {
	return NewMemory(), nil
}

// Memory returns a factory that creates a fresh in-memory filesystem per session.
func Memory() Factory {
	return memoryFactory{}
}

// Overlay returns a copy-on-write filesystem factory over lower.
func Overlay(lower Factory) Factory {
	return FactoryFunc(func(ctx context.Context) (FileSystem, error) {
		if lower == nil {
			return NewOverlay(NewMemory()), nil
		}
		base, err := lower.New(ctx)
		if err != nil {
			return nil, err
		}
		return NewOverlay(base), nil
	})
}

// Snapshot returns a factory that clones source into a read-only snapshot per session.
func Snapshot(source FileSystem) Factory {
	return FactoryFunc(func(ctx context.Context) (FileSystem, error) {
		return NewSnapshot(ctx, source)
	})
}

// Reusable returns a factory that materializes source once and gives each
// caller a fresh writable overlay above that shared base.
func Reusable(source Factory) Factory {
	return &reusableFactory{source: source}
}

func Clean(name string) string {
	if name == "" {
		return "/"
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	name = path.Clean(name)
	if name == "." {
		return "/"
	}
	return name
}

func Resolve(dir, name string) string {
	if name == "" {
		return Clean(dir)
	}
	if strings.HasPrefix(name, "/") {
		return Clean(name)
	}
	if dir == "" {
		dir = "/"
	}
	return Clean(path.Join(dir, name))
}

// joinClean concatenates a clean absolute directory path with a single path
// component. Both inputs must be pre-validated: dir is a clean absolute path
// (as returned by Clean or Resolve) and name is a single entry with no
// slashes. This avoids the path.Join + path.Clean overhead that Resolve pays.
func joinClean(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}
