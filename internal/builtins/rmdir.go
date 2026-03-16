package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"syscall"
)

type Rmdir struct{}

func NewRmdir() *Rmdir {
	return &Rmdir{}
}

func (c *Rmdir) Name() string {
	return "rmdir"
}

func (c *Rmdir) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Rmdir) Spec() CommandSpec {
	return CommandSpec{
		Name:  "rmdir",
		About: "Remove empty directories",
		Usage: "rmdir [OPTION]... DIRECTORY...",
		Options: []OptionSpec{
			{Name: "ignore-fail-on-non-empty", Long: "ignore-fail-on-non-empty", Help: "ignore each failure that is solely because a directory is non-empty"},
			{Name: "parents", Short: 'p', Long: "parents", Help: "remove DIRECTORY and its ancestors"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "output a diagnostic for every directory processed"},
		},
		Args: []ArgSpec{
			{Name: "dir", ValueName: "DIRECTORY", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			StopAtFirstPositional: true,
		},
	}
}

func (c *Rmdir) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts := rmdirOptions{
		ignoreFailOnNonEmpty: matches.Has("ignore-fail-on-non-empty"),
		parents:              matches.Has("parents"),
		verbose:              matches.Has("verbose"),
	}
	args := matches.Args("dir")

	for _, dir := range args {
		abs := allowPath(inv, dir)
		if err := removeEmptyDir(ctx, inv, dir, abs, opts); err != nil {
			return err
		}
		if opts.parents {
			for rawParent, absParent := path.Dir(strings.TrimRight(dir, "/")), path.Dir(abs); rawParent != "/" && rawParent != "." && rawParent != ""; rawParent, absParent = path.Dir(rawParent), path.Dir(absParent) {
				if err := removeEmptyDir(ctx, inv, rawParent, absParent, opts); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type rmdirOptions struct {
	ignoreFailOnNonEmpty bool
	parents              bool
	verbose              bool
}

func removeEmptyDir(ctx context.Context, inv *Invocation, raw, abs string, opts rmdirOptions) error {
	if opts.verbose {
		if _, err := fmt.Fprintf(inv.Stdout, "rmdir: removing directory, '%s'\n", raw); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := rmdirSymlinkSlashError(ctx, inv, raw, abs); err != nil {
		return err
	}
	info, err := inv.FS.Lstat(ctx, abs)
	if err != nil {
		return rmdirFailure(inv, raw, err)
	}
	if !info.IsDir() {
		return rmdirFailure(inv, raw, syscall.ENOTDIR)
	}
	if err := inv.FS.Remove(ctx, abs, false); err != nil {
		if opts.ignoreFailOnNonEmpty && rmdirDirNotEmpty(ctx, inv, abs, err) {
			return nil
		}
		return rmdirFailure(inv, raw, err)
	}
	return nil
}

func rmdirSymlinkSlashError(ctx context.Context, inv *Invocation, raw, abs string) error {
	if !strings.HasSuffix(raw, "/") {
		return nil
	}

	trimmedRaw := strings.TrimRight(raw, "/")
	if trimmedRaw == "" {
		trimmedRaw = raw
	}

	info, err := inv.FS.Lstat(ctx, abs)
	if err != nil || info.Mode()&stdfs.ModeSymlink == 0 {
		return nil //nolint:nilerr // lstat error on symlink means path doesn't exist
	}

	targetInfo, statErr := inv.FS.Stat(ctx, abs)
	if statErr == nil && !targetInfo.IsDir() {
		return rmdirFailure(inv, raw, syscall.ENOTDIR)
	}
	if statErr == nil || errors.Is(statErr, stdfs.ErrNotExist) {
		return exitf(inv, 1, "rmdir: failed to remove '%s': Symbolic link not followed", raw)
	}
	return rmdirFailure(inv, trimmedRaw, statErr)
}

func rmdirDirNotEmpty(ctx context.Context, inv *Invocation, abs string, err error) bool {
	switch {
	case errors.Is(err, stdfs.ErrInvalid), errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		return true
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EBUSY), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EROFS):
		entries, readErr := inv.FS.ReadDir(ctx, abs)
		return readErr == nil && len(entries) > 0
	default:
		return false
	}
}

func rmdirFailure(inv *Invocation, raw string, err error) error {
	return exitf(inv, 1, "rmdir: failed to remove '%s': %s", raw, rmdirErrorText(err))
}

func rmdirErrorText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return rmdirErrorText(pathErr.Err)
	}

	switch {
	case errors.Is(err, stdfs.ErrInvalid), errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		return "Directory not empty"
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.EISDIR):
		return "Not a directory"
	default:
		return err.Error()
	}
}

var _ Command = (*Rmdir)(nil)
