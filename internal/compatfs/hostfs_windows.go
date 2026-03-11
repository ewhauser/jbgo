//go:build windows

package compatfs

import (
	"context"
	"errors"
	stdfs "io/fs"
	"time"

	jbfs "github.com/ewhauser/jbgo/fs"
)

var errUnsupported = errors.New("host compatibility filesystem is unsupported on Windows")

type HostFS struct{}

func New() (*HostFS, error) {
	return nil, errUnsupported
}

func (HostFS) Open(context.Context, string) (jbfs.File, error) { return nil, unsupportedError() }

func (HostFS) OpenFile(context.Context, string, int, stdfs.FileMode) (jbfs.File, error) {
	return nil, unsupportedError()
}

func (HostFS) Stat(context.Context, string) (stdfs.FileInfo, error) { return nil, unsupportedError() }

func (HostFS) Lstat(context.Context, string) (stdfs.FileInfo, error) { return nil, unsupportedError() }

func (HostFS) ReadDir(context.Context, string) ([]stdfs.DirEntry, error) {
	return nil, unsupportedError()
}

func (HostFS) Readlink(context.Context, string) (string, error) { return "", unsupportedError() }

func (HostFS) Realpath(context.Context, string) (string, error) { return "", unsupportedError() }

func (HostFS) Symlink(context.Context, string, string) error { return unsupportedError() }

func (HostFS) Link(context.Context, string, string) error { return unsupportedError() }

func (HostFS) Chmod(context.Context, string, stdfs.FileMode) error { return unsupportedError() }

func (HostFS) Chtimes(context.Context, string, time.Time, time.Time) error { return unsupportedError() }

func (HostFS) MkdirAll(context.Context, string, stdfs.FileMode) error { return unsupportedError() }

func (HostFS) Remove(context.Context, string, bool) error { return unsupportedError() }

func (HostFS) Rename(context.Context, string, string) error { return unsupportedError() }

func (HostFS) Getwd() string { return "/" }

func (HostFS) Chdir(string) error { return unsupportedError() }

func unsupportedError() error {
	return &stdfs.PathError{Op: "compatfs", Path: "/", Err: errUnsupported}
}

var _ jbfs.FileSystem = (*HostFS)(nil)
