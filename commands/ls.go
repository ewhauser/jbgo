package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/policy"
)

type LS struct{}

type lsOptions struct {
	showAll       bool
	showAlmostAll bool
	longFormat    bool
	humanReadable bool
	recursive     bool
	reverse       bool
	sortBySize    bool
	sortByTime    bool
	classify      bool
	directoryOnly bool
	onePerLine    bool
}

type lsEntry struct {
	name string
	info stdfs.FileInfo
}

const lsHelpText = `ls - list directory contents

Usage:
  ls [OPTION]... [FILE]...

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
`

func NewLS() *LS {
	return &LS{}
}

func (c *LS) Name() string {
	return "ls"
}

func (c *LS) Run(ctx context.Context, inv *Invocation) error {
	for _, arg := range inv.Args {
		if arg == "--help" {
			if _, err := fmt.Fprint(inv.Stdout, lsHelpText); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
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

	var stdout strings.Builder
	exitCode := 0

	for i, target := range targets {
		if i > 0 && stdout.Len() > 0 && !strings.HasSuffix(stdout.String(), "\n\n") {
			stdout.WriteByte('\n')
		}

		output, status, err := c.listPath(ctx, inv, target, opts, len(targets) > 1)
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

func parseLSArgs(inv *Invocation) (lsOptions, []string, error) {
	args := inv.Args
	var opts lsOptions

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}

		switch arg {
		case "-a", "--all":
			opts.showAll = true
		case "-A", "--almost-all":
			opts.showAlmostAll = true
		case "-d", "--directory":
			opts.directoryOnly = true
		case "-F", "--classify":
			opts.classify = true
		case "-h", "--human-readable":
			opts.humanReadable = true
		case "-l":
			opts.longFormat = true
		case "-r", "--reverse":
			opts.reverse = true
		case "-R", "--recursive":
			opts.recursive = true
		case "-S":
			opts.sortBySize = true
		case "-t":
			opts.sortByTime = true
		case "-1":
			opts.onePerLine = true
		default:
			if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
				for _, flag := range arg[1:] {
					switch flag {
					case 'a':
						opts.showAll = true
					case 'A':
						opts.showAlmostAll = true
					case 'd':
						opts.directoryOnly = true
					case 'F':
						opts.classify = true
					case 'h':
						opts.humanReadable = true
					case 'l':
						opts.longFormat = true
					case 'r':
						opts.reverse = true
					case 'R':
						opts.recursive = true
					case 'S':
						opts.sortBySize = true
					case 't':
						opts.sortByTime = true
					case '1':
						opts.onePerLine = true
					default:
						return lsOptions{}, nil, exitf(inv, 1, "ls: unsupported flag -%c", flag)
					}
				}
			} else {
				return lsOptions{}, nil, exitf(inv, 1, "ls: unsupported flag %s", arg)
			}
		}
		args = args[1:]
	}

	return opts, args, nil
}

func (c *LS) listPath(ctx context.Context, inv *Invocation, target string, opts lsOptions, showHeader bool) (output string, status int, err error) {
	info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, target)
	if err != nil {
		return "", 0, err
	}
	if !exists {
		_, _ = fmt.Fprintf(inv.Stderr, "ls: %s: No such file or directory\n", target)
		return "", 2, nil
	}

	if opts.directoryOnly || !info.IsDir() {
		return c.renderPathEntry(ctx, inv, target, abs, info, opts)
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

	entryInfos, err := c.loadLSEntries(ctx, inv, abs, filtered)
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
	for _, entry := range entryInfos {
		line, err := c.renderDirectoryEntry(ctx, inv, abs, entry, opts)
		if err != nil {
			return "", 0, err
		}
		out.WriteString(line)
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
			subOutput, status, err := c.listPath(ctx, inv, subTarget, opts, false)
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

func (c *LS) renderPathEntry(ctx context.Context, inv *Invocation, target, abs string, info stdfs.FileInfo, opts lsOptions) (output string, status int, err error) {
	suffix := ""
	switch {
	case opts.classify:
		linfo, _, err := lstatPath(ctx, inv, abs)
		if err != nil {
			return "", 0, err
		}
		suffix = classifyLSSuffix(linfo)
	case opts.longFormat && info.IsDir():
		suffix = "/"
	}
	if opts.longFormat {
		return formatLSLongLine(target+suffix, info, opts.humanReadable), 0, nil
	}
	return target + suffix + "\n", 0, nil
}

func (c *LS) renderDirectoryEntry(ctx context.Context, inv *Invocation, dirAbs string, entry lsEntry, opts lsOptions) (string, error) {
	name := entry.name
	if opts.classify {
		switch name {
		case ".", "..":
			name += "/"
		default:
			linfo, _, err := lstatPath(ctx, inv, path.Join(dirAbs, name))
			if err != nil {
				return "", err
			}
			name += classifyLSSuffix(linfo)
		}
	} else if opts.longFormat && entry.info.IsDir() {
		name += "/"
	}
	if opts.longFormat {
		return formatLSLongLine(name, entry.info, opts.humanReadable), nil
	}
	return name + "\n", nil
}

func (c *LS) loadLSEntries(ctx context.Context, inv *Invocation, dirAbs string, names []string) ([]lsEntry, error) {
	entries := make([]lsEntry, 0, len(names))
	for _, name := range names {
		switch name {
		case ".", "..":
			info, _, err := statPath(ctx, inv, dirAbs)
			if err != nil {
				return nil, err
			}
			entries = append(entries, lsEntry{name: name, info: info})
		default:
			info, _, err := statPath(ctx, inv, path.Join(dirAbs, name))
			if err != nil {
				return nil, err
			}
			entries = append(entries, lsEntry{name: name, info: info})
		}
	}
	return entries, nil
}

func sortLSEntries(entries []lsEntry, opts lsOptions) {
	switch {
	case opts.sortBySize:
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].info.Size() == entries[j].info.Size() {
				return entries[i].name < entries[j].name
			}
			return entries[i].info.Size() > entries[j].info.Size()
		})
	case opts.sortByTime:
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].info.ModTime().Equal(entries[j].info.ModTime()) {
				return entries[i].name < entries[j].name
			}
			return entries[i].info.ModTime().After(entries[j].info.ModTime())
		})
	default:
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].name < entries[j].name
		})
	}
	if opts.reverse {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}
}

