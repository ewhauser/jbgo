package builtins

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"strconv"
	"strings"
)

type Tree struct{}

type treeOptions struct {
	showHidden  bool
	dirsOnly    bool
	fullPath    bool
	maxDepth    int
	hasMaxDepth bool
}

func NewTree() *Tree {
	return &Tree{}
}

func (c *Tree) Name() string {
	return "tree"
}

func (c *Tree) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Tree) Spec() CommandSpec {
	return CommandSpec{
		Name:  "tree",
		About: "List contents of directories in a tree-like format.",
		Usage: "tree [OPTION]... [DIRECTORY]",
		Options: []OptionSpec{
			{Name: "all", Short: 'a', Help: "all files are listed"},
			{Name: "dirs-only", Short: 'd', Help: "list directories only"},
			{Name: "full-path", Short: 'f', Help: "print the full path prefix for each file"},
			{Name: "level", Short: 'L', Arity: OptionRequiredValue, ValueName: "LEVEL", Help: "descend only LEVEL directories deep"},
		},
		Args: []ArgSpec{
			{Name: "target", ValueName: "DIRECTORY"},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Tree) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseTreeMatches(inv, matches)
	if err != nil {
		return err
	}
	target := "."
	if matches.Arg("target") != "" {
		target = matches.Arg("target")
	}

	info, abs, err := lstatPath(ctx, inv, target)
	if err != nil {
		return err
	}

	rootLabel := treeLabel(target, abs, opts.fullPath)
	if _, err := fmt.Fprintln(inv.Stdout, rootLabel); err != nil {
		return &ExitError{Code: 1, Err: err}
	}

	dirs, files, err := c.renderChildren(ctx, inv, abs, info, "", 0, opts)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(inv.Stdout, "\n%d directories, %d files\n", dirs, files); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseTreeMatches(inv *Invocation, matches *ParsedCommand) (treeOptions, error) {
	opts := treeOptions{
		showHidden: matches.Has("all"),
		dirsOnly:   matches.Has("dirs-only"),
		fullPath:   matches.Has("full-path"),
	}
	if matches.Has("level") {
		maxDepth, err := strconv.Atoi(matches.Value("level"))
		if err != nil || maxDepth < 0 {
			return treeOptions{}, exitf(inv, 1, "tree: invalid level %q", matches.Value("level"))
		}
		opts.maxDepth = maxDepth
		opts.hasMaxDepth = true
	}
	return opts, nil
}

func (c *Tree) renderChildren(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, prefix string, depth int, opts treeOptions) (dirs, files int, err error) {
	if !info.IsDir() {
		return 0, 1, nil
	}
	if opts.hasMaxDepth && depth >= opts.maxDepth {
		return 0, 0, nil
	}

	entries, _, err := readDir(ctx, inv, abs)
	if err != nil {
		return 0, 0, err
	}
	filtered := make([]stdfs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !opts.showHidden && strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if opts.dirsOnly {
			childInfo, _, err := lstatPath(ctx, inv, path.Join(abs, entry.Name()))
			if err != nil {
				return 0, 0, err
			}
			if !childInfo.IsDir() {
				continue
			}
		}
		filtered = append(filtered, entry)
	}

	for i, entry := range filtered {
		childAbs := path.Join(abs, entry.Name())
		childInfo, _, err := lstatPath(ctx, inv, childAbs)
		if err != nil {
			return 0, 0, err
		}
		connector := "├── "
		childPrefix := prefix + "│   "
		if i == len(filtered)-1 {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		label := entry.Name()
		if opts.fullPath {
			label = childAbs
		}
		if _, err := fmt.Fprintf(inv.Stdout, "%s%s%s\n", prefix, connector, label); err != nil {
			return 0, 0, &ExitError{Code: 1, Err: err}
		}
		if childInfo.IsDir() {
			dirs++
			childDirs, childFiles, err := c.renderChildren(ctx, inv, childAbs, childInfo, childPrefix, depth+1, opts)
			if err != nil {
				return 0, 0, err
			}
			dirs += childDirs
			files += childFiles
		} else {
			files++
		}
	}
	return dirs, files, nil
}

func treeLabel(target, abs string, fullPath bool) string {
	if fullPath {
		return abs
	}
	if target == "" {
		return "."
	}
	return target
}

var _ Command = (*Tree)(nil)
var _ SpecProvider = (*Tree)(nil)
var _ ParsedRunner = (*Tree)(nil)
