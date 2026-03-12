package commands

import (
	"context"
	"fmt"
	"io"
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
	for _, arg := range inv.Args {
		switch arg {
		case "--help":
			_, _ = io.WriteString(inv.Stdout, dirHelpText)
			return nil
		case "--version":
			_, _ = io.WriteString(inv.Stdout, dirVersionText)
			return nil
		}
	}

	opts, targets, err := parseLSArgs(inv)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		targets = []string{"."}
	}

	defaultColumns := !dirHasExplicitFormat(inv.Args)
	var stdout strings.Builder
	exitCode := 0
	for i, target := range targets {
		if i > 0 && stdout.Len() > 0 && !strings.HasSuffix(stdout.String(), "\n\n") {
			stdout.WriteByte('\n')
		}

		output, status, err := c.listPath(ctx, inv, target, opts, len(targets) > 1, defaultColumns)
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

func dirHasExplicitFormat(args []string) bool {
	for _, arg := range args {
		if arg == "--" || !strings.HasPrefix(arg, "-") || arg == "-" {
			return false
		}
		if arg == "-1" || arg == "-l" {
			return true
		}
		if strings.HasPrefix(arg, "--") {
			continue
		}
		for _, flag := range arg[1:] {
			if flag == '1' || flag == 'l' {
				return true
			}
		}
	}
	return false
}

func (c *Dir) listPath(ctx context.Context, inv *Invocation, target string, opts lsOptions, showHeader, defaultColumns bool) (output string, status int, err error) {
	info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, target)
	if err != nil {
		return "", 0, err
	}
	if !exists {
		_, _ = fmt.Fprintf(inv.Stderr, "dir: %s: No such file or directory\n", target)
		return "", 2, nil
	}

	if opts.directoryOnly || !info.IsDir() {
		return c.renderPathEntry(ctx, inv, target, abs, info, opts, defaultColumns)
	}

	entries, _, err := readDir(ctx, inv, target)
	if err != nil {
		return "", 0, err
	}

	filtered := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !opts.showAll && !opts.showAlmostAll && strings.HasPrefix(name, ".") {
			continue
		}
		filtered = append(filtered, name)
	}
	if opts.showAll {
		filtered = append([]string{".", ".."}, filtered...)
	}

	ls := &LS{}
	entryInfos, err := ls.loadLSEntries(ctx, inv, abs, filtered)
	if err != nil {
		return "", 0, err
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
	if defaultColumns && !opts.longFormat && !opts.onePerLine {
		items := make([]string, 0, len(entryInfos))
		for _, entry := range entryInfos {
			name, err := c.renderEntryName(ctx, inv, abs, entry, opts)
			if err != nil {
				return "", 0, err
			}
			items = append(items, name)
		}
		if len(items) > 0 {
			out.WriteString(strings.Join(items, "  "))
			out.WriteByte('\n')
		}
	} else {
		for _, entry := range entryInfos {
			line, err := c.renderDirectoryEntry(ctx, inv, abs, entry, opts)
			if err != nil {
				return "", 0, err
			}
			out.WriteString(line)
		}
	}

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
			subOutput, status, err := c.listPath(ctx, inv, subTarget, opts, false, defaultColumns)
			if err != nil {
				return "", 0, err
			}
			out.WriteString(subOutput)
			if status != 0 {
				return out.String(), status, nil
			}
		}
	}

	return out.String(), 0, nil
}

func (c *Dir) renderPathEntry(ctx context.Context, inv *Invocation, target, abs string, info stdfs.FileInfo, opts lsOptions, defaultColumns bool) (output string, status int, err error) {
	name := dirQuoteName(target)
	switch {
	case opts.classify:
		linfo, _, err := lstatPath(ctx, inv, abs)
		if err != nil {
			return "", 0, err
		}
		name += classifyLSSuffix(linfo)
	case opts.longFormat && info.IsDir():
		name += "/"
	}
	if opts.longFormat {
		return formatLSLongLine(name, info, opts.humanReadable), 0, nil
	}
	if defaultColumns && !opts.onePerLine {
		return name + "\n", 0, nil
	}
	return name + "\n", 0, nil
}

func (c *Dir) renderDirectoryEntry(ctx context.Context, inv *Invocation, dirAbs string, entry lsEntry, opts lsOptions) (string, error) {
	name, err := c.renderEntryName(ctx, inv, dirAbs, entry, opts)
	if err != nil {
		return "", err
	}
	if opts.longFormat {
		return formatLSLongLine(name, entry.info, opts.humanReadable), nil
	}
	return name + "\n", nil
}

func (c *Dir) renderEntryName(ctx context.Context, inv *Invocation, dirAbs string, entry lsEntry, opts lsOptions) (string, error) {
	name := dirQuoteName(entry.name)
	if opts.classify {
		switch entry.name {
		case ".", "..":
			name += "/"
		default:
			linfo, _, err := lstatPath(ctx, inv, path.Join(dirAbs, entry.name))
			if err != nil {
				return "", err
			}
			name += classifyLSSuffix(linfo)
		}
	} else if opts.longFormat && entry.info.IsDir() {
		name += "/"
	}
	return name, nil
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
  --help              show this help text
  --version           show version information
`

const dirVersionText = "dir (gbash) dev\n"

var _ Command = (*Dir)(nil)
