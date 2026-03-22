package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"golang.org/x/term"
)

type LS struct{}

type lsOptions struct {
	showAll               bool
	showAlmostAll         bool
	hidePatterns          []string
	ignorePatterns        []string
	ignoreBackups         bool
	longFormat            bool
	humanReadable         bool
	si                    bool
	kibibytes             bool
	blockSize             int64
	recursive             bool
	reverse               bool
	sortMode              lsSortMode
	groupDirectoriesFirst bool
	directoryOnly         bool
	format                lsFormatMode
	width                 int
	tabSize               int
	zero                  bool
	colorMode             lsColorMode
	hyperlinkMode         lsColorMode
	indicatorMode         lsIndicatorMode
	quotingMode           lsQuotingMode
	hideControlChars      bool
	showInode             bool
	showAllocSize         bool
	showAuthor            bool
	showGroup             bool
	showOwner             bool
	numericIDs            bool
	identityDB            *permissionIdentityDB
	identityDBLoader      func(context.Context, *Invocation) *permissionIdentityDB
	timeStyle             string
	dereference           lsDereferenceMode
	dired                 bool
	listedDirs            map[lsListedDirKey]struct{}
}

type lsEntry struct {
	name                 string
	info                 stdfs.FileInfo
	groupAsDir           bool
	unknownShortMetadata bool
}

type lsPathArg struct {
	name       string
	abs        string
	info       stdfs.FileInfo
	groupAsDir bool
}

type lsListedDirKey struct {
	dev  uint64
	ino  uint64
	path string
}

type lsParsedColors struct {
	entries  map[string]string
	patterns []lsColorPattern
}

type lsColorPattern struct {
	suffix string
	code   string
}

type lsByteRange struct {
	start int
	end   int
}

type lsRenderResult struct {
	text     string
	dired    []lsByteRange
	subdired []lsByteRange
}

type lsColorMode int

type lsFormatMode int

type lsSortMode int

type lsIndicatorMode int

type lsQuotingMode int

type lsDereferenceMode int

const (
	lsColorAuto lsColorMode = iota
	lsColorAlways
	lsColorNever
)

const (
	lsFormatOnePerLine lsFormatMode = iota
	lsFormatColumns
	lsFormatAcross
	lsFormatCommas
)

const (
	lsSortName lsSortMode = iota
	lsSortSize
	lsSortTime
	lsSortVersion
	lsSortExtension
	lsSortNone
	lsSortWidth
)

const (
	lsIndicatorNone lsIndicatorMode = iota
	lsIndicatorSlash
	lsIndicatorFileType
	lsIndicatorClassify
)

const (
	lsQuoteLiteral lsQuotingMode = iota
	lsQuoteEscape
	lsQuoteC
	lsQuoteShell
	lsQuoteShellAlways
)

const (
	lsDerefDefault lsDereferenceMode = iota
	lsDerefAll
	lsDerefArgs
	lsDerefDirArgs
	lsDerefNone
)

const lsHelpText = `ls - list directory contents

Usage:
  ls [OPTION]... [FILE]...

Supported options:
  -1                  list one file per line
  -C                  list entries by columns
  -x                  list entries by lines instead of by columns
  -m                  fill width with a comma separated list of entries
  -a, --all           do not ignore entries starting with .
  -A, --almost-all    do not list implied . and ..
  -B, --ignore-backups
                      do not list implied entries ending with ~
  -d, --directory     list directories themselves, not their contents
  -F, --classify[=WHEN]
                      append indicator (one of */=>@) to entries
  -h, --human-readable
                      with -l, print sizes like 1K 234M 2G
  -I, --ignore=PATTERN
                      do not list implied entries matching shell PATTERN
  -i, --inode         print the index number of each file
  -k, --kibibytes     default to 1024-byte blocks for block counts
  -l                  use a long listing format
  -n, --numeric-uid-gid
                      like -l, but list numeric user and group IDs
  -N, --literal       print entry names without quoting
  -o                  like -l, but do not list group information
  -p                  append / indicator to directories
  -q, --hide-control-chars
                      print ? instead of nongraphic characters
  -Q, --quote-name    enclose entry names in double quotes
  -r, --reverse       reverse order while sorting
  -R, --recursive     list subdirectories recursively
  -S                  sort by file size, largest first
  -s, --size          print the allocated size of each file, in blocks
  -t                  sort by time, newest first
  -U                  do not sort; list entries in directory order
  -v                  natural sort of version numbers within text
  -X                  sort alphabetically by entry extension
  -g                  like -l, but do not list owner information
  -H, --dereference-command-line
                      follow symbolic links listed on the command line
  -L, --dereference   when showing file information for a symbolic link, show information for the referent
  --format=WORD       across, commas, horizontal, long, single-column, verbose, vertical
  --group-directories-first
                      group directories before files
  --hide=PATTERN      do not list implied entries matching shell PATTERN
  --dired             generate output designed for Emacs' dired mode
  --hyperlink[=WHEN]  hyperlink file names; WHEN can be 'always', 'auto', or 'never'
  --indicator-style=WORD
                      append indicators with 'none', 'slash', 'file-type', or 'classify'
  --author            with -l, print the author of each file
  --block-size=SIZE   scale sizes by SIZE before printing them
  --dereference-command-line-symlink-to-dir
                      follow each command line symbolic link that points to a directory
  --full-time         like -l --time-style=full-iso
  --no-group          in long format, do not print group names
  --quoting-style=WORD
                      literal, shell, shell-escape, shell-always, shell-escape-always, c, escape
  --show-control-chars
                      show nongraphic characters as-is
  --si                like -h, but use powers of 1000 not 1024
  --sort=WORD         name, none, size, time, version, extension, width
  --time-style=STYLE  full-iso, long-iso, iso, locale, or +FORMAT
  --width=COLS        set output width to COLS
  --zero              end each output entry with NUL, not newline
  --color[=WHEN]      colorize the output; WHEN can be 'always', 'auto', or 'never'
  --help              show this help text
`

func NewLS() *LS {
	return &LS{}
}

func (c *LS) Name() string {
	return "ls"
}

func (c *LS) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *LS) Spec() CommandSpec {
	return CommandSpec{
		Name:    "ls",
		Usage:   "ls [OPTION]... [FILE]...",
		Options: lsOptionSpecs(),
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
		},
		HelpRenderer: renderStaticHelp(lsHelpText),
	}
}

func (c *LS) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return renderStaticHelp(lsHelpText)(inv.Stdout, c.Spec())
	}
	opts, err := lsOptionsFromParsed(inv, matches)
	if err != nil {
		return err
	}
	primeLSIdentityDB(ctx, inv, &opts)
	targets := matches.Args("file")
	if len(targets) == 0 {
		targets = []string{"."}
	}

	return lsRunTargets(ctx, inv, "ls", targets, &opts, func(value string) string { return value }, false, func(target string, showHeader bool) (string, int, lsRenderResult, error) {
		return c.listPath(ctx, inv, target, &opts, showHeader)
	})
}

