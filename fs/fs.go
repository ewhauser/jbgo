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
	Chmod(ctx context.Context, name string, mode stdfs.FileMode) error
	Chtimes(ctx context.Context, name string, atime, mtime time.Time) error
	MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error
	Remove(ctx context.Context, name string, recursive bool) error
	Rename(ctx context.Context, oldName, newName string) error
	Getwd() string
	Chdir(name string) error
}

type Factory interface {
	New(ctx context.Context) (FileSystem, error)
}

type MemoryFactory struct{}

func (MemoryFactory) New(context.Context) (FileSystem, error) {
	return NewMemory(), nil
}

type OverlayFactory struct {
	Lower Factory
}

func (f OverlayFactory) New(ctx context.Context) (FileSystem, error) {
	if f.Lower == nil {
		return NewOverlay(NewMemory()), nil
	}
	lower, err := f.Lower.New(ctx)
	if err != nil {
		return nil, err
	}
	return NewOverlay(lower), nil
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