func formatLSLongLine(name string, info stdfs.FileInfo, humanReadable bool) string {
	size := info.Size()
	sizeField := strconv.FormatInt(size, 10)
	if humanReadable {
		sizeField = formatHumanSize(size)
	}
	sizeField = fmt.Sprintf("%5s", sizeField)
	return fmt.Sprintf("%s 1 user user %s %s %s\n", formatModeLong(info.Mode()), sizeField, formatLSDate(info.ModTime()), name)
}

func formatHumanSize(bytes int64) string {
	if bytes < 1024 {
		return strconv.FormatInt(bytes, 10)
	}
	if bytes < 1024*1024 {
		k := float64(bytes) / 1024
		if k < 10 {
			return fmt.Sprintf("%.1fK", k)
		}
		return fmt.Sprintf("%.0fK", k)
	}
	if bytes < 1024*1024*1024 {
		m := float64(bytes) / (1024 * 1024)
		if m < 10 {
			return fmt.Sprintf("%.1fM", m)
		}
		return fmt.Sprintf("%.0fM", m)
	}
	g := float64(bytes) / (1024 * 1024 * 1024)
	if g < 10 {
		return fmt.Sprintf("%.1fG", g)
	}
	return fmt.Sprintf("%.0fG", g)
}

func formatLSDate(ts time.Time) string {
	month := ts.Format("Jan")
	day := fmt.Sprintf("%2d", ts.Day())
	sixMonthsAgo := time.Now().Add(-180 * 24 * time.Hour)
	if ts.After(sixMonthsAgo) {
		return fmt.Sprintf("%s %s %02d:%02d", month, day, ts.Hour(), ts.Minute())
	}
	return fmt.Sprintf("%s %s  %04d", month, day, ts.Year())
}

func classifyLSSuffix(info stdfs.FileInfo) string {
	if info.Mode()&stdfs.ModeSymlink != 0 {
		return "@"
	}
	if info.IsDir() {
		return "/"
	}
	if info.Mode()&0o111 != 0 {
		return "*"
	}
	return ""
}

var _ Command = (*LS)(nil)