func lsOptionSpecs() []OptionSpec {
	return []OptionSpec{
		{Name: "one-per-line", Short: '1', Help: "list one file per line"},
		{Name: "columns", Short: 'C', Help: "list entries by columns", Overrides: []string{"format", "columns", "long", "across", "commas"}},
		{Name: "across", Short: 'x', Help: "list entries by lines instead of by columns", Overrides: []string{"format", "columns", "long", "across", "commas"}},
		{Name: "commas", Short: 'm', Help: "fill width with a comma separated list of entries", Overrides: []string{"format", "columns", "long", "across", "commas"}},
		{Name: "format", Long: "format", ValueName: "WORD", Arity: OptionRequiredValue, Help: "across, commas, horizontal, long, single-column, verbose, vertical", Overrides: []string{"format", "columns", "long", "across", "commas"}},
		{Name: "all", Short: 'a', Long: "all", Help: "do not ignore entries starting with .", Overrides: []string{"all", "almost-all"}},
		{Name: "almost-all", Short: 'A', Long: "almost-all", Help: "do not list implied . and ..", Overrides: []string{"all", "almost-all"}},
		{Name: "ignore-backups", Short: 'B', Long: "ignore-backups", Help: "do not list implied entries ending with ~"},
		{Name: "ignore", Short: 'I', Long: "ignore", ValueName: "PATTERN", Arity: OptionRequiredValue, Repeatable: true, Help: "do not list implied entries matching shell PATTERN"},
		{Name: "hide", Long: "hide", ValueName: "PATTERN", Arity: OptionRequiredValue, Repeatable: true, Help: "do not list implied entries matching shell PATTERN"},
		{Name: "directory", Short: 'd', Long: "directory", Help: "list directories themselves, not their contents"},
		{Name: "classify", Short: 'F', Long: "classify", ValueName: "WHEN", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "append indicator (one of */=>@) to entries", Overrides: []string{"classify", "file-type", "slash", "indicator-style"}},
		{Name: "file-type", Long: "file-type", Help: "append indicator except for executables", Overrides: []string{"classify", "file-type", "slash", "indicator-style"}},
		{Name: "slash", Short: 'p', Help: "append / indicator to directories", Overrides: []string{"classify", "file-type", "slash", "indicator-style"}},
		{Name: "indicator-style", Long: "indicator-style", ValueName: "WORD", Arity: OptionRequiredValue, Help: "none, slash, file-type, classify", Overrides: []string{"classify", "file-type", "slash", "indicator-style"}},
		{Name: "human-readable", Short: 'h', Long: "human-readable", Help: "with -l, print sizes like 1K 234M 2G"},
		{Name: "inode", Short: 'i', Long: "inode", Help: "print the index number of each file"},
		{Name: "kibibytes", Short: 'k', Long: "kibibytes", Help: "default to 1024-byte blocks for block counts"},
		{Name: "long", Short: 'l', Help: "use a long listing format"},
		{Name: "numeric-uid-gid", Short: 'n', Long: "numeric-uid-gid", Help: "like -l, but list numeric user and group IDs"},
		{Name: "literal", Short: 'N', Long: "literal", Help: "print entry names without quoting", Overrides: []string{"quoting-style", "literal", "escape", "quote-name"}},
		{Name: "long-no-group", Short: 'o', Help: "like -l, but do not list group information"},
		{Name: "escape", Short: 'b', Long: "escape", Help: "print C-style escapes for nongraphic characters", Overrides: []string{"quoting-style", "literal", "escape", "quote-name"}},
		{Name: "quote-name", Short: 'Q', Long: "quote-name", Help: "enclose entry names in double quotes", Overrides: []string{"quoting-style", "literal", "escape", "quote-name"}},
		{Name: "quoting-style", Long: "quoting-style", Aliases: []string{"quoting"}, ValueName: "WORD", Arity: OptionRequiredValue, Help: "literal, shell, shell-escape, shell-always, shell-escape-always, c, escape", Overrides: []string{"quoting-style", "literal", "escape", "quote-name"}},
		{Name: "hide-control-chars", Short: 'q', Long: "hide-control-chars", Help: "print ? instead of nongraphic characters", Overrides: []string{"hide-control-chars", "show-control-chars"}},
		{Name: "show-control-chars", Long: "show-control-chars", Help: "show nongraphic characters as-is", Overrides: []string{"hide-control-chars", "show-control-chars"}},
		{Name: "reverse", Short: 'r', Long: "reverse", Help: "reverse order while sorting"},
		{Name: "recursive", Short: 'R', Long: "recursive", Help: "list subdirectories recursively"},
		{Name: "sort-size", Short: 'S', Help: "sort by file size, largest first", Overrides: []string{"sort", "sort-size", "sort-time", "sort-version", "sort-extension", "sort-none"}},
		{Name: "allocation-size", Short: 's', Long: "size", Help: "print the allocated size of each file, in blocks"},
		{Name: "sort-time", Short: 't', Help: "sort by time, newest first", Overrides: []string{"sort", "sort-size", "sort-time", "sort-version", "sort-extension", "sort-none"}},
		{Name: "sort-version", Short: 'v', Help: "natural sort of version numbers within text", Overrides: []string{"sort", "sort-size", "sort-time", "sort-version", "sort-extension", "sort-none"}},
		{Name: "sort-extension", Short: 'X', Help: "sort alphabetically by entry extension", Overrides: []string{"sort", "sort-size", "sort-time", "sort-version", "sort-extension", "sort-none"}},
		{Name: "sort-none", Short: 'U', Help: "do not sort; list entries in directory order", Overrides: []string{"sort", "sort-size", "sort-time", "sort-version", "sort-extension", "sort-none"}},
		{Name: "sort", Long: "sort", ValueName: "WORD", Arity: OptionRequiredValue, Help: "name, none, size, time, version, extension, width", Overrides: []string{"sort", "sort-size", "sort-time", "sort-version", "sort-extension", "sort-none"}},
		{Name: "long-no-owner", Short: 'g', Help: "like -l, but do not list owner information"},
		{Name: "dereference-command-line", Short: 'H', Long: "dereference-command-line", Help: "follow symbolic links listed on the command line", Overrides: []string{"dereference", "dereference-command-line", "dereference-command-line-symlink-to-dir"}},
		{Name: "dereference", Short: 'L', Long: "dereference", Help: "when showing file information for a symbolic link, show information for the referent", Overrides: []string{"dereference", "dereference-command-line", "dereference-command-line-symlink-to-dir"}},
		{Name: "dereference-command-line-symlink-to-dir", Long: "dereference-command-line-symlink-to-dir", Help: "follow each command line symbolic link that points to a directory", Overrides: []string{"dereference", "dereference-command-line", "dereference-command-line-symlink-to-dir"}},
		{Name: "no-group", Short: 'G', Long: "no-group", Help: "in long format, do not print group names"},
		{Name: "author", Long: "author", Help: "with -l, print the author of each file"},
		{Name: "si", Long: "si", Help: "like -h, but use powers of 1000 not 1024"},
		{Name: "block-size", Long: "block-size", ValueName: "SIZE", Arity: OptionRequiredValue, Help: "scale sizes by SIZE before printing them"},
		{Name: "time-style", Long: "time-style", ValueName: "STYLE", Arity: OptionRequiredValue, Help: "full-iso, long-iso, iso, locale, or +FORMAT"},
		{Name: "full-time", Long: "full-time", Help: "like -l --time-style=full-iso"},
		{Name: "hyperlink", Long: "hyperlink", ValueName: "WHEN", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "hyperlink file names; WHEN can be 'always', 'auto', or 'never'"},
		{Name: "dired", Long: "dired", Help: "generate output designed for Emacs' dired mode"},
		{Name: "group-directories-first", Long: "group-directories-first", Help: "group directories before files"},
		{Name: "tabsize", Short: 'T', Long: "tabsize", ValueName: "COLS", Arity: OptionRequiredValue, Help: "assume tab stops every COLS instead of 8"},
		{Name: "width", Short: 'w', Long: "width", ValueName: "COLS", Arity: OptionRequiredValue, Help: "set output width to COLS"},
		{Name: "zero", Long: "zero", Help: "end each output entry with NUL, not newline"},
		{Name: "unsorted-all", Short: 'f', Help: "list all entries in directory order"},
		{Name: "color", Long: "color", ValueName: "WHEN", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "colorize the output; WHEN can be 'always', 'auto', or 'never'"},
		{Name: "help", Long: "help", Help: "show this help text"},
	}
}

