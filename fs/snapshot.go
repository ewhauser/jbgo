package fs

import (
	"context"
	stdfs "io/fs"
	"os"
	"time"
)

type SnapshotFS struct {
	base *MemoryFS
}

func NewSnapshot(ctx context.Context, source FileSystem) (*SnapshotFS, error) {
	if source == nil {
		source = NewMemory()
	}
	base := NewMemory()
	if err := clonePath(ctx, source, "/", base, "/"); err != nil {
		return nil, err
	}
	return &SnapshotFS{base: base}, nil
}

func (s *SnapshotFS) Open(ctx context.Context, name string) (File, error) {
	return s.base.Open(ctx, name)
}

func (s *SnapshotFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	if hasWriteIntent(flag) {
		return nil, snapshotWriteError("open", Resolve(s.base.Getwd(), name))
	}
	return s.base.OpenFile(ctx, name, flag, perm)
}

func (s *SnapshotFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	return s.base.Stat(ctx, name)
}

func (s *SnapshotFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	return s.base.Lstat(ctx, name)
}

func (s *SnapshotFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	return s.base.ReadDir(ctx, name)
}

func (s *SnapshotFS) Readlink(ctx context.Context, name string) (string, error) {
	return s.base.Readlink(ctx, name)
}

func (s *SnapshotFS) Realpath(ctx context.Context, name string) (string, error) {
	return s.base.Realpath(ctx, name)
}

func (s *SnapshotFS) Symlink(_ context.Context, _, linkName string) error {
	return snapshotWriteError("symlink", Resolve(s.base.Getwd(), linkName))
}

func (s *SnapshotFS) Link(_ context.Context, oldName, _ string) error {
	return snapshotWriteError("link", Resolve(s.base.Getwd(), oldName))
}

func (s *SnapshotFS) Chmod(_ context.Context, name string, _ stdfs.FileMode) error {
	return snapshotWriteError("chmod", Resolve(s.base.Getwd(), name))
}

func (s *SnapshotFS) Chtimes(_ context.Context, name string, _, _ time.Time) error {
	return snapshotWriteError("chtimes", Resolve(s.base.Getwd(), name))
}

func (s *SnapshotFS) MkdirAll(_ context.Context, name string, _ stdfs.FileMode) error {
	return snapshotWriteError("mkdir", Resolve(s.base.Getwd(), name))
}

func (s *SnapshotFS) Remove(_ context.Context, name string, _ bool) error {
	return snapshotWriteError("remove", Resolve(s.base.Getwd(), name))
}

func (s *SnapshotFS) Rename(_ context.Context, oldName, _ string) error {
	return snapshotWriteError("rename", Resolve(s.base.Getwd(), oldName))
}

func (s *SnapshotFS) Getwd() string {
	return s.base.Getwd()
}

func (s *SnapshotFS) Chdir(name string) error {
	return s.base.Chdir(name)
}

func snapshotWriteError(op, name string) error {
	return &os.PathError{Op: op, Path: Clean(name), Err: stdfs.ErrPermission}
}

var _ FileSystem = (*SnapshotFS)(nil)
