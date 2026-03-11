//go:build !windows

package compatfs

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	jbfs "github.com/ewhauser/jbgo/fs"
)

type HostFS struct {
	mu  sync.RWMutex
	cwd string
}

func New() (*HostFS, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &HostFS{cwd: filepath.Clean(cwd)}, nil
}

func (h *HostFS) Open(_ context.Context, name string) (jbfs.File, error) {
	return h.OpenFile(context.Background(), name, os.O_RDONLY, 0)
}

func (h *HostFS) OpenFile(_ context.Context, name string, flag int, perm fs.FileMode) (jbfs.File, error) {
	return os.OpenFile(h.resolve(name), flag, perm)
}

func (h *HostFS) Stat(_ context.Context, name string) (fs.FileInfo, error) {
	return os.Stat(h.resolve(name))
}

func (h *HostFS) Lstat(_ context.Context, name string) (fs.FileInfo, error) {
	return os.Lstat(h.resolve(name))
}

func (h *HostFS) ReadDir(_ context.Context, name string) ([]fs.DirEntry, error) {
	return os.ReadDir(h.resolve(name))
}

func (h *HostFS) Readlink(_ context.Context, name string) (string, error) {
	target, err := os.Readlink(h.resolve(name))
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(target), nil
}

func (h *HostFS) Realpath(_ context.Context, name string) (string, error) {
	resolved, err := filepath.EvalSymlinks(h.resolve(name))
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(resolved), nil
}

func (h *HostFS) Symlink(_ context.Context, target, linkName string) error {
	return os.Symlink(target, h.resolve(linkName))
}

func (h *HostFS) Link(_ context.Context, oldName, newName string) error {
	return os.Link(h.resolve(oldName), h.resolve(newName))
}

func (h *HostFS) Chmod(_ context.Context, name string, mode fs.FileMode) error {
	return os.Chmod(h.resolve(name), mode)
}

func (h *HostFS) Chtimes(_ context.Context, name string, atime, mtime time.Time) error {
	return os.Chtimes(h.resolve(name), atime, mtime)
}

func (h *HostFS) MkdirAll(_ context.Context, name string, perm fs.FileMode) error {
	return os.MkdirAll(h.resolve(name), perm)
}

func (h *HostFS) Remove(_ context.Context, name string, recursive bool) error {
	resolved := h.resolve(name)
	if recursive {
		return os.RemoveAll(resolved)
	}
	return os.Remove(resolved)
}

func (h *HostFS) Rename(_ context.Context, oldName, newName string) error {
	return os.Rename(h.resolve(oldName), h.resolve(newName))
}

func (h *HostFS) Getwd() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return filepath.ToSlash(h.cwd)
}

func (h *HostFS) Chdir(name string) error {
	resolved := h.resolve(name)
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: resolved, Err: fs.ErrInvalid}
	}
	h.mu.Lock()
	h.cwd = filepath.Clean(resolved)
	h.mu.Unlock()
	return nil
}

func (h *HostFS) resolve(name string) string {
	converted := filepath.FromSlash(name)
	if filepath.IsAbs(converted) {
		return filepath.Clean(converted)
	}

	h.mu.RLock()
	cwd := h.cwd
	h.mu.RUnlock()
	if cwd == "" {
		cwd = string(os.PathSeparator)
	}
	return filepath.Clean(filepath.Join(cwd, converted))
}