func lsOptionsFromParsed(inv *Invocation, matches *ParsedCommand) (lsOptions, error) {
	format, longFormat, err := parseLSFormat(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	sortMode, err := parseLSSortMode(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	indicatorMode, err := parseLSIndicatorMode(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	quotingMode, err := parseLSQuotingMode(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	width, err := parseLSWidth(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	tabSize, err := parseLSTabSize(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	blockSize, err := parseLSBlockSize(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	var timeStyle string
	if longFormat {
		timeStyle, err = parseLSTimeStyle(inv, matches)
		if err != nil {
			return lsOptions{}, err
		}
	}
	dereference := parseLSDereferenceMode(matches, longFormat, indicatorMode)
	hyperlinkMode, err := parseLSHyperlinkMode(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	colorMode, err := parseLSColorMode(inv, matches)
	if err != nil {
		return lsOptions{}, err
	}
	diredRequested, diredActive := parseLSDiredMode(matches)
	if diredActive && matches.Has("zero") {
		return lsOptions{}, exitf(inv, 2, "ls: options '--dired' and '--zero' are incompatible")
	}
	if diredRequested && diredActive {
		longFormat = true
		hyperlinkMode = lsColorNever
	}
	showAll, showAlmostAll := parseLSVisibility(matches)
	if matches.Has("zero") {
		colorMode = lsColorNever
		hyperlinkMode = lsColorNever
		quotingMode = lsQuoteLiteral
	}
	return lsOptions{
		showAll:               showAll,
		showAlmostAll:         showAlmostAll,
		hidePatterns:          matches.Values("hide"),
		ignorePatterns:        matches.Values("ignore"),
		ignoreBackups:         matches.Has("ignore-backups"),
		longFormat:            longFormat,
		humanReadable:         matches.Has("human-readable"),
		si:                    matches.Has("si"),
		kibibytes:             matches.Has("kibibytes"),
		blockSize:             blockSize,
		recursive:             matches.Has("recursive"),
		reverse:               matches.Has("reverse"),
		sortMode:              sortMode,
		groupDirectoriesFirst: matches.Has("group-directories-first"),
		directoryOnly:         matches.Has("directory"),
		format:                format,
		width:                 width,
		tabSize:               tabSize,
		zero:                  matches.Has("zero"),
		colorMode:             colorMode,
		hyperlinkMode:         hyperlinkMode,
		indicatorMode:         indicatorMode,
		quotingMode:           quotingMode,
		hideControlChars:      !matches.Has("show-control-chars") && matches.Has("hide-control-chars"),
		showInode:             matches.Has("inode"),
		showAllocSize:         matches.Has("allocation-size"),
		showAuthor:            matches.Has("author"),
		showGroup:             !matches.Has("long-no-group") && !matches.Has("no-group"),
		showOwner:             !matches.Has("long-no-owner"),
		numericIDs:            matches.Has("numeric-uid-gid"),
		timeStyle:             timeStyle,
		dereference:           dereference,
		dired:                 diredActive,
	}, nil
}

func renderStaticHelp(text string) func(io.Writer, CommandSpec) error {
	return func(w io.Writer, _ CommandSpec) error {
		_, err := io.WriteString(w, text)
		return err
	}
}

func primeLSIdentityDB(ctx context.Context, inv *Invocation, opts *lsOptions) {
	if opts == nil || opts.identityDB != nil {
		return
	}
	if !opts.longFormat || opts.numericIDs {
		return
	}
	if !opts.showOwner && !opts.showGroup && !opts.showAuthor {
		return
	}
	loader := opts.identityDBLoader
	if loader == nil {
		loader = loadPermissionIdentityDB
	}
	opts.identityDB = loader(ctx, inv)
}

func renderStaticVersion(text string) func(io.Writer, CommandSpec) error {
	return func(w io.Writer, _ CommandSpec) error {
		_, err := io.WriteString(w, text)
		return err
	}
}

func (c *LS) listPath(ctx context.Context, inv *Invocation, target string, opts *lsOptions, showHeader bool) (output string, status int, rendered lsRenderResult, err error) {
	info, abs, exists, err := lsStatMaybeForTarget(ctx, inv, target, opts)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	if !exists {
		_, _ = fmt.Fprintf(inv.Stderr, "ls: %s: No such file or directory\n", target)
		return "", 2, lsRenderResult{}, nil
	}

	if opts.directoryOnly || !info.IsDir() { //nolint:nilaway // info is non-nil when exists is true
		return c.renderPathEntry(ctx, inv, target, abs, info, opts)
	}

	entries, err := readDir(ctx, inv, target)
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

	entryInfos, err := c.loadLSEntries(ctx, inv, abs, filtered, opts)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	sortLSEntries(entryInfos, opts)
	statusCode := 0
	for _, entry := range entryInfos {
		if entry.unknownShortMetadata {
			statusCode = 1
			break
		}
	}

	var out strings.Builder
	result := lsRenderResult{}
	if opts.recursive {
		if _, err := lsRememberListedDirectory(ctx, inv, abs, info, opts); err != nil {
			return "", 0, lsRenderResult{}, err
		}
	}
	if opts.recursive || showHeader {
		header := lsQuoteName(target, opts.quotingMode, opts.hideControlChars)
		if lsShouldUseColor(inv, opts.hyperlinkMode) {
			header = lsHyperlink(header, lsHeaderHyperlinkTarget(ctx, inv, abs, info, opts))
		}
		if opts.dired {
			out.WriteString("  ")
			start := out.Len()
			out.WriteString(header)
			result.subdired = append(result.subdired, lsByteRange{start: start, end: out.Len()})
			out.WriteString(":\n")
		} else {
			out.WriteString(header)
			out.WriteString(":\n")
		}
	}
	if lsShouldShowDirectoryTotal(opts) {
		if opts.dired {
			out.WriteString("  ")
		}
		out.WriteString("total ")
		out.WriteString(strconv.FormatInt(lsDirectoryTotal(entryInfos, opts), 10))
		out.WriteByte('\n')
	}
	entryRendered, err := lsRenderEntries(ctx, inv, abs, entryInfos, opts, func(value string) string { return value }, false)
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	offset := out.Len()
	out.WriteString(entryRendered.text)
	for _, entry := range entryRendered.dired {
		result.dired = append(result.dired, lsByteRange{start: offset + entry.start, end: offset + entry.end})
	}
	for _, entry := range entryRendered.subdired {
		result.subdired = append(result.subdired, lsByteRange{start: offset + entry.start, end: offset + entry.end})
	}

	if opts.recursive {
		subdirs := make([]lsEntry, 0)
		recursiveStatus := statusCode
		for _, entry := range entryInfos {
			if entry.name == "." || entry.name == ".." || !entry.info.IsDir() {
				continue
			}
			subdirs = append(subdirs, entry)
		}
		for _, dir := range subdirs {
			subTarget := target
			subAbs := path.Join(abs, dir.name)
			switch subTarget {
			case ".":
				subTarget = "./" + dir.name
			case "/":
				subTarget = "/" + dir.name
			default:
				subTarget = lsJoinRecursiveTarget(subTarget, dir.name)
			}
			alreadyListed, err := lsHasListedDirectory(ctx, inv, subAbs, dir.info, opts)
			if err != nil {
				return "", 0, lsRenderResult{}, err
			}
			if alreadyListed {
				_, _ = fmt.Fprintf(inv.Stderr, "ls: %s: not listing already-listed directory\n", subTarget)
				recursiveStatus = max(recursiveStatus, 2)
				continue
			}
			out.WriteByte('\n')
			subOutput, status, subRendered, err := c.listPath(ctx, inv, subTarget, opts, false)
			if err != nil {
				return "", 0, lsRenderResult{}, err
			}
			offset := out.Len()
			out.WriteString(subOutput)
			for _, entry := range subRendered.dired {
				result.dired = append(result.dired, lsByteRange{start: offset + entry.start, end: offset + entry.end})
			}
			for _, entry := range subRendered.subdired {
				result.subdired = append(result.subdired, lsByteRange{start: offset + entry.start, end: offset + entry.end})
			}
			if status != 0 {
				return out.String(), status, result, nil
			}
		}
		return out.String(), recursiveStatus, result, nil
	}

	return out.String(), statusCode, result, nil
}

func lsStatMaybeForTarget(ctx context.Context, inv *Invocation, target string, opts *lsOptions) (info stdfs.FileInfo, abs string, exists bool, err error) {
	if target != "/" && strings.HasSuffix(target, "/") {
		return statMaybe(ctx, inv, target)
	}
	switch {
	case opts == nil || opts.dereference == lsDerefAll || opts.dereference == lsDerefArgs:
		linfo, lAbs, lExists, lErr := lstatMaybe(ctx, inv, target)
		if lErr != nil || !lExists {
			return linfo, lAbs, lExists, lErr
		}
		if linfo.Mode()&stdfs.ModeSymlink == 0 {
			return linfo, lAbs, true, nil
		}
		targetInfo, _, resolveErr := lsResolveSymlinkInfo(ctx, inv, lAbs)
		if resolveErr != nil {
			return nil, lAbs, false, resolveErr
		}
		return targetInfo, lAbs, true, nil
	case opts.dereference == lsDerefDirArgs:
		linfo, lAbs, lExists, lErr := lstatMaybe(ctx, inv, target)
		if lErr != nil || !lExists {
			return linfo, lAbs, lExists, lErr
		}
		if linfo.Mode()&stdfs.ModeSymlink == 0 {
			return linfo, lAbs, true, nil
		}
		targetInfo, _, resolveErr := lsResolveSymlinkInfo(ctx, inv, lAbs)
		if resolveErr == nil && targetInfo.IsDir() {
			return targetInfo, lAbs, true, nil
		}
		return linfo, lAbs, true, nil
	default:
		return lstatMaybe(ctx, inv, target)
	}
}

func (c *LS) renderPathEntry(ctx context.Context, inv *Invocation, target, abs string, info stdfs.FileInfo, opts *lsOptions) (output string, status int, rendered lsRenderResult, err error) {
	name, ranges, err := lsDecoratedName(ctx, inv, target, abs, info, opts, func(value string) string { return value })
	if err != nil {
		return "", 0, lsRenderResult{}, err
	}
	if opts.longFormat {
		line, dired := formatLSLongLine(name, info, opts, ranges, inv.Now())
		if opts.dired {
			line = "  " + line
			for i := range dired {
				dired[i].start += 2
				dired[i].end += 2
			}
		}
		return line, 0, lsRenderResult{text: line, dired: dired}, nil
	}
	line := formatLSShortPrefix(info, opts, false) + name + lsTerminator(opts)
	return line, 0, lsRenderResult{text: line}, nil
}

func lsShouldIncludeEntry(name string, opts *lsOptions) bool {
	if opts.showAll {
		return true
	}
	if opts.showAlmostAll && (name == "." || name == "..") {
		return false
	}
	if strings.HasPrefix(name, ".") && !opts.showAlmostAll {
		return false
	}
	if opts.ignoreBackups && strings.HasSuffix(name, "~") {
		return false
	}
	for _, pattern := range opts.ignorePatterns {
		if matched, _ := path.Match(pattern, name); matched {
			return false
		}
	}
	for _, pattern := range opts.hidePatterns {
		if matched, _ := path.Match(pattern, name); matched {
			return false
		}
	}
	return true
}

func lsRenderEntries(ctx context.Context, inv *Invocation, dirAbs string, entries []lsEntry, opts *lsOptions, quote func(string) string, forceColumns bool) (lsRenderResult, error) {
	names := make([]string, 0, len(entries))
	result := lsRenderResult{}
	offset := 0
	for _, entry := range entries {
		name, ranges, err := lsDecoratedName(ctx, inv, entry.name, path.Join(dirAbs, entry.name), entry.info, opts, quote)
		if err != nil {
			return lsRenderResult{}, err
		}
		if opts.longFormat {
			line, dired := formatLSLongLine(name, entry.info, opts, ranges, inv.Now())
			if opts.dired {
				line = "  " + line
				for i := range dired {
					dired[i].start += 2
					dired[i].end += 2
				}
			}
			names = append(names, line)
			for _, entry := range dired {
				result.dired = append(result.dired, lsByteRange{start: offset + entry.start, end: offset + entry.end})
			}
			offset += len(line)
			continue
		}
		names = append(names, formatLSShortPrefix(entry.info, opts, entry.unknownShortMetadata)+name)
	}
	if opts.longFormat {
		result.text = strings.Join(names, "")
		return result, nil
	}
	result.text = lsRenderNames(names, opts, forceColumns)
	return result, nil
}

func lsRenderPathArgs(ctx context.Context, inv *Invocation, args []lsPathArg, opts *lsOptions, quote func(string) string, forceColumns bool) (lsRenderResult, error) {
	names := make([]string, 0, len(args))
	result := lsRenderResult{}
	offset := 0
	for _, arg := range args {
		name, ranges, err := lsDecoratedName(ctx, inv, arg.name, arg.abs, arg.info, opts, quote)
		if err != nil {
			return lsRenderResult{}, err
		}
		if opts.longFormat {
			line, dired := formatLSLongLine(name, arg.info, opts, ranges, inv.Now())
			if opts.dired {
				line = "  " + line
				for i := range dired {
					dired[i].start += 2
					dired[i].end += 2
				}
			}
			names = append(names, line)
			for _, entry := range dired {
				result.dired = append(result.dired, lsByteRange{start: offset + entry.start, end: offset + entry.end})
			}
			offset += len(line)
			continue
		}
		names = append(names, formatLSShortPrefix(arg.info, opts, false)+name)
	}
	if opts.longFormat {
		result.text = strings.Join(names, "")
		return result, nil
	}
	result.text = lsRenderNames(names, opts, forceColumns)
	return result, nil
}

func lsRunTargets(ctx context.Context, inv *Invocation, commandName string, targets []string, opts *lsOptions, quote func(string) string, forceColumns bool, listPath func(string, bool) (string, int, lsRenderResult, error)) error {
	var stdout strings.Builder
	exitCode := 0
	var diredPositions []lsByteRange
	var subdiredPositions []lsByteRange
	fileArgs := make([]lsPathArg, 0, len(targets))
	dirTargets := make([]string, 0, len(targets))

	for _, target := range targets {
		info, abs, exists, err := lsStatMaybeForTarget(ctx, inv, target, opts)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "%s: %s\n", commandName, err)
			exitCode = max(exitCode, 2)
			continue
		}
		if !exists {
			_, _ = fmt.Fprintf(inv.Stderr, "%s: %s: No such file or directory\n", commandName, target)
			exitCode = max(exitCode, 2)
			continue
		}
		if opts.directoryOnly || !info.IsDir() {
			groupAsDir := info.IsDir()
			if opts.groupDirectoriesFirst && opts.sortMode != lsSortNone {
				groupAsDir = lsGroupsAsDirectory(ctx, inv, abs, info)
			}
			fileArgs = append(fileArgs, lsPathArg{
				name:       target,
				abs:        abs,
				info:       info,
				groupAsDir: groupAsDir,
			})
			continue
		}
		dirTargets = append(dirTargets, target)
	}

	showHeaders := len(targets) > 1

	renderDirs := func() error {
		for i, target := range dirTargets {
			if opts.recursive {
				opts.listedDirs = make(map[lsListedDirKey]struct{})
			}
			if i > 0 && stdout.Len() > 0 && !strings.HasSuffix(stdout.String(), "\n\n") {
				stdout.WriteByte('\n')
			}
			output, status, rendered, err := listPath(target, showHeaders)
			if err != nil {
				return err
			}
			offset := stdout.Len()
			stdout.WriteString(output)
			if opts.dired {
				for _, entry := range rendered.dired {
					diredPositions = append(diredPositions, lsByteRange{start: offset + entry.start, end: offset + entry.end})
				}
				for _, entry := range rendered.subdired {
					subdiredPositions = append(subdiredPositions, lsByteRange{start: offset + entry.start, end: offset + entry.end})
				}
			}
			if status > exitCode {
				exitCode = status
			}
		}
		return nil
	}

	if len(fileArgs) > 0 {
		sortLSPathArgs(fileArgs, opts)
		rendered, err := lsRenderPathArgs(ctx, inv, fileArgs, opts, quote, forceColumns)
		if err != nil {
			return err
		}
		offset := stdout.Len()
		stdout.WriteString(rendered.text)
		for _, entry := range rendered.dired {
			diredPositions = append(diredPositions, lsByteRange{start: offset + entry.start, end: offset + entry.end})
		}
		if len(dirTargets) > 0 && !strings.HasSuffix(stdout.String(), "\n\n") {
			stdout.WriteByte('\n')
		}
	}

	if err := renderDirs(); err != nil {
		return err
	}

	if opts.dired {
		appendLSDiredFooter(&stdout, diredPositions, subdiredPositions, opts)
	}
	if _, err := fmt.Fprint(inv.Stdout, stdout.String()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func lsRenderNames(names []string, opts *lsOptions, forceColumns bool) string {
	if len(names) == 0 {
		return ""
	}
	if opts.zero {
		return strings.Join(names, "\x00") + "\x00"
	}
	if opts.format == lsFormatCommas {
		return lsRenderCommaNames(names, opts.width) + "\n"
	}
	if forceColumns || opts.format == lsFormatColumns || opts.format == lsFormatAcross {
		return lsRenderGrid(names, opts.format == lsFormatAcross, opts.width, opts.tabSize) + "\n"
	}
	return strings.Join(names, "\n") + "\n"
}

func appendLSDiredFooter(out *strings.Builder, diredPositions, subdiredPositions []lsByteRange, opts *lsOptions) {
	if len(diredPositions) > 0 {
		out.WriteString("//DIRED//")
		for _, entry := range diredPositions {
			fmt.Fprintf(out, " %d %d", entry.start, entry.end)
		}
		out.WriteByte('\n')
	}
	if len(subdiredPositions) > 0 {
		out.WriteString("//SUBDIRED//")
		for _, entry := range subdiredPositions {
			fmt.Fprintf(out, " %d %d", entry.start, entry.end)
		}
		out.WriteByte('\n')
	}
	out.WriteString("//DIRED-OPTIONS// --quoting-style=")
	out.WriteString(lsDiredQuotingStyle(opts.quotingMode))
	out.WriteByte('\n')
}

func lsDiredQuotingStyle(mode lsQuotingMode) string {
	switch mode {
	case lsQuoteEscape:
		return "escape"
	case lsQuoteC:
		return "c"
	case lsQuoteShell:
		return "shell"
	case lsQuoteShellAlways:
		return "shell"
	default:
		return "literal"
	}
}

func lsRenderGrid(names []string, across bool, width, tabSize int) string {
	if tabSize <= 0 {
		tabSize = 8
	}
	displayWidths := make([]int, len(names))
	maxWidth := 0
	for i, name := range names {
		displayWidths[i] = lsVisibleWidthTabAware(name, tabSize)
		if displayWidths[i] > maxWidth {
			maxWidth = displayWidths[i]
		}
	}
	colWidth := maxWidth + 2
	if width > 0 {
		for _, name := range names {
			if strings.Contains(name, "\x1b") || strings.ContainsAny(name, `'"`) {
				colWidth++
				break
			}
		}
	}
	if colWidth <= 0 {
		colWidth = 1
	}
	cols := len(names)
	if width > 0 {
		cols = max(1, (width+2)/colWidth)
		cols = min(cols, len(names))
	}
	rows := (len(names) + cols - 1) / cols

	var lines []string
	for row := range rows {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			var index int
			if across {
				index = row*cols + col
			} else {
				index = col*rows + row
			}
			if index >= len(names) {
				continue
			}
			name := names[index]
			line.WriteString(name)
			if col == cols-1 {
				continue
			}
			padding := colWidth - displayWidths[index]
			padding = max(2, padding)
			line.WriteString(strings.Repeat(" ", padding))
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}

func lsVisibleWidth(value string) int {
	width := 0
	inEscape := false
	for i := 0; i < len(value); i++ {
		switch {
		case !inEscape && value[i] == '\x1b':
			inEscape = true
		case inEscape && value[i] == 'm':
			inEscape = false
		case !inEscape:
			width++
		}
	}
	return width
}

func lsVisibleWidthTabAware(value string, tabSize int) int {
	if tabSize <= 0 {
		tabSize = 8
	}
	width := 0
	inEscape := false
	for i := 0; i < len(value); i++ {
		switch {
		case !inEscape && value[i] == '\x1b':
			inEscape = true
		case inEscape && value[i] == 'm':
			inEscape = false
		case !inEscape && value[i] == '\t':
			width += tabSize - (width % tabSize)
		case !inEscape:
			width++
		}
	}
	return width
}

func lsTerminator(opts *lsOptions) string {
	if opts.zero {
		return "\x00"
	}
	return "\n"
}

func parseLSColorMode(inv *Invocation, matches *ParsedCommand) (lsColorMode, error) {
	colorIndex, unsortedAllIndex := 0, 0
	for idx, option := range matches.OptionOrder() {
		switch option {
		case "color":
			colorIndex = idx + 1
		case "unsorted-all":
			unsortedAllIndex = idx + 1
		}
	}
	if !matches.Has("color") {
		if unsortedAllIndex > 0 {
			return lsColorNever, nil
		}
		return lsColorAuto, nil
	}
	switch value := matches.Value("color"); value {
	case "", "always", "yes", "force":
		return lsColorAlways, nil
	case "auto", "tty", "if-tty":
		if colorIndex == 0 && unsortedAllIndex > 0 {
			return lsColorNever, nil
		}
		return lsColorAuto, nil
	case "never", "no", "none":
		return lsColorNever, nil
	default:
		return lsColorNever, exitf(inv, 1, "ls: invalid argument %s for '--color'", quoteGNUOperand(value))
	}
}

func parseLSHyperlinkMode(inv *Invocation, matches *ParsedCommand) (lsColorMode, error) {
	if !matches.Has("hyperlink") {
		return lsColorNever, nil
	}
	switch value := matches.Value("hyperlink"); value {
	case "", "always", "yes", "force":
		return lsColorAlways, nil
	case "auto", "tty", "if-tty":
		return lsColorAuto, nil
	case "never", "no", "none":
		return lsColorNever, nil
	default:
		return lsColorNever, exitf(inv, 1, "ls: invalid argument %s for '--hyperlink'", quoteGNUOperand(value))
	}
}

func parseLSDiredMode(matches *ParsedCommand) (requested, active bool) {
	for _, option := range matches.OptionOrder() {
		switch option {
		case "dired":
			requested = true
			active = true
		case "hyperlink", "one-per-line", "columns", "across", "commas", "format":
			active = false
		}
	}
	return requested, active
}

func parseLSVisibility(matches *ParsedCommand) (showAll, showAlmostAll bool) {
	for _, option := range matches.OptionOrder() {
		switch option {
		case "all", "unsorted-all":
			showAll = true
			showAlmostAll = false
		case "almost-all":
			showAll = false
			showAlmostAll = true
		}
	}
	return showAll, showAlmostAll
}

func lsRenderCommaNames(names []string, width int) string {
	if len(names) == 0 {
		return ""
	}
	if width <= 0 {
		return strings.Join(names, ", ")
	}

	lines := make([]string, 0, len(names))
	current := names[0]
	currentWidth := lsVisibleWidth(current)
	for _, name := range names[1:] {
		segment := ", " + name
		if currentWidth+lsVisibleWidth(segment) > width {
			lines = append(lines, current+",")
			current = name
			currentWidth = lsVisibleWidth(name)
			continue
		}
		current += segment
		currentWidth += lsVisibleWidth(segment)
	}
	lines = append(lines, current)
	return strings.Join(lines, "\n")
}

func parseLSFormat(inv *Invocation, matches *ParsedCommand) (lsFormatMode, bool, error) {
	format := lsFormatOnePerLine
	longFormat := false
	for _, option := range matches.OptionOrder() {
		switch option {
		case "one-per-line":
			format = lsFormatOnePerLine
			longFormat = false
		case "columns":
			format = lsFormatColumns
			longFormat = false
		case "across":
			format = lsFormatAcross
			longFormat = false
		case "commas":
			format = lsFormatCommas
			longFormat = false
		case "long-no-group", "long-no-owner":
			longFormat = true
		case "long":
			longFormat = true
		case "format":
			switch matches.Value("format") {
			case "long", "verbose":
				longFormat = true
			case "single-column":
				format = lsFormatOnePerLine
				longFormat = false
			case "columns", "vertical":
				format = lsFormatColumns
				longFormat = false
			case "across", "horizontal":
				format = lsFormatAcross
				longFormat = false
			case "commas":
				format = lsFormatCommas
				longFormat = false
			default:
				return lsFormatOnePerLine, false, exitf(inv, 1, "ls: invalid argument %s for '--format'", quoteGNUOperand(matches.Value("format")))
			}
		}
	}
	return format, longFormat, nil
}

func parseLSSortMode(inv *Invocation, matches *ParsedCommand) (lsSortMode, error) {
	if matches.Has("unsorted-all") {
		return lsSortNone, nil
	}
	sortMode := lsSortName
	for _, option := range matches.OptionOrder() {
		switch option {
		case "sort-size":
			sortMode = lsSortSize
		case "sort-time":
			sortMode = lsSortTime
		case "sort-version":
			sortMode = lsSortVersion
		case "sort-extension":
			sortMode = lsSortExtension
		case "sort-none":
			sortMode = lsSortNone
		case "sort":
			switch matches.Value("sort") {
			case "name":
				sortMode = lsSortName
			case "none":
				sortMode = lsSortNone
			case "time":
				sortMode = lsSortTime
			case "size":
				sortMode = lsSortSize
			case "version":
				sortMode = lsSortVersion
			case "extension":
				sortMode = lsSortExtension
			case "width":
				sortMode = lsSortWidth
			default:
				return lsSortName, exitf(inv, 1, "ls: invalid argument %s for '--sort'", quoteGNUOperand(matches.Value("sort")))
			}
		}
	}
	return sortMode, nil
}

func parseLSIndicatorMode(inv *Invocation, matches *ParsedCommand) (lsIndicatorMode, error) {
	mode := lsIndicatorNone
	for _, option := range matches.OptionOrder() {
		switch option {
		case "classify":
			value := matches.Value("classify")
			switch value {
			case "", "always", "yes", "force":
				mode = lsIndicatorClassify
			case "auto", "tty", "if-tty":
				if lsTerminalWriter(inv.Stdout) {
					mode = lsIndicatorClassify
				} else {
					mode = lsIndicatorNone
				}
			case "never", "no", "none":
				mode = lsIndicatorNone
			default:
				return lsIndicatorNone, exitf(inv, 1, "ls: invalid argument %s for '--classify'", quoteGNUOperand(value))
			}
		case "file-type":
			mode = lsIndicatorFileType
		case "slash":
			mode = lsIndicatorSlash
		case "indicator-style":
			switch matches.Value("indicator-style") {
			case "none":
				mode = lsIndicatorNone
			case "slash":
				mode = lsIndicatorSlash
			case "file-type":
				mode = lsIndicatorFileType
			case "classify":
				mode = lsIndicatorClassify
			default:
				return lsIndicatorNone, exitf(inv, 1, "ls: invalid argument %s for '--indicator-style'", quoteGNUOperand(matches.Value("indicator-style")))
			}
		}
	}
	return mode, nil
}

func parseLSQuotingMode(inv *Invocation, matches *ParsedCommand) (lsQuotingMode, error) {
	mode := lsQuoteLiteral
	for _, option := range matches.OptionOrder() {
		switch option {
		case "literal":
			mode = lsQuoteLiteral
		case "escape":
			mode = lsQuoteEscape
		case "quote-name":
			mode = lsQuoteC
		case "quoting-style":
			switch matches.Value("quoting-style") {
			case "literal":
				mode = lsQuoteLiteral
			case "escape":
				mode = lsQuoteEscape
			case "c", "c-maybe", "clocale":
				mode = lsQuoteC
			case "shell", "shell-escape":
				mode = lsQuoteShell
			case "shell-always", "shell-escape-always":
				mode = lsQuoteShellAlways
			default:
				return lsQuoteLiteral, exitf(inv, 1, "ls: invalid argument %s for '--quoting-style'", quoteGNUOperand(matches.Value("quoting-style")))
			}
		}
	}
	return mode, nil
}

func parseLSWidth(inv *Invocation, matches *ParsedCommand) (int, error) {
	if !matches.Has("width") {
		return 80, nil
	}
	invalidWidth := func() error {
		return exitf(inv, 2, "ls: invalid line width: %s\nTry 'ls --help' for more information.", quoteGNUOperand(matches.Value("width")))
	}
	raw := matches.Value("width")
	if strings.HasPrefix(raw, "-") {
		return 0, invalidWidth()
	}
	width, err := strconv.ParseUint(raw, 0, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && errors.Is(numErr.Err, strconv.ErrRange) {
			return int(^uint(0) >> 1), nil
		}
		return 0, invalidWidth()
	}
	maxInt := uint64(^uint(0) >> 1)
	if width > maxInt {
		return int(maxInt), nil
	}
	return int(width), nil
}

func parseLSTabSize(inv *Invocation, matches *ParsedCommand) (int, error) {
	if !matches.Has("tabsize") {
		return 8, nil
	}
	value := matches.Value("tabsize")
	tabSize, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return 0, exitf(inv, 2, "ls: invalid tab size: %s\nTry 'ls --help' for more information.", quoteGNUOperand(value))
	}
	maxInt := uint64(^uint(0) >> 1)
	if tabSize > maxInt {
		return int(maxInt), nil
	}
	return int(tabSize), nil
}

func lsJoinRecursiveTarget(base, name string) string {
	switch {
	case base == ".":
		return "./" + name
	case base == "/":
		return "/" + name
	case strings.HasPrefix(base, "./"):
		return base + "/" + name
	default:
		return path.Join(base, name)
	}
}

func parseLSBlockSize(inv *Invocation, matches *ParsedCommand) (int64, error) {
	if matches.Has("block-size") {
		return parseLSBlockSizeValue(inv, matches.Value("block-size"))
	}
	if matches.Has("human-readable") {
		return 1, nil
	}
	if matches.Has("si") {
		return 1, nil
	}
	if matches.Has("kibibytes") {
		return 1024, nil
	}
	return 1, nil
}

func parseLSBlockSizeValue(inv *Invocation, value string) (int64, error) {
	switch value {
	case "human-readable", "si":
		return 1, nil
	}
	if value == "" || value == "0" {
		return 0, exitf(inv, 1, "ls: invalid --block-size argument %s", quoteGNUOperand(value))
	}
	multiplier := int64(1)
	switch last := value[len(value)-1]; last {
	case 'K', 'k':
		multiplier = 1024
		value = value[:len(value)-1]
	case 'M', 'm':
		multiplier = 1024 * 1024
		value = value[:len(value)-1]
	case 'G', 'g':
		multiplier = 1024 * 1024 * 1024
		value = value[:len(value)-1]
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, exitf(inv, 1, "ls: invalid --block-size argument %s", quoteGNUOperand(value))
	}
	return n * multiplier, nil
}

func parseLSTimeStyle(inv *Invocation, matches *ParsedCommand) (string, error) {
	style := strings.TrimSpace(inv.Env["TIME_STYLE"])
	for _, option := range matches.OptionOrder() {
		switch option {
		case "time-style":
			style = matches.Value("time-style")
		case "full-time":
			style = "full-iso"
		}
	}
	switch style {
	case "", "full-iso", "long-iso", "iso", "locale":
		return style, nil
	default:
		if strings.HasPrefix(style, "+") {
			return style, nil
		}
		return "", exitf(inv, 2, "ls: invalid argument %s for 'time style'\nValid arguments are:\n  - [posix-]full-iso\n  - [posix-]long-iso\n  - [posix-]iso\n  - [posix-]locale\n  - +FORMAT (e.g., +%%H:%%M) for a 'date'-style format\nTry 'ls --help' for more information.", quoteGNUOperand(style))
	}
}

func parseLSDereferenceMode(matches *ParsedCommand, longFormat bool, indicator lsIndicatorMode) lsDereferenceMode {
	if matches.Has("dereference") {
		return lsDerefAll
	}
	if matches.Has("dereference-command-line") {
		return lsDerefArgs
	}
	if matches.Has("dereference-command-line-symlink-to-dir") {
		return lsDerefDirArgs
	}
	if matches.Has("directory") || indicator == lsIndicatorClassify || longFormat {
		return lsDerefNone
	}
	return lsDerefDirArgs
}

func lsDecoratedName(ctx context.Context, inv *Invocation, rawName, abs string, info stdfs.FileInfo, opts *lsOptions, quote func(string) string) (string, []lsByteRange, error) {
	display, diredRanges, err := lsQuotedNameWithDired(ctx, inv, rawName, abs, info, opts, quote)
	if err != nil {
		return "", nil, err
	}
	leadingSpace := opts.longFormat && opts.quotingMode == lsQuoteShell && !lsNeedsShellQuoting(rawName)
	if leadingSpace {
		for i := range diredRanges {
			diredRanges[i].start++
			diredRanges[i].end++
		}
	}
	suffix, linfo, err := lsSuffixAndInfo(ctx, inv, abs, info, rawName, opts)
	if err != nil {
		return "", nil, err
	}
	display += suffix
	if lsShouldUseColor(inv, opts.hyperlinkMode) {
		display = lsHyperlink(display, abs)
	}
	if !lsShouldUseColor(inv, opts.colorMode) {
		if leadingSpace {
			display = " " + display
		}
		return display, diredRanges, nil
	}
	code, err := lsColorCode(ctx, inv, abs, info, linfo)
	if err != nil {
		return "", nil, err
	}
	if code == "" {
		if leadingSpace {
			display = " " + display
		}
		return display, diredRanges, nil
	}
	if leadingSpace {
		return " " + "\x1b[0m\x1b[" + code + "m" + display + "\x1b[0m", diredRanges, nil
	}
	return "\x1b[0m\x1b[" + code + "m" + display + "\x1b[0m", diredRanges, nil
}

func lsHyperlink(label, target string) string {
	return "\x1b]8;;" + lsFileURI(target) + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

func lsFileURI(target string) string {
	var b strings.Builder
	b.Grow(len(target) + len("file://"))
	b.WriteString("file://")
	for i := 0; i < len(target); i++ {
		ch := target[i]
		if ch == '/' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '.' || ch == '_' || ch == '~' {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('%')
		b.WriteByte("0123456789abcdef"[ch>>4])
		b.WriteByte("0123456789abcdef"[ch&0x0f])
	}
	return b.String()
}

func lsHasListedDirectory(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, opts *lsOptions) (bool, error) {
	key, ok, err := lsListedDirectoryKey(ctx, inv, abs, info, opts)
	if err != nil || !ok || opts == nil || opts.listedDirs == nil {
		return false, err
	}
	_, seen := opts.listedDirs[key]
	return seen, nil
}

func lsRememberListedDirectory(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, opts *lsOptions) (bool, error) {
	key, ok, err := lsListedDirectoryKey(ctx, inv, abs, info, opts)
	if err != nil || !ok {
		return false, err
	}
	if opts.listedDirs == nil {
		opts.listedDirs = make(map[lsListedDirKey]struct{})
	}
	_, seen := opts.listedDirs[key]
	if !seen {
		opts.listedDirs[key] = struct{}{}
	}
	return seen, nil
}

func lsListedDirectoryKey(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, opts *lsOptions) (lsListedDirKey, bool, error) {
	if opts == nil || info == nil || !info.IsDir() {
		return lsListedDirKey{}, false, nil
	}
	keyPath := path.Clean(abs)
	keyInfo := info
	linfo, _, err := lstatPath(ctx, inv, abs)
	if err != nil {
		return lsListedDirKey{}, false, err
	}
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		resolvedInfo, resolvedAbs, resolveErr := lsResolveSymlinkInfo(ctx, inv, abs)
		if resolveErr == nil && resolvedInfo.IsDir() {
			keyInfo = resolvedInfo
			keyPath = resolvedAbs
		}
	}
	if dev, ino, ok := testDeviceAndInode(keyInfo); ok {
		return lsListedDirKey{dev: dev, ino: ino}, true, nil
	}
	return lsListedDirKey{path: keyPath}, true, nil
}

func lsHeaderHyperlinkTarget(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, opts *lsOptions) string {
	if opts == nil || info == nil || !info.IsDir() || opts.dereference == lsDerefNone {
		return abs
	}
	linfo, _, err := lstatPath(ctx, inv, abs)
	if err != nil || linfo.Mode()&stdfs.ModeSymlink == 0 {
		return abs
	}
	_, resolvedAbs, err := lsResolveSymlinkInfo(ctx, inv, abs)
	if err != nil {
		return abs
	}
	return resolvedAbs
}

func lsQuotedNameWithDired(ctx context.Context, inv *Invocation, rawName, abs string, _ stdfs.FileInfo, opts *lsOptions, quote func(string) string) (string, []lsByteRange, error) {
	quoted := rawName
	if opts.quotingMode == lsQuoteLiteral && !opts.hideControlChars {
		quoted = quote(rawName)
	}
	quoted = lsQuoteName(quoted, opts.quotingMode, opts.hideControlChars)
	ranges := []lsByteRange{{start: 0, end: len(quoted)}}

	if !opts.longFormat || opts.dereference == lsDerefAll {
		return quoted, ranges, nil
	}

	linfo, _, err := lstatPath(ctx, inv, abs)
	if err != nil {
		return "", nil, err
	}
	if linfo.Mode()&stdfs.ModeSymlink == 0 {
		return quoted, ranges, nil
	}
	target, err := inv.FS.Readlink(ctx, abs)
	if err != nil {
		return quoted, ranges, nil //nolint:nilerr // readlink failure falls back to showing name without target
	}
	targetQuoted := target
	if opts.quotingMode == lsQuoteLiteral && !opts.hideControlChars {
		targetQuoted = quote(target)
	}
	targetQuoted = lsQuoteName(targetQuoted, opts.quotingMode, opts.hideControlChars)

	display := quoted + " -> " + targetQuoted
	return display, []lsByteRange{{start: 0, end: len(display)}}, nil
}

func lsSuffixAndInfo(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, rawName string, opts *lsOptions) (string, stdfs.FileInfo, error) {
	if opts.dereference == lsDerefAll {
		switch opts.indicatorMode {
		case lsIndicatorClassify:
			if rawName == "." || rawName == ".." {
				return "/", info, nil
			}
			return classifyLSSuffix(info), info, nil
		case lsIndicatorFileType:
			if rawName == "." || rawName == ".." {
				return "/", info, nil
			}
			return fileTypeLSSuffix(info), info, nil
		case lsIndicatorSlash:
			if info.IsDir() { //nolint:nilaway // caller guarantees non-nil info
				return "/", info, nil
			}
		}
		return "", info, nil
	}

	linfo, _, err := lstatPath(ctx, inv, abs)
	if err != nil {
		return "", nil, err
	}
	switch opts.indicatorMode {
	case lsIndicatorClassify:
		if rawName == "." || rawName == ".." {
			return "/", linfo, nil
		}
		return classifyLSSuffix(linfo), linfo, nil
	case lsIndicatorFileType:
		if rawName == "." || rawName == ".." {
			return "/", linfo, nil
		}
		return fileTypeLSSuffix(linfo), linfo, nil
	case lsIndicatorSlash:
		if info.IsDir() {
			return "/", linfo, nil
		}
	}
	return "", linfo, nil
}

func lsShouldUseColor(inv *Invocation, mode lsColorMode) bool {
	switch mode {
	case lsColorAlways:
		return true
	case lsColorNever:
		return false
	default:
		return lsTerminalWriter(inv.Stdout)
	}
}

func lsTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func lsColorCode(ctx context.Context, inv *Invocation, abs string, info, linfo stdfs.FileInfo) (string, error) {
	if inv != nil && inv.Env["LS_COLORS"] != "" {
		if code, ok, err := lsColorCodeFromEnv(ctx, inv, abs, info, linfo); err != nil {
			return "", err
		} else if ok {
			return code, nil
		}
		return "", nil
	}
	return lsDefaultColorCode(linfo), nil
}

func lsColorCodeFromEnv(ctx context.Context, inv *Invocation, abs string, info, linfo stdfs.FileInfo) (code string, ok bool, err error) {
	lsColors := inv.Env["LS_COLORS"]
	if lsColors == "" {
		return "", false, nil
	}
	parsed := parseLSColorsEnv(lsColors)
	indicator, err := lsColorIndicator(ctx, inv, abs, info, linfo, parsed.entries)
	if err != nil {
		return "", false, err
	}
	if indicator != "" {
		if code, ok := parsed.entries[indicator]; ok {
			return code, true, nil
		}
	}
	name := path.Base(abs)
	lastMatch := ""
	matched := false
	for _, pattern := range parsed.patterns {
		if pattern.suffix == "" || strings.HasSuffix(name, pattern.suffix) {
			lastMatch = pattern.code
			matched = true
		}
	}
	if matched {
		return lastMatch, true, nil
	}
	if code, ok := parsed.entries["fi"]; ok {
		return code, true, nil
	}
	if code, ok := parsed.entries["no"]; ok {
		return code, true, nil
	}
	return "", false, nil
}

func parseLSColorsEnv(value string) lsParsedColors {
	parsed := lsParsedColors{entries: make(map[string]string)}
	for part := range strings.SplitSeq(value, ":") {
		if part == "" {
			continue
		}
		key, code, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if suffix, ok := strings.CutPrefix(key, "*"); ok {
			parsed.patterns = append(parsed.patterns, lsColorPattern{
				suffix: suffix,
				code:   code,
			})
			continue
		}
		parsed.entries[key] = code
	}
	return parsed
}

func lsColorIndicator(ctx context.Context, inv *Invocation, abs string, info, linfo stdfs.FileInfo, entries map[string]string) (string, error) {
	if linfo.Mode()&stdfs.ModeSymlink != 0 { //nolint:nilaway // caller guarantees non-nil linfo
		if entries["ln"] == "target" {
			targetInfo, _, exists, err := statMaybe(ctx, inv, abs)
			if err != nil {
				return "", err
			}
			if !exists {
				if _, ok := entries["or"]; ok {
					return "or", nil
				}
				if _, ok := entries["mi"]; ok {
					return "mi", nil
				}
				return "ln", nil
			}
			if targetInfo.IsDir() {
				return "di", nil
			}
			if targetInfo.Mode()&0o111 != 0 {
				return "ex", nil
			}
			return "", nil
		}
		return "ln", nil
	}
	switch {
	case info.IsDir(): //nolint:nilaway // caller guarantees non-nil info
		otherWritable := linfo.Mode().Perm()&0o002 != 0
		sticky := linfo.Mode()&stdfs.ModeSticky != 0
		switch {
		case sticky && otherWritable:
			return "tw", nil
		case otherWritable:
			return "ow", nil
		case sticky:
			return "st", nil
		}
		return "di", nil
	case linfo.Mode()&stdfs.ModeNamedPipe != 0:
		return "pi", nil
	case linfo.Mode()&stdfs.ModeSocket != 0:
		return "so", nil
	case linfo.Mode()&stdfs.ModeDevice != 0 && linfo.Mode()&stdfs.ModeCharDevice != 0:
		return "cd", nil
	case linfo.Mode()&stdfs.ModeDevice != 0:
		return "bd", nil
	case linfo.Mode()&0o4000 != 0:
		return "su", nil
	case linfo.Mode()&0o2000 != 0:
		return "sg", nil
	case linfo.Mode()&0o111 != 0:
		return "ex", nil
	default:
		return "", nil
	}
}

func lsDefaultColorCode(info stdfs.FileInfo) string {
	if info == nil {
		return ""
	}
	for _, entry := range dircolorsFileTypes {
		switch entry.Key {
		case "ln":
			if info.Mode()&stdfs.ModeSymlink != 0 {
				return entry.Code
			}
		case "tw":
			if info.IsDir() && info.Mode()&stdfs.ModeSticky != 0 && info.Mode().Perm()&0o002 != 0 {
				return entry.Code
			}
		case "ow":
			if info.IsDir() && info.Mode().Perm()&0o002 != 0 && info.Mode()&stdfs.ModeSticky == 0 {
				return entry.Code
			}
		case "st":
			if info.IsDir() && info.Mode()&stdfs.ModeSticky != 0 && info.Mode().Perm()&0o002 == 0 {
				return entry.Code
			}
		case "di":
			if info.IsDir() {
				return entry.Code
			}
		case "pi":
			if info.Mode()&stdfs.ModeNamedPipe != 0 {
				return entry.Code
			}
		case "so":
			if info.Mode()&stdfs.ModeSocket != 0 {
				return entry.Code
			}
		case "cd":
			if info.Mode()&stdfs.ModeDevice != 0 && info.Mode()&stdfs.ModeCharDevice != 0 {
				return entry.Code
			}
		case "bd":
			if info.Mode()&stdfs.ModeDevice != 0 && info.Mode()&stdfs.ModeCharDevice == 0 {
				return entry.Code
			}
		case "su":
			if info.Mode()&0o4000 != 0 {
				return entry.Code
			}
		case "sg":
			if info.Mode()&0o2000 != 0 {
				return entry.Code
			}
		case "ex":
			if info.Mode()&0o111 != 0 {
				return entry.Code
			}
		}
	}
	name := strings.ToLower(path.Base(info.Name()))
	for _, entry := range dircolorsFileColors {
		pattern := strings.ToLower(strings.TrimPrefix(entry.Pattern, "*"))
		if pattern == "" || strings.HasSuffix(name, pattern) {
			return entry.Code
		}
	}
	return ""
}

func (c *LS) loadLSEntries(ctx context.Context, inv *Invocation, dirAbs string, names []string, opts *lsOptions) ([]lsEntry, error) {
	entries := make([]lsEntry, 0, len(names))
	for _, name := range names {
		switch name {
		case ".", "..":
			info, _, err := statPath(ctx, inv, dirAbs)
			if err != nil {
				return nil, err
			}
			entries = append(entries, lsEntry{name: name, info: info, groupAsDir: info.IsDir()})
		default:
			info, groupAsDir, unknownShortMetadata, err := lsLoadEntryInfo(ctx, inv, path.Join(dirAbs, name), opts)
			if err != nil {
				return nil, err
			}
			entries = append(entries, lsEntry{
				name:                 name,
				info:                 info,
				groupAsDir:           groupAsDir,
				unknownShortMetadata: unknownShortMetadata,
			})
		}
	}
	return entries, nil
}

func sortLSEntries(entries []lsEntry, opts *lsOptions) {
	switch opts.sortMode {
	case lsSortSize:
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].info.Size() == entries[j].info.Size() {
				return entries[i].name < entries[j].name
			}
			return entries[i].info.Size() > entries[j].info.Size()
		})
	case lsSortTime:
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].info.ModTime().Equal(entries[j].info.ModTime()) {
				return entries[i].name < entries[j].name
			}
			return entries[i].info.ModTime().After(entries[j].info.ModTime())
		})
	case lsSortVersion:
		sort.SliceStable(entries, func(i, j int) bool {
			return lsNaturalLess(entries[i].name, entries[j].name)
		})
	case lsSortExtension:
		sort.SliceStable(entries, func(i, j int) bool {
			leftExt := path.Ext(entries[i].name)
			rightExt := path.Ext(entries[j].name)
			if leftExt == rightExt {
				return entries[i].name < entries[j].name
			}
			return leftExt < rightExt
		})
	case lsSortWidth:
		sort.SliceStable(entries, func(i, j int) bool {
			if len(entries[i].name) == len(entries[j].name) {
				return entries[i].name < entries[j].name
			}
			return len(entries[i].name) < len(entries[j].name)
		})
	case lsSortNone:
	default:
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].name < entries[j].name
		})
	}
	if opts.groupDirectoriesFirst && opts.sortMode != lsSortNone {
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].groupAsDir == entries[j].groupAsDir {
				return false
			}
			return entries[i].groupAsDir
		})
	}
	if opts.reverse {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}
}

