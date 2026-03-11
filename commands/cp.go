package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/ewhauser/jbgo/policy"
)

type CP struct{}

func NewCP() *CP {
	return &CP{}
}

func (c *CP) Name() string {
	return "cp"
}

func (c *CP) Run(ctx context.Context, inv *Invocation) error {
	opts, args, err := parseCPArgs(inv)
	if err != nil {
		return err
	}

	if len(args) < 2 {
		return exitf(inv, 1, "cp: missing destination file operand")
	}

	sources := args[:len(args)-1]
	destArg := args[len(args)-1]
	multipleSources := len(sources) > 1

	for _, source := range sources {
		srcInfo, srcAbs, err := statPath(ctx, inv, source)
		if err != nil {
			return exitf(inv, 1, "cp: cannot stat %q: No such file or directory", source)
		}

		destAbs, _, _, err := resolveDestination(ctx, inv, srcAbs, destArg, multipleSources)
		if err != nil {
			return err
		}
		_, _, destExists, err := statMaybe(ctx, inv, policy.FileActionStat, destAbs)
		if err != nil {
			return err
		}
		if opts.noClobber && destExists {
			continue
		}

		if srcInfo.IsDir() {
			if !opts.recursive {
				return exitf(inv, 1, "cp: omitting directory %q", source)
			}
			if destAbs == srcAbs || strings.HasPrefix(destAbs, srcAbs+"/") {
				return exitf(inv, 1, "cp: cannot copy %q into itself", source)
			}
			if err := copyTree(ctx, inv, srcAbs, destAbs); err != nil {
				return err
			}
			continue
		}

		if err := copyFileContents(ctx, inv, srcAbs, destAbs, srcInfo.Mode().Perm()); err != nil {
			return err
		}

		if opts.verbose {
			if _, err := fmt.Fprintf(inv.Stdout, "'%s' -> '%s'\n", source, destAbs); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	return nil
}

type cpOptions struct {
	recursive bool
	noClobber bool
	preserve  bool
	verbose   bool
}

func parseCPArgs(inv *Invocation) (cpOptions, []string, error) {
	args := inv.Args
	var opts cpOptions
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		arg := args[0]
		if arg == "--" {
			return opts, args[1:], nil
		}
		switch arg {
		case "-r", "-R", "--recursive":
			opts.recursive = true
		case "-n", "--no-clobber":
			opts.noClobber = true
		case "-p", "--preserve":
			opts.preserve = true
		case "-v", "--verbose":
			opts.verbose = true
		default:
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				for _, flag := range arg[1:] {
					switch flag {
					case 'r', 'R':
						opts.recursive = true
					case 'n':
						opts.noClobber = true
					case 'p':
						opts.preserve = true
					case 'v':
						opts.verbose = true
					default:
						return cpOptions{}, nil, exitf(inv, 1, "cp: unsupported flag -%c", flag)
					}
				}
				args = args[1:]
				continue
			}
			return cpOptions{}, nil, exitf(inv, 1, "cp: unsupported flag %s", arg)
		}
		args = args[1:]
	}
	return opts, args, nil
}

var _ Command = (*CP)(nil)
