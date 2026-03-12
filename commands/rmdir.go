package commands

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
	args := inv.Args
	opts, args, err := parseRmdirArgs(inv, args)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return exitf(inv, 1, "rmdir: missing operand")
	}

	for _, dir := range args {
		abs, err := allowPath(ctx, inv, policy.FileActionRemove, dir)
		if err != nil {
			return err
		}
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

func parseRmdirArgs(inv *Invocation, args []string) (rmdirOptions, []string, error) {
	opts := rmdirOptions{}
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			return opts, args[1:], nil
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			return opts, args, nil
		}
		if strings.HasPrefix(arg, "--") {
			name, ok := inferRmdirLongOption(arg)
			if !ok {
				return rmdirOptions{}, nil, exitf(inv, 1, "rmdir: unsupported flag %s", arg)
			}
			switch name {
			case "ignore-fail-on-non-empty":
				opts.ignoreFailOnNonEmpty = true
			case "parents":
				opts.parents = true
			case "verbose":
				opts.verbose = true
			}
			args = args[1:]
			continue
		}
		if !applyRmdirShortFlags(arg, &opts) {
			return rmdirOptions{}, nil, exitf(inv, 1, "rmdir: unsupported flag %s", arg)
		}
		args = args[1:]
	}
	return opts, args, nil
}

func inferRmdirLongOption(arg string) (string, bool) {
	if strings.Contains(arg, "=") {
		return "", false
	}
	name := strings.TrimPrefix(arg, "--")
	options := []string{"ignore-fail-on-non-empty", "parents", "verbose"}
	match := ""
	for _, option := range options {
		if strings.HasPrefix(option, name) {
			if match != "" {
				return "", false
			}
			match = option
		}
	}
	return match, match != ""
}

func applyRmdirShortFlags(arg string, opts *rmdirOptions) bool {
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return false
	}
	for _, flag := range arg[1:] {
		switch flag {
		case 'p':
			opts.parents = true
		case 'v':
			opts.verbose = true
		default:
			return false
		}
	}
	return true
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
		return nil
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