func lsLoadEntryInfo(ctx context.Context, inv *Invocation, abs string, opts *lsOptions) (info stdfs.FileInfo, groupAsDir, unknownShortMetadata bool, err error) {
	linfo, _, err := lstatPath(ctx, inv, abs)
	if err != nil {
		return nil, false, false, err
	}

	info = linfo
	groupAsDir = linfo.IsDir()
	if opts != nil && opts.groupDirectoriesFirst && opts.sortMode != lsSortNone {
		groupAsDir = lsGroupsAsDirectory(ctx, inv, abs, linfo)
	}
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		targetInfo, _, statErr := lsResolveSymlinkInfo(ctx, inv, abs)
		switch {
		case statErr == nil:
			if opts != nil && opts.groupDirectoriesFirst && opts.sortMode != lsSortNone {
				groupAsDir = targetInfo.IsDir()
			}
			if opts != nil && opts.dereference == lsDerefAll {
				info = targetInfo
			}
		case opts != nil && opts.dereference == lsDerefAll && lsCanListBrokenImplicitSymlink(opts, linfo):
			return linfo, false, opts.showInode || opts.showAllocSize, nil
		case opts != nil && opts.dereference == lsDerefAll:
			return nil, false, false, statErr
		}
		return info, groupAsDir, false, nil
	}

	if opts != nil && opts.dereference == lsDerefAll {
		info, _, err = statPath(ctx, inv, abs)
		if err != nil {
			return nil, false, false, err
		}
		if opts.groupDirectoriesFirst && opts.sortMode != lsSortNone {
			groupAsDir = info.IsDir()
		}
	}
	return info, groupAsDir, false, nil
}

