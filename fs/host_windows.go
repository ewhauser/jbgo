//go:build windows

package fs

import (
	"context"
	"errors"
	stdfs "io/fs"
	"time"
)

var errHostUnsupported = errors.New("host-backed filesystem is unsupported on Windows")

// HostFS is unavailable on Windows.
type HostFS struct{}

// NewHost returns an unsupported error on Windows.
func NewHost(HostOptions) (*HostFS, error) {
	return nil, unsupportedHostError()
}

func (HostFS) Open(context.Context, string) (File, error) { return nil, unsupportedHostError() }

func (HostFS) OpenFile(context.Context, string, int, stdfs.FileMode) (File, error) {
	return nil, unsupportedHostError()
}

func (HostFS) Stat(context.Context, string) (stdfs.FileInfo, error) {
	return nil, unsupportedHostError()
}

func (HostFS) Lstat(context.Context, string) (stdfs.FileInfo, error) {
	return nil, unsupportedHostError()
}

func (HostFS) ReadDir(context.Context, string) ([]stdfs.DirEntry, error) {
	return nil, unsupportedHostError()
}

func (HostFS) Readlink(context.Context, string) (string, error) { return "", unsupportedHostError() }

func (HostFS) Realpath(context.Context, string) (string, error) { return "", unsupportedHostError() }

func (HostFS) Symlink(context.Context, string, string) error { return unsupportedHostError() }

func (HostFS) Link(context.Context, string, string) error { return unsupportedHostError() }

func (HostFS) Chmod(context.Context, string, stdfs.FileMode) error { return unsupportedHostError() }

func (HostFS) Chtimes(context.Context, string, time.Time, time.Time) error {
	return unsupportedHostError()
}

func (HostFS) MkdirAll(context.Context, string, stdfs.FileMode) error { return unsupportedHostError() }

func (HostFS) Remove(context.Context, string, bool) error { return unsupportedHostError() }

func (HostFS) Rename(context.Context, string, string) error { return unsupportedHostError() }

func (HostFS) Getwd() string { return "/" }

func (HostFS) Chdir(string) error { return unsupportedHostError() }

func unsupportedHostError() error {
	return &stdfs.PathError{Op: "hostfs", Path: "/", Err: errHostUnsupported}
}

var _ FileSystem = (*HostFS)(nil)
