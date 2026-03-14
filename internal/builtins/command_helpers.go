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

func allowPath(_ context.Context, inv *Invocation, _ policy.FileAction, name string) (string, error) {
	if inv == nil || inv.FS == nil {
		return gbfs.Clean(name), nil
	}
	return inv.FS.Resolve(name), nil
}

func openRead(ctx context.Context, inv *Invocation, name string) (gbfs.File, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionRead, name)
	if err != nil {
		return nil, "", err
	}
	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return file, abs, nil
}

func readDir(ctx context.Context, inv *Invocation, name string) ([]stdfs.DirEntry, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionReadDir, name)
	if err != nil {
		return nil, "", err
	}
	entries, err := inv.FS.ReadDir(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return entries, abs, nil
}

func statPath(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionStat, name)
	if err != nil {
		return nil, "", err
	}
	info, err := inv.FS.Stat(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return info, abs, nil
}

func lstatPath(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionLstat, name)
	if err != nil {
		return nil, "", err
	}
	info, err := inv.FS.Lstat(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return info, abs, nil
}

func statMaybe(ctx context.Context, inv *Invocation, action policy.FileAction, name string) (info stdfs.FileInfo, abs string, exists bool, err error) {
	abs, err = allowPath(ctx, inv, action, name)
	if err != nil {
		return nil, "", false, err
	}
	info, err = inv.FS.Stat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, abs, false, nil
		}
		return nil, "", false, err
	}
	return info, abs, true, nil
}

func lstatMaybe(ctx context.Context, inv *Invocation, action policy.FileAction, name string) (info stdfs.FileInfo, abs string, exists bool, err error) {
	abs, err = allowPath(ctx, inv, action, name)
	if err != nil {
		return nil, "", false, err
	}
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
