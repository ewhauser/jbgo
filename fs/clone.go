package fs

import (
	"context"
	"io"
	stdfs "io/fs"
	"os"
	"path"
)

func clonePath(ctx context.Context, src FileSystem, srcName string, dst *MemoryFS, dstName string) error {
	info, err := src.Lstat(ctx, srcName)
	if err != nil {
		return err
	}
	absDst := Clean(dstName)
	if info.Mode()&stdfs.ModeSymlink != 0 {
		target, err := src.Readlink(ctx, srcName)
		if err != nil {
			return err
		}
		return dst.Symlink(ctx, target, absDst)
	}
	if info.IsDir() {
		if err := dst.MkdirAll(ctx, absDst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := src.ReadDir(ctx, srcName)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			childSrc := path.Join(Clean(srcName), entry.Name())
			childDst := path.Join(absDst, entry.Name())
			if err := clonePath(ctx, src, childSrc, dst, childDst); err != nil {
				return err
			}
		}
		return nil
	}
	return cloneFile(ctx, src, srcName, dst, absDst, info.Mode().Perm())
}

func cloneFile(ctx context.Context, src FileSystem, srcName string, dst *MemoryFS, dstName string, perm stdfs.FileMode) error {
	reader, err := src.Open(ctx, srcName)
	if err != nil {
		return err
	}
	defer reader.Close()

	writer, err := dst.OpenFile(ctx, dstName, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer writer.Close()

	_, err = io.Copy(writer, reader)
	return err
}
