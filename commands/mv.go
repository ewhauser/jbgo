package commands

import (
	"context"
	stdfs "io/fs"
	"path"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

type MV struct{}

func NewMV() *MV {
	return &MV{}
}

func (c *MV) Name() string {
	return "mv"
}

func (c *MV) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return exitf(inv, 1, "mv: unsupported flag %s", args[0])
	}

	if len(args) < 2 {
		return exitf(inv, 1, "mv: missing destination file operand")
	}

	sources := args[:len(args)-1]
	destArg := args[len(args)-1]
	multipleSources := len(sources) > 1

	for _, source := range sources {
		srcInfo, srcAbs, err := statPath(ctx, inv, source)
		if err != nil {
			return exitf(inv, 1, "mv: cannot stat %q: No such file or directory", source)
		}

		destAbs, destInfo, destExists, err := resolveDestination(ctx, inv, srcAbs, destArg, multipleSources)
		if err != nil {
			return err
		}
		if srcInfo.IsDir() && (destAbs == srcAbs || strings.HasPrefix(destAbs, srcAbs+"/")) {
			return exitf(inv, 1, "mv: cannot move %q into itself", source)
		}
		if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
			return err
		}
		if _, err := allowPath(ctx, inv, policy.FileActionRename, srcAbs); err != nil {
			return err
		}
		if _, err := allowPath(ctx, inv, policy.FileActionRename, destAbs); err != nil {
			return err
		}

		if destExists {
			if destInfo != nil && destInfo.IsDir() && !srcInfo.IsDir() {
				destAbs = path.Join(destAbs, path.Base(srcAbs))
			} else {
				if err := inv.FS.Remove(ctx, destAbs, destInfo != nil && destInfo.IsDir()); err != nil && !isNotExist(err) {
					return &ExitError{Code: 1, Err: err}
				}
			}
		}

		if err := inv.FS.Rename(ctx, srcAbs, destAbs); err != nil {
			if isExists(err) {
				if err := inv.FS.Remove(ctx, destAbs, isDirInfo(destInfo)); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				if err := inv.FS.Rename(ctx, srcAbs, destAbs); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				recordFileMutation(inv.Trace, "rename", destAbs, srcAbs, destAbs)
				continue
			}
			return &ExitError{Code: 1, Err: err}
		}
		recordFileMutation(inv.Trace, "rename", destAbs, srcAbs, destAbs)
	}

	return nil
}

func isNotExist(err error) bool {
	return err != nil && strings.Contains(err.Error(), stdfs.ErrNotExist.Error())
}

func isExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), stdfs.ErrExist.Error())
}

func isDirInfo(info stdfs.FileInfo) bool {
	return info != nil && info.IsDir()
}

var _ Command = (*MV)(nil)
