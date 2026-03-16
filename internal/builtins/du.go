package builtins

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"strconv"
)

type DU struct{}

type duOptions struct {
	showAll     bool
	summary     bool
	human       bool
	total       bool
	maxDepth    int
	hasMaxDepth bool
}

func NewDU() *DU {
	return &DU{}
}

func (c *DU) Name() string {
	return "du"
}

func (c *DU) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *DU) Spec() CommandSpec {
	return CommandSpec{
		Name:  "du",
		About: "Estimate file space usage.",
		Usage: "du [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "all", Short: 'a', Long: "all", Help: "write counts for all files, not just directories"},
			{Name: "summarize", Short: 's', Long: "summarize", Help: "display only a total for each argument"},
			{Name: "human-readable", Short: 'h', Long: "human-readable", Help: "print sizes in human readable format"},
			{Name: "total", Short: 'c', Long: "total", Help: "produce a grand total"},
			{Name: "max-depth", Long: "max-depth", Arity: OptionRequiredValue, ValueName: "N", Help: "print the total for a directory only if it is N or fewer levels below the command line argument"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
	}
}

func (c *DU) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseDUMatches(inv, matches)
	if err != nil {
		return err
	}

	targets := matches.Args("file")
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

func parseDUMatches(inv *Invocation, matches *ParsedCommand) (duOptions, error) {
	opts := duOptions{}
	for _, name := range matches.OptionOrder() {
		switch name {
		case "all":
			opts.showAll = true
		case "summarize":
			opts.summary = true
		case "human-readable":
			opts.human = true
		case "total":
			opts.total = true
		case "max-depth":
			maxDepth, err := strconv.Atoi(matches.Value("max-depth"))
			if err != nil || maxDepth < 0 {
				return duOptions{}, exitf(inv, 1, "du: invalid maximum depth %q", matches.Value("max-depth"))
			}
			opts.maxDepth = maxDepth
			opts.hasMaxDepth = true
		}
	}
	return opts, nil
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

	entries, err := readDir(ctx, inv, abs)
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
var _ SpecProvider = (*DU)(nil)
var _ ParsedRunner = (*DU)(nil)
