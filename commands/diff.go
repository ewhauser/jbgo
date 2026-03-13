package commands

import (
	"context"
	"fmt"
	"strings"
)

type Diff struct{}

type diffOptions struct {
	brief      bool
	reportSame bool
	ignoreCase bool
}

type diffOp struct {
	kind byte
	line string
}

func NewDiff() *Diff {
	return &Diff{}
}

func (c *Diff) Name() string {
	return "diff"
}

func (c *Diff) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Diff) Spec() CommandSpec {
	return CommandSpec{
		Name:  "diff",
		About: "Compare files line by line.",
		Usage: "diff [OPTION]... FILES",
		Options: []OptionSpec{
			{Name: "unified", Short: 'u', Long: "unified", Help: "output unified format"},
			{Name: "brief", Short: 'q', Long: "brief", Help: "report only when files differ"},
			{Name: "report-identical-files", Short: 's', Long: "report-identical-files", Help: "report when two files are the same"},
			{Name: "ignore-case", Short: 'i', Long: "ignore-case", Help: "ignore case differences in file contents"},
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

func (c *Diff) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, leftName, rightName, err := parseDiffMatches(inv, matches)
	if err != nil {
		return err
	}
	leftData, rightData, err := readTwoInputs(ctx, inv, leftName, rightName)
	if err != nil {
		return err
	}
	leftLines := textLines(leftData)
	rightLines := textLines(rightData)

	if diffEqual(leftLines, rightLines, opts.ignoreCase) {
		if opts.reportSame {
			if _, err := fmt.Fprintf(inv.Stdout, "Files %s and %s are identical\n", leftName, rightName); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	if opts.brief {
		if _, err := fmt.Fprintf(inv.Stdout, "Files %s and %s differ\n", leftName, rightName); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return &ExitError{Code: 1}
	}

	ops := buildDiffOps(leftLines, rightLines, opts.ignoreCase)
	if err := writeUnifiedDiff(inv, leftName, rightName, leftLines, rightLines, ops); err != nil {
		return err
	}
	return &ExitError{Code: 1}
}

func parseDiffMatches(inv *Invocation, matches *ParsedCommand) (opts diffOptions, leftName, rightName string, err error) {
	for _, name := range matches.OptionOrder() {
		switch name {
		case "brief":
			opts.brief = true
		case "report-identical-files":
			opts.reportSame = true
		case "ignore-case":
			opts.ignoreCase = true
		case "unified":
			// accepted; unified is the only output format currently implemented
		}
	}
	args := matches.Args("file")
	if len(args) != 2 {
		return diffOptions{}, "", "", exitf(inv, 2, "diff: expected exactly two input files")
	}
	return opts, args[0], args[1], nil
}

func diffEqual(left, right []string, ignoreCase bool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !diffLineEqual(left[i], right[i], ignoreCase) {
			return false
		}
	}
	return true
}

func diffLineEqual(left, right string, ignoreCase bool) bool {
	if ignoreCase {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func buildDiffOps(left, right []string, ignoreCase bool) []diffOp {
	dp := make([][]int, len(left)+1)
	for i := range dp {
		dp[i] = make([]int, len(right)+1)
	}
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if diffLineEqual(left[i], right[j], ignoreCase) {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			dp[i][j] = max(dp[i+1][j], dp[i][j+1])
		}
	}

	var ops []diffOp
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		switch {
		case diffLineEqual(left[i], right[j], ignoreCase):
			ops = append(ops, diffOp{kind: ' ', line: left[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{kind: '-', line: left[i]})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', line: right[j]})
			j++
		}
	}
	for ; i < len(left); i++ {
		ops = append(ops, diffOp{kind: '-', line: left[i]})
	}
	for ; j < len(right); j++ {
		ops = append(ops, diffOp{kind: '+', line: right[j]})
	}
	return ops
}

func writeUnifiedDiff(inv *Invocation, leftName, rightName string, left, right []string, ops []diffOp) error {
	if _, err := fmt.Fprintf(inv.Stdout, "--- %s\n+++ %s\n", leftName, rightName); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if _, err := fmt.Fprintf(inv.Stdout, "@@ -1,%d +1,%d @@\n", len(left), len(right)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	for _, op := range ops {
		if _, err := fmt.Fprintf(inv.Stdout, "%c%s\n", op.kind, op.line); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

var _ Command = (*Diff)(nil)
var _ SpecProvider = (*Diff)(nil)
var _ ParsedRunner = (*Diff)(nil)
