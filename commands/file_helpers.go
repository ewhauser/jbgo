package commands

import (
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

func ensureParentDirExists(ctx context.Context, inv *Invocation, targetAbs string) error {
	parent := path.Dir(targetAbs)
	info, _, exists, err := statMaybe(ctx, inv, policy.FileActionStat, parent)
	if err != nil {
		return err
	}
	if !exists {
		return &ExitError{
			Code: 1,
			Err:  fmt.Errorf("%s: No such file or directory", parent),
		}
	}
	if !info.IsDir() {
		return &ExitError{
			Code: 1,
			Err:  fmt.Errorf("%s: Not a directory", parent),
		}
	}
	return nil
}

func copyFileContents(ctx context.Context, inv *Invocation, srcAbs, dstAbs string, perm stdfs.FileMode) error {
	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionWrite, dstAbs); err != nil {
		return err
	}

	srcFile, err := inv.FS.Open(ctx, srcAbs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer srcFile.Close()

	dstFile, err := inv.FS.OpenFile(ctx, dstAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.Trace, "copy", dstAbs, srcAbs, dstAbs)
	return nil
}

func copyTree(ctx context.Context, inv *Invocation, srcAbs, dstAbs string) error {
	srcInfo, _, err := statPath(ctx, inv, srcAbs)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return copyFileContents(ctx, inv, srcAbs, dstAbs, srcInfo.Mode().Perm())
	}

	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionMkdir, dstAbs); err != nil {
		return err
	}
	if err := inv.FS.MkdirAll(ctx, dstAbs, srcInfo.Mode().Perm()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.Trace, "mkdir", dstAbs, "", "")

	entries, _, err := readDir(ctx, inv, srcAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childSrc := path.Join(srcAbs, entry.Name())
		childDst := path.Join(dstAbs, entry.Name())
		childInfo, _, err := statPath(ctx, inv, childSrc)
		if err != nil {
			return err
		}
		if childInfo.IsDir() {
			if err := copyTree(ctx, inv, childSrc, childDst); err != nil {
				return err
			}
			continue
		}
		if err := copyFileContents(ctx, inv, childSrc, childDst, childInfo.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func writeFileContents(ctx context.Context, inv *Invocation, targetAbs string, data []byte, perm stdfs.FileMode) error {
	if err := ensureParentDirExists(ctx, inv, targetAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionWrite, targetAbs); err != nil {
		return err
	}

	file, err := inv.FS.OpenFile(ctx, targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.Trace, "write", targetAbs, targetAbs, targetAbs)
	return nil
}

func resolveDestination(ctx context.Context, inv *Invocation, sourceAbs, destArg string, multipleSources bool) (destAbs string, destInfo stdfs.FileInfo, destExists bool, err error) {
	destInfo, destAbs, destExists, err = statMaybe(ctx, inv, policy.FileActionStat, destArg)
	if err != nil {
		return "", nil, false, err
	}
	if multipleSources {
		if !destExists || !destInfo.IsDir() {
			return "", nil, false, exitf(inv, 1, "target %q is not a directory", destArg)
		}
		return path.Join(destAbs, path.Base(sourceAbs)), destInfo, true, nil
	}
	if destExists && destInfo.IsDir() {
		return path.Join(destAbs, path.Base(sourceAbs)), destInfo, true, nil
	}
	if strings.HasSuffix(destArg, "/") {
		return "", nil, false, exitf(inv, 1, "target %q is not a directory", destArg)
	}
	return destAbs, destInfo, destExists, nil
}
