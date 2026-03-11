package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"strings"

	"github.com/ewhauser/jbgo/policy"
)

type Find struct{}

const findHelpText = `find - search for files in a directory hierarchy

Usage:
  find [path ...] [expression]

Supported predicates:
  -name PATTERN       file name matches shell pattern
  -iname PATTERN      case-insensitive file name match
  -path PATTERN       displayed path matches shell pattern
  -ipath PATTERN      case-insensitive displayed path match
  -regex PATTERN      displayed path matches regular expression
  -iregex PATTERN     case-insensitive regular expression match
  -type f|d           filter by file or directory
  -empty              match empty files and empty directories
  -mtime N            match modification age in days (+N, -N, N)
  -newer FILE         match files newer than FILE
  -size N[ckMGb]      match file size
  -maxdepth N         descend at most N levels
  -a, -and            logical AND
  -o, -or             logical OR
  -not, !             negate the following expression
  -print              always true; default output remains enabled
  --help              show this help text
`

func NewFind() *Find {
	return &Find{}
}

func (c *Find) Name() string {
	return "find"
}

func (c *Find) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) == 1 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprint(inv.Stdout, findHelpText)
		return nil
	}

	paths, opts, expr, err := parseFindCommandArgs(inv)
	if err != nil {
		return err
	}
	if err := resolveFindExpr(ctx, inv, expr); err != nil {
		return err
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
		if err := c.walk(ctx, inv, root, rootAbs, rootAbs, 0, opts, expr); err != nil {
			return err
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func (c *Find) walk(ctx context.Context, inv *Invocation, rootArg, rootAbs, currentAbs string, depth int, opts findCommandOptions, expr findExpr) error {
	info, _, err := statPath(ctx, inv, currentAbs)
	if err != nil {
		return err
	}

	displayPath := walkDisplayPath(rootArg, rootAbs, currentAbs)
	var entries []stdfs.DirEntry
	entriesLoaded := false
	isEmpty := false

	if info.IsDir() && findExprNeedsEmptyCheck(expr) {
		entries, _, err = readDir(ctx, inv, currentAbs)
		if err != nil {
			return err
		}
		entriesLoaded = true
		isEmpty = len(entries) == 0
	} else if !info.IsDir() {
		isEmpty = info.Size() == 0
	}

	matchCtx := &findEvalContext{
		displayPath: displayPath,
		name:        info.Name(),
		isDir:       info.IsDir(),
		isEmpty:     isEmpty,
		mtime:       info.ModTime(),
		size:        info.Size(),
	}
	if evaluateFindExpr(expr, matchCtx) {
		if _, err := fmt.Fprintln(inv.Stdout, displayPath); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if !info.IsDir() || (opts.hasMaxDepth && depth >= opts.maxDepth) {
		return nil
	}
	if !entriesLoaded {
		entries, _, err = readDir(ctx, inv, currentAbs)
		if err != nil {
			return err
		}
	}

	for _, entry := range entries {
		childAbs := path.Join(currentAbs, entry.Name())
		if err := c.walk(ctx, inv, rootArg, rootAbs, childAbs, depth+1, opts, expr); err != nil {
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
