package policy

import (
	"context"
	"errors"
	stdfs "io/fs"
	"path"

	jbfs "github.com/cadencerpm/just-bash-go/fs"
)

func CheckPath(ctx context.Context, pol Policy, fsys jbfs.FileSystem, action FileAction, target string) error {
	target = cleanAbs(target)
	if pol == nil {
		return nil
	}
	if err := pol.AllowPath(ctx, action, target); err != nil {
		return err
	}
	if fsys == nil {
		return nil
	}

	resolved, traversed, err := resolveTarget(ctx, fsys, action, target)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) || errors.Is(err, stdfs.ErrInvalid) {
			return nil
		}
		return err
	}
	if traversed && pol.SymlinkMode() == SymlinkDeny {
		return &DeniedError{
			Subject: string(action) + " " + `"` + target + `"`,
			Reason:  "symlink traversal denied",
		}
	}
	if resolved != target {
		if err := pol.AllowPath(ctx, action, resolved); err != nil {
			return err
		}
	}
	return nil
}

func resolveTarget(ctx context.Context, fsys jbfs.FileSystem, action FileAction, target string) (resolved string, traversed bool, err error) {
	switch action {
	case FileActionRead, FileActionStat, FileActionReadDir:
		resolved, err := fsys.Realpath(ctx, target)
		return cleanAbs(resolved), cleanAbs(resolved) != cleanAbs(target), err
	case FileActionLstat, FileActionReadlink:
		parent := cleanAbs(path.Dir(target))
		parentResolved, err := fsys.Realpath(ctx, parent)
		if err != nil {
			if errors.Is(err, stdfs.ErrNotExist) && parent == "/" {
				return cleanAbs(target), false, nil
			}
			return "", false, err
		}
		resolved = cleanAbs(path.Join(parentResolved, path.Base(target)))
		return resolved, resolved != cleanAbs(target), nil
	case FileActionWrite, FileActionMkdir:
		resolved, err := fsys.Realpath(ctx, target)
		if err == nil {
			resolved = cleanAbs(resolved)
			return resolved, resolved != cleanAbs(target), nil
		}
		if !errors.Is(err, stdfs.ErrNotExist) {
			return "", false, err
		}

		parent := cleanAbs(path.Dir(target))
		parentResolved, err := fsys.Realpath(ctx, parent)
		if err != nil {
			return "", false, err
		}
		resolved = cleanAbs(path.Join(parentResolved, path.Base(target)))
		return resolved, resolved != cleanAbs(target), nil
	default:
		return target, false, nil
	}
}
