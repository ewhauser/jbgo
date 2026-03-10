package commands

import (
	"context"
	"strings"
)

type CP struct{}

func NewCP() *CP {
	return &CP{}
}

func (c *CP) Name() string {
	return "cp"
}

func (c *CP) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	recursive := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-r", "-R", "--recursive":
			recursive = true
		default:
			return exitf(inv, 1, "cp: unsupported flag %s", args[0])
		}
		args = args[1:]
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

		if srcInfo.IsDir() {
			if !recursive {
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
	}

	return nil
}

var _ Command = (*CP)(nil)
