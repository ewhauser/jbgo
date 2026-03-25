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

	"github.com/ewhauser/gbash/policy"
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
	if err := rmdirTraversalPathError(ctx, inv, raw); err != nil {
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

	info, err := inv.FS.Lstat(ctx, abs)
	if err != nil || info.Mode()&stdfs.ModeSymlink == 0 {
		return nil //nolint:nilerr // lstat error on symlink means path doesn't exist
	}

	resolvedTarget, resolveErr := canonicalizeReadlink(ctx, inv, abs, readlinkModeCanonicalizeExisting)
	switch {
	case resolveErr == nil:
		targetInfo, statErr := inv.FS.Lstat(ctx, resolvedTarget)
		if statErr != nil {
			return rmdirFailure(inv, raw, statErr)
		}
		if !targetInfo.IsDir() {
			return rmdirFailure(inv, raw, syscall.ENOTDIR)
		}
		return exitf(inv, 1, "rmdir: failed to remove '%s': Symbolic link not followed", raw)
	case errors.Is(resolveErr, syscall.ENOTDIR):
		return rmdirFailure(inv, raw, syscall.ENOTDIR)
	case errors.Is(resolveErr, stdfs.ErrNotExist):
		return exitf(inv, 1, "rmdir: failed to remove '%s': Symbolic link not followed", raw)
	default:
		return rmdirFailure(inv, raw, resolveErr)
	}
}

func rmdirTraversalPathError(ctx context.Context, inv *Invocation, raw string) error {
	cleaned := path.Clean(strings.TrimRight(raw, "/"))
	if cleaned == "." || cleaned == "/" {
		return nil
	}

	absolute, parts := splitReadlinkPath(cleaned)
	if len(parts) < 2 {
		return nil
	}

	currentAbs := inv.FS.Getwd()
	if absolute {
		currentAbs = "/"
	}

	for i := 0; i < len(parts)-1; i++ {
		currentAbs = path.Join(currentAbs, parts[i])
		info, err := inv.FS.Lstat(ctx, currentAbs)
		if err != nil {
			return nil //nolint:nilerr // ancestor lookup failures should defer to the final rmdir path error
		}
		if info.Mode()&stdfs.ModeSymlink != 0 {
			targetIsDir, checkErr := rmdirPathIsDirectory(ctx, inv, currentAbs)
			if checkErr != nil {
				return rmdirFailure(inv, rmdirDisplayPath(absolute, parts[:i+1]), checkErr)
			}
			if targetIsDir {
				continue
			}
			return rmdirFailure(inv, rmdirDisplayPath(absolute, parts[:i+1]), syscall.ENOTDIR)
		}
		if !info.IsDir() {
			return rmdirFailure(inv, rmdirDisplayPath(absolute, parts[:i+1]), syscall.ENOTDIR)
		}
	}
	return nil
}

func rmdirDisplayPath(absolute bool, parts []string) string {
	if len(parts) == 0 {
		if absolute {
			return "/"
		}
		return "."
	}
	if absolute {
		return "/" + path.Join(parts...)
	}
	return path.Join(parts...)
}

func rmdirPathIsDirectory(ctx context.Context, inv *Invocation, name string) (bool, error) {
	if inv == nil || inv.FS == nil {
		return false, nil
	}
	info, err := inv.FS.StatQuiet(ctx, name)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || policy.IsDenied(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

func rmdirDirNotEmpty(ctx context.Context, inv *Invocation, abs string, err error) bool {
	switch {
	case errors.Is(err, stdfs.ErrInvalid), errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		return true
	case errors.Is(err, stdfs.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EBUSY), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EROFS):
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