func lsGroupsAsDirectory(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo) bool {
	if info != nil && info.IsDir() {
		return true
	}
	if info == nil || info.Mode()&stdfs.ModeSymlink == 0 {
		return false
	}
	targetInfo, _, err := lsResolveSymlinkInfo(ctx, inv, abs)
	return err == nil && targetInfo != nil && targetInfo.IsDir()
}

func lsCanListBrokenImplicitSymlink(opts *lsOptions, info stdfs.FileInfo) bool {
	if opts == nil || info == nil || info.Mode()&stdfs.ModeSymlink == 0 {
		return false
	}
	return !opts.longFormat && opts.indicatorMode == lsIndicatorNone
}

func lsResolveSymlinkInfo(ctx context.Context, inv *Invocation, abs string) (stdfs.FileInfo, string, error) {
	current := abs
	for depth := 0; depth <= readlinkMaxSymlinkDepth; depth++ {
		info, _, err := lstatPath(ctx, inv, current)
		if err != nil {
			return nil, "", err
		}
		if info.Mode()&stdfs.ModeSymlink == 0 {
			return info, current, nil
		}
		if depth == readlinkMaxSymlinkDepth {
			return nil, "", &os.PathError{Op: "realpath", Path: current, Err: fmt.Errorf("too many levels of symbolic links")}
		}
		target, err := inv.FS.Readlink(ctx, current)
		if err != nil {
			return nil, "", err
		}
		if path.IsAbs(target) {
			current = path.Clean(target)
		} else {
			current = path.Clean(path.Join(path.Dir(current), target))
		}
	}
	return nil, "", &os.PathError{Op: "realpath", Path: abs, Err: fmt.Errorf("too many levels of symbolic links")}
}

