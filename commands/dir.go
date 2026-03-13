package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type Dir struct{}

func NewDir() *Dir {
	return &Dir{}
}

func (c *Dir) Name() string {
	return "dir"
}

func (c *Dir) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Dir) Spec() CommandSpec {
	return CommandSpec{
		Name:  "dir",
		Usage: "dir [OPTION]... [FILE]...",
		Options: append(lsOptionSpecs(),
			OptionSpec{Name: "version", Long: "version", Help: "show version information"},
		),
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
		},
		HelpRenderer:    renderStaticHelp(dirHelpText),
		VersionRenderer: renderStaticVersion(dirVersionText),
	}
}

func (c *Dir) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return renderStaticHelp(dirHelpText)(inv.Stdout, c.Spec())
	}
	if matches.Has("version") {
		return renderStaticVersion(dirVersionText)(inv.Stdout, c.Spec())
	}
	opts, err := lsOptionsFromParsed(inv, matches)
	if err != nil {
		return err
	}
	targets := matches.Args("file")
	if len(targets) == 0 {
		targets = []string{"."}
	}

	defaultColumns := !opts.longFormat && !opts.zero && !lsHasExplicitFormat(matches)
	var stdout strings.Builder
	exitCode := 0
	for i, target := range targets {
		if i > 0 && stdout.Len() > 0 && !strings.HasSuffix(stdout.String(), "\n\n") {
			stdout.WriteByte('\n')
		}

		output, status, _, err := c.listPath(ctx, inv, target, &opts, len(targets) > 1, defaultColumns)
		if err != nil {
			return err
		}
		stdout.WriteString(output)
		if status > exitCode {
			exitCode = status
		}
	}

	if _, err := fmt.Fprint(inv.Stdout, stdout.String()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func (c *Dir) listPath(ctx context.Context, inv *Invocation, target string, opts *lsOptions, showHeader, defaultColumns bool) (output string, status int, rendered lsRenderResult, err error) {
	info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, target)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	if !exists {
		_, _ = fmt.Fprintf(inv.Stderr, "dir: %s: No such file or directory\n", target)
		return "", 2, lsRenderResult{}, nil
	}

	if opts.directoryOnly || !info.IsDir() {
		return c.renderPathEntry(ctx, inv, target, abs, info, opts, defaultColumns)
	}

	entries, _, err := readDir(ctx, inv, target)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}

	filtered := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !lsShouldIncludeEntry(name, opts) {
			continue
		}
		filtered = append(filtered, name)
	}
	if opts.showAll {
		filtered = append([]string{".", ".."}, filtered...)
	}

	ls := &LS{}
	entryInfos, err := ls.loadLSEntries(ctx, inv, abs, filtered, opts)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	sortLSEntries(entryInfos, opts)

	var out strings.Builder
	if opts.recursive || showHeader {
		out.WriteString(target)
		out.WriteString(":\n")
	}
	if opts.longFormat {
		out.WriteString("total ")
		out.WriteString(strconv.Itoa(len(entryInfos)))
		out.WriteByte('\n')
	}
	rendered, err = lsRenderEntries(ctx, inv, abs, entryInfos, opts, dirQuoteName, defaultColumns)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	out.WriteString(rendered.text)

	if opts.recursive {
		subdirs := make([]lsEntry, 0)
		for _, entry := range entryInfos {
			if entry.name == "." || entry.name == ".." || !entry.info.IsDir() {
				continue
			}
			subdirs = append(subdirs, entry)
		}
		for _, dir := range subdirs {
			out.WriteByte('\n')
			subTarget := target
			switch subTarget {
			case ".":
				subTarget = "./" + dir.name
			case "/":
				subTarget = "/" + dir.name
			default:
				subTarget = path.Join(subTarget, dir.name)
			}
			subOutput, status, _, err := c.listPath(ctx, inv, subTarget, opts, false, defaultColumns)
			if err != nil {
				return "", 0, lsRenderResult{}, err
			}
			out.WriteString(subOutput)
			if status != 0 {
				return out.String(), status, lsRenderResult{}, nil
			}
		}
	}

	return out.String(), 0, lsRenderResult{text: out.String()}, nil
}

func (c *Dir) renderPathEntry(ctx context.Context, inv *Invocation, target, abs string, info stdfs.FileInfo, opts *lsOptions, defaultColumns bool) (output string, status int, rendered lsRenderResult, err error) {
	name, _, err := lsDecoratedName(ctx, inv, target, abs, info, opts, dirQuoteName)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	if opts.longFormat {
		line, _ := formatLSLongLine(name, info, opts, nil)
		return line, 0, lsRenderResult{text: line}, nil
	}
	if defaultColumns {
		line := name + lsTerminator(opts)
		return line, 0, lsRenderResult{text: line}, nil
	}
	line := name + lsTerminator(opts)
	return line, 0, lsRenderResult{text: line}, nil
}

func lsHasExplicitFormat(matches *ParsedCommand) bool {
	for _, option := range matches.OptionOrder() {
		switch option {
		case "one-per-line", "columns", "across", "commas", "format":
			return true
		}
	}
	return false
}

func dirQuoteName(name string) string {
	var out strings.Builder
	for _, r := range name {
		switch r {
		case '\\':
			out.WriteString("\\\\")
		case '\a':
			out.WriteString("\\a")
		case '\b':
			out.WriteString("\\b")
		case '\f':
			out.WriteString("\\f")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		case '\v':
			out.WriteString("\\v")
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&out, "\\x%02x", r)
				continue
			}
			out.WriteRune(r)
		}
	}
	return out.String()
}

const dirHelpText = `dir - list directory contents in columns

Usage:
  dir [OPTION]... [FILE]...

Supported options:
  -1                  list one file per line
  -a, --all           do not ignore entries starting with .
  -A, --almost-all    do not list implied . and ..
  -d, --directory     list directories themselves, not their contents
  -F, --classify      append indicator (one of */=>@) to entries
  -h, --human-readable
                      with -l, print sizes like 1K 234M 2G
  -l                  use a long listing format
  -r, --reverse       reverse order while sorting
  -R, --recursive     list subdirectories recursively
  -S                  sort by file size, largest first
  -t                  sort by time, newest first
  --color[=WHEN]      colorize the output; WHEN can be 'always', 'auto', or 'never'
  --help              show this help text
  --version           show version information
`

const dirVersionText = "dir (gbash) dev\n"

var _ Command = (*Dir)(nil)
var _ SpecProvider = (*Dir)(nil)
var _ ParsedRunner = (*Dir)(nil)
