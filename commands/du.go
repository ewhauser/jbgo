package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"strconv"
	"strings"
)

type DU struct{}

type duOptions struct {
	showAll    bool
	summary    bool
	human      bool
	total      bool
	maxDepth   int
	hasMaxDepth bool
}

func NewDU() *DU {
	return &DU{}
}

func (c *DU) Name() string {
	return "du"
}

func (c *DU) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	var opts duOptions

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch {
		case args[0] == "-a":
			opts.showAll = true
			args = args[1:]
		case args[0] == "-s":
			opts.summary = true
			args = args[1:]
		case args[0] == "-h":
			opts.human = true
			args = args[1:]
		case args[0] == "-c":
			opts.total = true
			args = args[1:]
		case args[0] == "--max-depth":
			if len(args) < 2 {
				return exitf(inv, 1, "du: option requires an argument -- max-depth")
			}
			maxDepth, err := strconv.Atoi(args[1])
			if err != nil || maxDepth < 0 {
				return exitf(inv, 1, "du: invalid maximum depth %q", args[1])
			}
			opts.maxDepth = maxDepth
			opts.hasMaxDepth = true
			args = args[2:]
		case strings.HasPrefix(args[0], "--max-depth="):
			value := strings.TrimPrefix(args[0], "--max-depth=")
			maxDepth, err := strconv.Atoi(value)
			if err != nil || maxDepth < 0 {
				return exitf(inv, 1, "du: invalid maximum depth %q", value)
			}
			opts.maxDepth = maxDepth
			opts.hasMaxDepth = true
			args = args[1:]
		default:
			return exitf(inv, 1, "du: unsupported flag %s", args[0])
		}
	}

	targets := args
	if len(targets) == 0 {
		targets = []string{"."}
	}

	exitCode := 0
	var grandTotal int64
	for _, target := range targets {
		info, abs, err := lstatPath(ctx, inv, target)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "du: cannot access %q: No such file or directory\n", target)
			exitCode = 1
			continue
		}
		size, err := c.emit(ctx, inv, abs, info, 0, opts)
		if err != nil {
			return err
		}
		grandTotal += size
	}

	if opts.total && len(targets) > 1 {
		if _, err := fmt.Fprintf(inv.Stdout, "%s\ttotal\n", formatDUSize(grandTotal, opts.human)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func (c *DU) emit(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, depth int, opts duOptions) (int64, error) {
	if !info.IsDir() {
		size := info.Size()
		if opts.showAll || opts.summary || !opts.hasMaxDepth {
			if !opts.summary || depth == 0 {
				if _, err := fmt.Fprintf(inv.Stdout, "%s\t%s\n", formatDUSize(size, opts.human), abs); err != nil {
					return 0, &ExitError{Code: 1, Err: err}
				}
			}
		}
		return size, nil
	}

	entries, _, err := readDir(ctx, inv, abs)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		childAbs := path.Join(abs, entry.Name())
		childInfo, _, err := lstatPath(ctx, inv, childAbs)
		if err != nil {
			return 0, err
		}
		size, err := c.emit(ctx, inv, childAbs, childInfo, depth+1, opts)
		if err != nil {
			return 0, err
		}
		total += size
	}

	shouldPrint := depth == 0 || (!opts.summary && (!opts.hasMaxDepth || depth <= opts.maxDepth))
	if shouldPrint {
		if _, err := fmt.Fprintf(inv.Stdout, "%s\t%s\n", formatDUSize(total, opts.human), abs); err != nil {
			return 0, &ExitError{Code: 1, Err: err}
		}
	}
	return total, nil
}

func formatDUSize(size int64, human bool) string {
	if human {
		return humanizeBytes(size)
	}
	return fmt.Sprintf("%d", size)
}

var _ Command = (*DU)(nil)