func sortLSPathArgs(args []lsPathArg, opts *lsOptions) {
	switch opts.sortMode {
	case lsSortSize:
		sort.SliceStable(args, func(i, j int) bool {
			if args[i].info.Size() == args[j].info.Size() {
				return args[i].name < args[j].name
			}
			return args[i].info.Size() > args[j].info.Size()
		})
	case lsSortTime:
		sort.SliceStable(args, func(i, j int) bool {
			if args[i].info.ModTime().Equal(args[j].info.ModTime()) {
				return args[i].name < args[j].name
			}
			return args[i].info.ModTime().After(args[j].info.ModTime())
		})
	case lsSortVersion:
		sort.SliceStable(args, func(i, j int) bool {
			return lsNaturalLess(args[i].name, args[j].name)
		})
	case lsSortExtension:
		sort.SliceStable(args, func(i, j int) bool {
			leftExt := path.Ext(args[i].name)
			rightExt := path.Ext(args[j].name)
			if leftExt == rightExt {
				return args[i].name < args[j].name
			}
			return leftExt < rightExt
		})
	case lsSortWidth:
		sort.SliceStable(args, func(i, j int) bool {
			if len(args[i].name) == len(args[j].name) {
				return args[i].name < args[j].name
			}
			return len(args[i].name) < len(args[j].name)
		})
	case lsSortNone:
	default:
		sort.SliceStable(args, func(i, j int) bool {
			return args[i].name < args[j].name
		})
	}
	if opts.groupDirectoriesFirst && opts.sortMode != lsSortNone {
		sort.SliceStable(args, func(i, j int) bool {
			if args[i].groupAsDir == args[j].groupAsDir {
				return false
			}
			return args[i].groupAsDir
		})
	}
	if opts.reverse {
		for i, j := 0, len(args)-1; i < j; i, j = i+1, j-1 {
			args[i], args[j] = args[j], args[i]
		}
	}
}

