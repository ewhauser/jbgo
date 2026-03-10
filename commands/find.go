package commands

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

type Find struct{}

type findOptions struct {
	namePattern string
	typeFilter  byte
	maxDepth    int
	hasMaxDepth bool
}

func NewFind() *Find {
	return &Find{}
}

func (c *Find) Name() string {
	return "find"
}

func (c *Find) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	if len(args) == 1 && args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: find [path ...] [-name pattern] [-type f|d] [-maxdepth N]")
		return nil
	}

	paths := make([]string, 0, len(args))
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		paths = append(paths, args[0])
		args = args[1:]
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}

	var opts findOptions
	for len(args) > 0 {
		switch args[0] {
		case "-name":
			if len(args) < 2 {
				return exitf(inv, 1, "find: missing argument to -name")
			}
			opts.namePattern = args[1]
			args = args[2:]
		case "-type":
			if len(args) < 2 {
				return exitf(inv, 1, "find: missing argument to -type")
			}
			if args[1] != "f" && args[1] != "d" {
				return exitf(inv, 1, "find: Unknown argument to -type: %s", args[1])
			}
			opts.typeFilter = args[1][0]
			args = args[2:]
		case "-maxdepth":
			if len(args) < 2 {
				return exitf(inv, 1, "find: missing argument to -maxdepth")
			}
			maxDepth, err := strconv.Atoi(args[1])
			if err != nil || maxDepth < 0 {
				return exitf(inv, 1, "find: invalid maxdepth %q", args[1])
			}
			opts.maxDepth = maxDepth
			opts.hasMaxDepth = true
			args = args[2:]
		default:
			return exitf(inv, 1, "find: unknown predicate %q", args[0])
		}
	}

	exitCode := 0
	for _, root := range paths {
		rootAbs := path.Join(inv.Dir, root)
		if strings.HasPrefix(root, "/") {
			rootAbs = root
		}
		if _, _, exists, err := statMaybe(ctx, inv, policy.FileActionStat, rootAbs); err != nil {
			return err
		} else if !exists {
			_, _ = fmt.Fprintf(inv.Stderr, "find: %s: No such file or directory\n", root)
			exitCode = 1
			continue
		}

		if err := c.walk(ctx, inv, root, rootAbs, rootAbs, 0, opts); err != nil {
			return err
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func findMatches(opts findOptions, name string, isDir bool, depth int) bool {
	if opts.hasMaxDepth && depth > opts.maxDepth {
		return false
	}
	if opts.namePattern != "" {
		matched, err := path.Match(opts.namePattern, name)
		if err != nil || !matched {
			return false
		}
	}
	if opts.typeFilter == 'f' && isDir {
		return false
	}
	if opts.typeFilter == 'd' && !isDir {
		return false
	}
	return true
}

func (c *Find) walk(ctx context.Context, inv *Invocation, rootArg, rootAbs, currentAbs string, depth int, opts findOptions) error {
	info, _, err := statPath(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	if findMatches(opts, info.Name(), info.IsDir(), depth) {
		if _, err := fmt.Fprintln(inv.Stdout, walkDisplayPath(rootArg, rootAbs, currentAbs)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if !info.IsDir() || (opts.hasMaxDepth && depth >= opts.maxDepth) {
		return nil
	}

	entries, _, err := readDir(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childAbs := path.Join(currentAbs, entry.Name())
		if err := c.walk(ctx, inv, rootArg, rootAbs, childAbs, depth+1, opts); err != nil {
			return err
		}
	}
	return nil
}

func walkDisplayPath(rootArg, rootAbs, currentAbs string) string {
	if currentAbs == rootAbs {
		if strings.HasPrefix(rootArg, "/") {
			return rootAbs
		}
		if rootArg == "" {
			return "."
		}
		return rootArg
	}

	rel := strings.TrimPrefix(currentAbs, rootAbs+"/")
	if strings.HasPrefix(rootArg, "/") {
		return currentAbs
	}
	if rootArg == "." {
		return "./" + rel
	}
	return path.Join(rootArg, rel)
}

var _ Command = (*Find)(nil)
