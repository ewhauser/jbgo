package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type MV struct{}

func NewMV() *MV {
	return &MV{}
}

func (c *MV) Name() string {
	return "mv"
}

func (c *MV) Run(ctx context.Context, inv *Invocation) error {
	opts, args, err := parseMVArgs(inv)
	if err != nil {
		return err
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

		destAbs, _, _, err := resolveDestination(ctx, inv, srcAbs, destArg, multipleSources)
		if err != nil {
			return err
		}
		destInfo, _, destExists, err := statMaybe(ctx, inv, policy.FileActionStat, destAbs)
		if err != nil {
			return err
		}
		if opts.noClobber && destExists {
			continue
		}
		if srcInfo.IsDir() && (destAbs == srcAbs || strings.HasPrefix(destAbs, srcAbs+"/")) {
			return exitf(inv, 1, "mv: cannot move %q into itself", source)
		}
		if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
			return err
		}
		if destExists {
			if err := inv.FS.Remove(ctx, destAbs, destInfo != nil && destInfo.IsDir()); err != nil && !isNotExist(err) {
				return &ExitError{Code: 1, Err: err}
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
				continue
			}
			return &ExitError{Code: 1, Err: err}
		}
		if opts.verbose {
			if _, err := fmt.Fprintf(inv.Stdout, "renamed '%s' -> '%s'\n", source, destAbs); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	return nil
}

type mvOptions struct {
	force     bool
	noClobber bool
	verbose   bool
}

func parseMVArgs(inv *Invocation) (mvOptions, []string, error) {
	args := inv.Args
	var opts mvOptions
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		arg := args[0]
		if arg == "--" {
			return opts, args[1:], nil
		}
		switch arg {
		case "-f", "--force":
			opts.force = true
		case "-n", "--no-clobber":
			opts.noClobber = true
			opts.force = false
		case "-v", "--verbose":
			opts.verbose = true
		default:
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				for _, flag := range arg[1:] {
					switch flag {
					case 'f':
						opts.force = true
					case 'n':
						opts.noClobber = true
						opts.force = false
					case 'v':
						opts.verbose = true
					default:
						return mvOptions{}, nil, exitf(inv, 1, "mv: unsupported flag -%c", flag)
					}
				}
				args = args[1:]
				continue
			}
			return mvOptions{}, nil, exitf(inv, 1, "mv: unsupported flag %s", arg)
		}
		args = args[1:]
	}
	return opts, args, nil
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
