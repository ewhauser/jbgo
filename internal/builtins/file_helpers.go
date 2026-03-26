package builtins

import (
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
)

func ensureParentDirExists(ctx context.Context, inv *Invocation, targetAbs string) error {
	parent := path.Dir(targetAbs)
	info, _, exists, err := statMaybe(ctx, inv, parent)
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

	srcFile, err := inv.FS.Open(ctx, srcAbs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := inv.FS.OpenFile(ctx, dstAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer func() { _ = dstFile.Close() }()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.TraceRecorder(), "copy", dstAbs, srcAbs, dstAbs)
	return nil
}

func writeFileContents(ctx context.Context, inv *Invocation, targetAbs string, data []byte, perm stdfs.FileMode) error {
	if err := ensureParentDirExists(ctx, inv, targetAbs); err != nil {
		return err
	}

	file, err := inv.FS.OpenFile(ctx, targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(data); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.TraceRecorder(), "write", targetAbs, targetAbs, targetAbs)
	return nil
}
