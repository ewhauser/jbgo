package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"maps"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func exitf(inv *Invocation, code int, format string, args ...any) error {
	return Exitf(inv, code, format, args...)
}

func commandUsageError(inv *Invocation, name, format string, args ...any) error {
	return exitf(inv, 1, "%s: %s\nTry '%s --help' for more information.", name, fmt.Sprintf(format, args...), name)
}

func exitCodeForError(err error) int {
	if policy.IsDenied(err) {
		return 126
	}
	return 1
}

func allowPath(inv *Invocation, name string) string {
	if inv == nil || inv.FS == nil {
		return gbfs.Clean(name)
	}
	return inv.FS.Resolve(name)
}

func openRead(ctx context.Context, inv *Invocation, name string) (gbfs.File, string, error) {
	abs := allowPath(inv, name)
	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return file, abs, nil
}

func readDir(ctx context.Context, inv *Invocation, name string) ([]stdfs.DirEntry, error) {
	abs := allowPath(inv, name)
	entries, err := inv.FS.ReadDir(ctx, abs)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func statPath(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, error) {
	abs := allowPath(inv, name)
	info, err := inv.FS.Stat(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return info, abs, nil
}

func lstatPath(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, error) {
	abs := allowPath(inv, name)
	info, err := inv.FS.Lstat(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return info, abs, nil
}

func statMaybe(ctx context.Context, inv *Invocation, name string) (info stdfs.FileInfo, abs string, exists bool, err error) {
	abs = allowPath(inv, name)
	info, err = inv.FS.Stat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, abs, false, nil
		}
		return nil, "", false, err
	}
	return info, abs, true, nil
}

func lstatMaybe(ctx context.Context, inv *Invocation, name string) (info stdfs.FileInfo, abs string, exists bool, err error) {
	abs = allowPath(inv, name)
	info, err = inv.FS.Lstat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, abs, false, nil
		}
		return nil, "", false, err
	}
	return info, abs, true, nil
}

func cloneEnv(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}
