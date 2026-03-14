//go:build windows

package fs

import (
	"context"
	stdfs "io/fs"
	"time"
)

// ReadWriteFS is unavailable on Windows.
type ReadWriteFS struct{}

// NewReadWrite returns an unsupported error on Windows.
func NewReadWrite(ReadWriteOptions) (*ReadWriteFS, error) {
	return nil, unsupportedHostError()
}

func (ReadWriteFS) Open(context.Context, string) (File, error) { return nil, unsupportedHostError() }

func (ReadWriteFS) OpenFile(context.Context, string, int, stdfs.FileMode) (File, error) {
	return nil, unsupportedHostError()
}

func (ReadWriteFS) Stat(context.Context, string) (stdfs.FileInfo, error) {
	return nil, unsupportedHostError()
}

func (ReadWriteFS) Lstat(context.Context, string) (stdfs.FileInfo, error) {
	return nil, unsupportedHostError()
}

func (ReadWriteFS) ReadDir(context.Context, string) ([]stdfs.DirEntry, error) {
	return nil, unsupportedHostError()
}

func (ReadWriteFS) Readlink(context.Context, string) (string, error) {
	return "", unsupportedHostError()
}

func (ReadWriteFS) Realpath(context.Context, string) (string, error) {
	return "", unsupportedHostError()
}

func (ReadWriteFS) Symlink(context.Context, string, string) error { return unsupportedHostError() }

func (ReadWriteFS) Link(context.Context, string, string) error { return unsupportedHostError() }

func (ReadWriteFS) Chown(context.Context, string, uint32, uint32, bool) error {
	return unsupportedHostError()
}

func (ReadWriteFS) Chmod(context.Context, string, stdfs.FileMode) error {
	return unsupportedHostError()
}

func (ReadWriteFS) Chtimes(context.Context, string, time.Time, time.Time) error {
	return unsupportedHostError()
}

func (ReadWriteFS) MkdirAll(context.Context, string, stdfs.FileMode) error {
	return unsupportedHostError()
}

func (ReadWriteFS) Remove(context.Context, string, bool) error { return unsupportedHostError() }

func (ReadWriteFS) Rename(context.Context, string, string) error { return unsupportedHostError() }

func (ReadWriteFS) Getwd() string { return "/" }

func (ReadWriteFS) Chdir(string) error { return unsupportedHostError() }

var _ FileSystem = (*ReadWriteFS)(nil)