func lsNaturalLess(left, right string) bool {
	for left != "" && right != "" {
		lr, rr := left[0], right[0]
		if lr >= '0' && lr <= '9' && rr >= '0' && rr <= '9' {
			ln, lrest := lsNumericChunk(left)
			rn, rrest := lsNumericChunk(right)
			if len(ln) != len(rn) {
				return len(ln) < len(rn)
			}
			if ln != rn {
				return ln < rn
			}
			left, right = lrest, rrest
			continue
		}
		if lr != rr {
			return lr < rr
		}
		left = left[1:]
		right = right[1:]
	}
	return left < right
}

func lsNumericChunk(value string) (chunk, rest string) {
	index := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	return value[:index], value[index:]
}

func formatLSLongLine(name string, info stdfs.FileInfo, opts *lsOptions, nameRanges []lsByteRange, now time.Time) (string, []lsByteRange) {
	fields := make([]string, 0, 9)
	userToken, groupToken, authorToken := lsIdentityTokens(info, opts.numericIDs, opts.identityDB)
	if opts.showInode {
		fields = append(fields, strconv.FormatUint(statInode(info), 10))
	}
	if opts.showAllocSize {
		fields = append(fields, formatLSBlockCount(info, opts))
	}
	fields = append(fields, formatModeLong(info.Mode()), "1") //nolint:nilaway // caller guarantees non-nil info
	if opts.showOwner {
		fields = append(fields, userToken)
	}
	if opts.showGroup {
		fields = append(fields, groupToken)
	}
	if opts.showAuthor {
		fields = append(fields, authorToken)
	}
	fields = append(fields, formatLSSize(info, opts), formatLSDateStyle(info.ModTime(), opts, now))
	prefix := strings.Join(fields, " ")
	if prefix != "" {
		prefix += " "
	}
	line := prefix + name + "\n"
	dired := make([]lsByteRange, 0, len(nameRanges))
	for _, entry := range nameRanges {
		dired = append(dired, lsByteRange{start: len(prefix) + entry.start, end: len(prefix) + entry.end})
	}
	return line, dired
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

func formatHumanSizeBase(bytes int64, base float64, suffixes []string) string {
	value := float64(bytes)
	suffix := ""
	for i := 0; i < len(suffixes) && value >= base; i++ {
		value /= base
		suffix = suffixes[i]
	}
	if suffix == "" {
		return strconv.FormatInt(bytes, 10)
	}
	if value < 10 {
		return fmt.Sprintf("%.1f%s", value, suffix)
	}
	return fmt.Sprintf("%.0f%s", value, suffix)
}

func formatLSSize(info stdfs.FileInfo, opts *lsOptions) string {
	size := info.Size() //nolint:nilaway // caller guarantees non-nil info
	switch {
	case opts.si:
		return formatHumanSizeBase(size, 1000, []string{"K", "M", "G", "T", "P", "E"})
	case opts.humanReadable:
		return formatHumanSize(size)
	case opts.blockSize > 1:
		return strconv.FormatInt((size+opts.blockSize-1)/opts.blockSize, 10)
	default:
		return strconv.FormatInt(size, 10)
	}
}

func lsBlockCountValue(info stdfs.FileInfo, opts *lsOptions) int64 {
	blockSize := opts.blockSize
	if blockSize <= 0 {
		blockSize = 1024
	}
	size := info.Size() //nolint:nilaway // caller guarantees non-nil info
	return (size + blockSize - 1) / blockSize
}

func formatLSBlockCount(info stdfs.FileInfo, opts *lsOptions) string {
	return strconv.FormatInt(lsBlockCountValue(info, opts), 10)
}

func lsShouldShowDirectoryTotal(opts *lsOptions) bool {
	return opts != nil && (opts.longFormat || opts.showAllocSize)
}

func lsDirectoryTotal(entries []lsEntry, opts *lsOptions) int64 {
	var total int64
	for _, entry := range entries {
		if entry.unknownShortMetadata {
			continue
		}
		total += lsBlockCountValue(entry.info, opts)
	}
	return total
}

func formatLSShortPrefix(info stdfs.FileInfo, opts *lsOptions, unknownMetadata bool) string {
	parts := make([]string, 0, 2)
	if opts.showInode {
		if unknownMetadata {
			parts = append(parts, "?")
		} else {
			parts = append(parts, strconv.FormatUint(statInode(info), 10))
		}
	}
	if opts.showAllocSize {
		if unknownMetadata {
			parts = append(parts, "?")
		} else {
			parts = append(parts, formatLSBlockCount(info, opts))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ") + " "
}

func lsIdentityTokens(info stdfs.FileInfo, numericIDs bool, db *permissionIdentityDB) (userToken, groupToken, authorToken string) {
	ownership, ok := gbfs.OwnershipFromFileInfo(info)
	if !ok {
		ownership = gbfs.DefaultOwnership()
	}
	userToken = strconv.FormatUint(uint64(ownership.UID), 10)
	groupToken = strconv.FormatUint(uint64(ownership.GID), 10)
	authorToken = userToken
	if numericIDs {
		return userToken, groupToken, authorToken
	}
	owner := permissionLookupOwnership(db, info)
	userToken = permissionNameOrID(owner.user, owner.uid)
	groupToken = permissionNameOrID(owner.group, owner.gid)
	authorToken = userToken
	return userToken, groupToken, authorToken
}

func formatLSDate(ts, now time.Time) string {
	month := ts.Format("Jan")
	day := fmt.Sprintf("%2d", ts.Day())
	sixMonthsAgo := now.Add(-180 * 24 * time.Hour)
	if ts.After(sixMonthsAgo) {
		return fmt.Sprintf("%s %s %02d:%02d", month, day, ts.Hour(), ts.Minute())
	}
	return fmt.Sprintf("%s %s  %04d", month, day, ts.Year())
}

func formatLSDateStyle(ts time.Time, opts *lsOptions, now time.Time) string {
	switch opts.timeStyle {
	case "":
		return formatLSDate(ts, now)
	case "full-iso":
		return ts.Format("2006-01-02 15:04:05.000000000 -0700")
	case "long-iso":
		return ts.Format("2006-01-02 15:04")
	case "iso":
		return ts.Format("01-02 15:04")
	case "locale":
		return formatLSDate(ts, now)
	default:
		if format, ok := strings.CutPrefix(opts.timeStyle, "+"); ok {
			return ts.Format(format)
		}
		return formatLSDate(ts, now)
	}
}

func classifyLSSuffix(info stdfs.FileInfo) string {
	if info.Mode()&stdfs.ModeSymlink != 0 { //nolint:nilaway // caller guarantees non-nil info
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

func fileTypeLSSuffix(info stdfs.FileInfo) string {
	if info.Mode()&stdfs.ModeSymlink != 0 { //nolint:nilaway // caller guarantees non-nil info
		return "@"
	}
	if info.IsDir() {
		return "/"
	}
	return ""
}

func lsQuoteName(value string, mode lsQuotingMode, hideControlChars bool) string {
	base := value
	if hideControlChars {
		base = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return '?'
			}
			return r
		}, base)
	}
	switch mode {
	case lsQuoteEscape:
		return dirQuoteName(base)
	case lsQuoteC:
		return strconv.Quote(base)
	case lsQuoteShell:
		if !lsNeedsShellQuoting(base) {
			return base
		}
		return shellSingleQuote(base)
	case lsQuoteShellAlways:
		return shellSingleQuote(base)
	default:
		return base
	}
}

func lsNeedsShellQuoting(value string) bool {
	if value == "" {
		return true
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._/-", r):
		default:
			return true
		}
	}
	return false
}

var _ Command = (*LS)(nil)
var _ SpecProvider = (*LS)(nil)
var _ ParsedRunner = (*LS)(nil)
