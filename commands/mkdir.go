package commands

import (
	"context"
	"errors"
	stdfs "io/fs"
	"path"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type Mkdir struct{}

type mkdirOptions struct {
	parents bool
	mode    stdfs.FileMode
	modeSet bool
}

func NewMkdir() *Mkdir {
	return &Mkdir{}
}

func (c *Mkdir) Name() string {
	return "mkdir"
}

func (c *Mkdir) Run(ctx context.Context, inv *Invocation) error {
	opts, args, err := parseMkdirArgs(inv)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return exitf(inv, 1, "mkdir: missing operand")
	}

	createMode := stdfs.FileMode(0o755)
	if opts.modeSet {
		createMode = opts.mode
	}

	for _, name := range args {
		abs, err := allowPath(ctx, inv, policy.FileActionMkdir, name)
		if err != nil {
			return err
		}

		created, err := mkdirPath(ctx, inv, abs, createMode, opts.parents)
		if err != nil {
			return err
		}
		if opts.modeSet && created {
			if err := inv.FS.Chmod(ctx, abs, opts.mode); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	return nil
}

func parseMkdirArgs(inv *Invocation) (mkdirOptions, []string, error) {
	opts := mkdirOptions{}
	args := append([]string(nil), inv.Args...)
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			return opts, args[1:], nil
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch {
		case arg == "-p" || arg == "--parents":
			opts.parents = true
		case arg == "-m" || arg == "--mode":
			if len(args) < 2 {
				return mkdirOptions{}, nil, exitf(inv, 1, "mkdir: option requires an argument -- 'm'")
			}
			mode, err := parseMkdirMode(args[1])
			if err != nil {
				return mkdirOptions{}, nil, exitf(inv, 1, "mkdir: invalid mode %q", args[1])
			}
			opts.mode = mode
			opts.modeSet = true
			args = args[1:]
		case strings.HasPrefix(arg, "--mode="):
			mode, err := parseMkdirMode(strings.TrimPrefix(arg, "--mode="))
			if err != nil {
				return mkdirOptions{}, nil, exitf(inv, 1, "mkdir: invalid mode %q", strings.TrimPrefix(arg, "--mode="))
			}
			opts.mode = mode
			opts.modeSet = true
		default:
			remaining, err := parseMkdirShortOptions(inv, &opts, arg)
			if err != nil {
				return mkdirOptions{}, nil, err
			}
			if remaining != "" {
				mode, err := parseMkdirMode(remaining)
				if err != nil {
					return mkdirOptions{}, nil, exitf(inv, 1, "mkdir: invalid mode %q", remaining)
				}
				opts.mode = mode
				opts.modeSet = true
			}
		}
		args = args[1:]
	}
	return opts, args, nil
}

func parseMkdirShortOptions(inv *Invocation, opts *mkdirOptions, arg string) (string, error) {
	short := strings.TrimPrefix(arg, "-")
	for i := 0; i < len(short); i++ {
		switch short[i] {
		case 'p':
			opts.parents = true
		case 'm':
			return short[i+1:], nil
		default:
			return "", exitf(inv, 1, "mkdir: unsupported flag -%c", short[i])
		}
	}
	return "", nil
}

func parseMkdirMode(spec string) (stdfs.FileMode, error) {
	mode, err := computeChmodMode(stdfs.ModeDir|0o777, spec)
	if err != nil {
		return 0, err
	}
	return mode & (stdfs.ModePerm | stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky), nil
}

func mkdirPath(ctx context.Context, inv *Invocation, abs string, perm stdfs.FileMode, parents bool) (bool, error) {
	if info, err := inv.FS.Lstat(ctx, abs); err == nil {
		if parents && info.IsDir() {
			return false, nil
		}
		return false, exitf(inv, 1, "mkdir: cannot create directory %q: File exists", abs)
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return false, &ExitError{Code: 1, Err: err}
	}

	if !parents {
		parent := path.Dir(abs)
		info, err := inv.FS.Stat(ctx, parent)
		if err != nil {
			if errors.Is(err, stdfs.ErrNotExist) {
				return false, exitf(inv, 1, "mkdir: cannot create directory %q: No such file or directory", abs)
			}
			return false, &ExitError{Code: 1, Err: err}
		}
		if !info.IsDir() {
			return false, exitf(inv, 1, "mkdir: cannot create directory %q: Not a directory", abs)
		}
	}

	if err := inv.FS.MkdirAll(ctx, abs, perm); err != nil {
		return false, &ExitError{Code: 1, Err: err}
	}
	return true, nil
}

var _ Command = (*Mkdir)(nil)
