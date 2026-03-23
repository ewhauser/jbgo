package builtins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Diff struct{}

type diffOutputMode int

type diffColorMode int

type diffOpKind byte

const (
	diffModeNormal diffOutputMode = iota
	diffModeContext
	diffModeUnified
	diffModeEd
	diffModeRCS
	diffModeSideBySide
	diffModeIfdef
	diffModeCustom
)

const (
	diffColorNever diffColorMode = iota
	diffColorAlways
	diffColorAuto
)

const (
	diffOpEqual  diffOpKind = ' '
	diffOpDelete diffOpKind = '-'
	diffOpInsert diffOpKind = '+'
)

type diffOptions struct {
	mode                diffOutputMode
	contextLines        int
	brief               bool
	reportSame          bool
	sideBySide          bool
	width               int
	leftColumn          bool
	suppressCommonLines bool
	showCFunction       bool
	showFunctionLine    string
	showFunctionRegexp  *regexp.Regexp
	labels              []string
	expandTabs          bool
	initialTab          bool
	tabSize             int
	suppressBlankEmpty  bool
	paginate            bool
	recursive           bool
	noDereference       bool
	newFile             bool
	unidirectionalNew   bool
	ignoreFileNameCase  bool
	excludes            []string
	excludeFiles        []string
	startingFile        string
	fromFile            string
	toFile              string
	ignoreCase          bool
	ignoreTabExpansion  bool
	ignoreTrailingSpace bool
	ignoreSpaceChange   bool
	ignoreAllSpace      bool
	ignoreBlankLines    bool
	ignoreMatchingRaw   []string
	ignoreMatching      []*regexp.Regexp
	text                bool
	stripTrailingCR     bool
	ifdefName           string
	groupFormats        map[string]string
	lineFormats         map[string]string
	lineFormat          string
	minimal             bool
	horizonLines        int
	speedLargeFiles     bool
	colorMode           diffColorMode
	palette             string
	showHelp            bool
	showVersion         bool
}

type diffJob struct {
	left  string
	right string
}

type diffLoader struct {
	stdinData   []byte
	stdinLoaded bool
}

type diffInput struct {
	name               string
	abs                string
	exists             bool
	isDir              bool
	isSymlink          bool
	info               stdfs.FileInfo
	data               []byte
	lines              []diffLine
	totalLines         int
	hasTrailingNewline bool
	binary             bool
}

type diffLine struct {
	text   string
	key    string
	lineNo int
}

type diffUnit struct {
	kind     diffOpKind
	leftPos  int
	rightPos int
	left     *diffLine
	right    *diffLine
}

type diffChange struct {
	units []diffUnit
}

type diffHunk struct {
	units []diffUnit
}

func NewDiff() *Diff {
	return &Diff{}
}

func (c *Diff) Name() string {
	return "diff"
}

func (c *Diff) Run(ctx context.Context, inv *Invocation) error {
	if inv == nil {
		return nil
	}

	opts, jobs, err := parseDiffArgs(inv)
	if err != nil {
		return err
	}
	if opts.showHelp {
		_, err := io.WriteString(inv.Stdout, diffHelpText)
		return diffWriteError(err)
	}
	if opts.showVersion {
		_, err := io.WriteString(inv.Stdout, diffVersionText)
		return diffWriteError(err)
	}

	if err := diffLoadExcludePatterns(ctx, inv, &opts); err != nil {
		return err
	}
	if err := diffCompileRegexes(inv, &opts); err != nil {
		return err
	}

	loader := &diffLoader{}
	exitCode := 0
	for i, job := range jobs {
		if i > 0 {
			if _, err := io.WriteString(inv.Stdout, "\n"); err != nil {
				return diffWriteError(err)
			}
		}
		status, err := c.compareJob(ctx, inv, &opts, loader, job)
		if err != nil {
			return err
		}
		if status > exitCode {
			exitCode = status
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseDiffArgs(inv *Invocation) (diffOptions, []diffJob, error) {
	opts := diffOptions{
		mode:         diffModeNormal,
		contextLines: 3,
		width:        130,
		tabSize:      8,
		colorMode:    diffColorNever,
		groupFormats: make(map[string]string),
		lineFormats:  make(map[string]string),
	}

	args := append([]string(nil), inv.Args...)
	var positionals []string
	endOfOptions := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if endOfOptions || arg == "-" || !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		if arg == "--" {
			endOfOptions = true
			continue
		}
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := strings.Cut(arg[2:], "=")
			switch name {
			case "help":
				if hasValue {
					return diffOptions{}, nil, diffUsageAfter(inv, arg)
				}
				opts.showHelp = true
				return opts, nil, nil
			case "version":
				if hasValue {
					return diffOptions{}, nil, diffUsageAfter(inv, arg)
				}
				opts.showVersion = true
				return opts, nil, nil
			case "normal":
				opts.mode = diffModeNormal
				opts.sideBySide = false
			case "brief":
				opts.brief = true
			case "report-identical-files":
				opts.reportSame = true
			case "context":
				n, err := diffOptionalIntArg(inv, value, hasValue, 3)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.mode = diffModeContext
				opts.contextLines = n
				opts.sideBySide = false
			case "unified":
				n, err := diffOptionalIntArg(inv, value, hasValue, 3)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.mode = diffModeUnified
				opts.contextLines = n
				opts.sideBySide = false
			case "ed":
				opts.mode = diffModeEd
				opts.sideBySide = false
			case "rcs":
				opts.mode = diffModeRCS
				opts.sideBySide = false
			case "side-by-side":
				opts.mode = diffModeSideBySide
				opts.sideBySide = true
			case "width":
				n, err := diffRequiredIntArg(inv, "--width", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.width = n
			case "left-column":
				opts.leftColumn = true
			case "suppress-common-lines":
				opts.suppressCommonLines = true
			case "show-c-function":
				opts.showCFunction = true
			case "show-function-line":
				v, err := diffRequiredStringArg(inv, "--show-function-line", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.showFunctionLine = v
			case "label":
				v, err := diffRequiredStringArg(inv, "--label", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.labels = append(opts.labels, v)
			case "expand-tabs":
				opts.expandTabs = true
			case "initial-tab":
				opts.initialTab = true
			case "tabsize":
				n, err := diffRequiredIntArg(inv, "--tabsize", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.tabSize = n
			case "suppress-blank-empty":
				opts.suppressBlankEmpty = true
			case "paginate":
				opts.paginate = true
			case "recursive":
				opts.recursive = true
			case "no-dereference":
				opts.noDereference = true
			case "new-file":
				opts.newFile = true
			case "unidirectional-new-file":
				opts.unidirectionalNew = true
			case "ignore-file-name-case":
				opts.ignoreFileNameCase = true
			case "no-ignore-file-name-case":
				opts.ignoreFileNameCase = false
			case "exclude":
				v, err := diffRequiredStringArg(inv, "--exclude", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.excludes = append(opts.excludes, v)
			case "exclude-from":
				v, err := diffRequiredStringArg(inv, "--exclude-from", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.excludeFiles = append(opts.excludeFiles, v)
			case "starting-file":
				v, err := diffRequiredStringArg(inv, "--starting-file", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.startingFile = v
			case "from-file":
				v, err := diffRequiredStringArg(inv, "--from-file", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.fromFile = v
			case "to-file":
				v, err := diffRequiredStringArg(inv, "--to-file", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.toFile = v
			case "ignore-case":
				opts.ignoreCase = true
			case "ignore-tab-expansion":
				opts.ignoreTabExpansion = true
			case "ignore-trailing-space":
				opts.ignoreTrailingSpace = true
			case "ignore-space-change":
				opts.ignoreSpaceChange = true
			case "ignore-all-space":
				opts.ignoreAllSpace = true
			case "ignore-blank-lines":
				opts.ignoreBlankLines = true
			case "ignore-matching-lines":
				v, err := diffRequiredStringArg(inv, "--ignore-matching-lines", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.ignoreMatchingRaw = append(opts.ignoreMatchingRaw, v)
			case "text":
				opts.text = true
			case "strip-trailing-cr":
				opts.stripTrailingCR = true
			case "ifdef":
				v, err := diffRequiredStringArg(inv, "--ifdef", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.ifdefName = v
				opts.mode = diffModeIfdef
				opts.sideBySide = false
			case "old-group-format", "new-group-format", "changed-group-format", "unchanged-group-format":
				v, err := diffRequiredStringArg(inv, "--"+name, value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.groupFormats[name[:len(name)-len("-format")]] = v
				opts.mode = diffModeCustom
				opts.sideBySide = false
			case "line-format":
				v, err := diffRequiredStringArg(inv, "--line-format", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.lineFormat = v
				opts.mode = diffModeCustom
				opts.sideBySide = false
			case "old-line-format", "new-line-format", "unchanged-line-format":
				v, err := diffRequiredStringArg(inv, "--"+name, value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.lineFormats[name[:len(name)-len("-format")]] = v
				opts.mode = diffModeCustom
				opts.sideBySide = false
			case "minimal":
				opts.minimal = true
			case "horizon-lines":
				n, err := diffRequiredIntArg(inv, "--horizon-lines", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.horizonLines = n
			case "speed-large-files":
				opts.speedLargeFiles = true
			case "color":
				if !hasValue {
					opts.colorMode = diffColorAuto
					break
				}
				mode, err := parseDiffColorMode(value)
				if err != nil {
					return diffOptions{}, nil, exitf(inv, 2, "diff: invalid argument %s for '--color'\nTry 'diff --help' for more information.", quoteGNUOperand(value))
				}
				opts.colorMode = mode
			case "palette":
				v, err := diffRequiredStringArg(inv, "--palette", value, hasValue, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				opts.palette = v
			default:
				return diffOptions{}, nil, exitf(inv, 2, "diff: unrecognized option '--%s'\nTry 'diff --help' for more information.", name)
			}
			continue
		}

		shorts := arg[1:]
		for shorts != "" {
			ch := shorts[0]
			shorts = shorts[1:]
			switch ch {
			case 'q':
				opts.brief = true
			case 's':
				opts.reportSame = true
			case 'c':
				opts.mode = diffModeContext
				opts.contextLines = 3
				opts.sideBySide = false
			case 'C':
				v, err := diffShortValue(inv, "-C", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				n, err := strconv.Atoi(v)
				if err != nil {
					return diffOptions{}, nil, diffInvalidNumber(inv, v)
				}
				opts.mode = diffModeContext
				opts.contextLines = n
				opts.sideBySide = false
			case 'u':
				opts.mode = diffModeUnified
				opts.contextLines = 3
				opts.sideBySide = false
			case 'U':
				v, err := diffShortValue(inv, "-U", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				n, err := strconv.Atoi(v)
				if err != nil {
					return diffOptions{}, nil, diffInvalidNumber(inv, v)
				}
				opts.mode = diffModeUnified
				opts.contextLines = n
				opts.sideBySide = false
			case 'e':
				opts.mode = diffModeEd
				opts.sideBySide = false
			case 'n':
				opts.mode = diffModeRCS
				opts.sideBySide = false
			case 'y':
				opts.mode = diffModeSideBySide
				opts.sideBySide = true
			case 'W':
				v, err := diffShortValue(inv, "-W", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				n, err := strconv.Atoi(v)
				if err != nil {
					return diffOptions{}, nil, diffInvalidNumber(inv, v)
				}
				opts.width = n
			case 'p':
				opts.showCFunction = true
			case 'F':
				v, err := diffShortValue(inv, "-F", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				opts.showFunctionLine = v
			case 'L':
				v, err := diffShortValue(inv, "-L", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				opts.labels = append(opts.labels, v)
			case 't':
				opts.expandTabs = true
			case 'T':
				opts.initialTab = true
			case 'l':
				opts.paginate = true
			case 'r':
				opts.recursive = true
			case 'N':
				opts.newFile = true
			case 'x':
				v, err := diffShortValue(inv, "-x", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				opts.excludes = append(opts.excludes, v)
			case 'X':
				v, err := diffShortValue(inv, "-X", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				opts.excludeFiles = append(opts.excludeFiles, v)
			case 'S':
				v, err := diffShortValue(inv, "-S", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				opts.startingFile = v
			case 'i':
				opts.ignoreCase = true
			case 'E':
				opts.ignoreTabExpansion = true
			case 'Z':
				opts.ignoreTrailingSpace = true
			case 'b':
				opts.ignoreSpaceChange = true
			case 'w':
				opts.ignoreAllSpace = true
			case 'B':
				opts.ignoreBlankLines = true
			case 'I':
				v, err := diffShortValue(inv, "-I", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				opts.ignoreMatchingRaw = append(opts.ignoreMatchingRaw, v)
			case 'a':
				opts.text = true
			case 'D':
				v, err := diffShortValue(inv, "-D", shorts, args, &i)
				if err != nil {
					return diffOptions{}, nil, err
				}
				shorts = ""
				opts.ifdefName = v
				opts.mode = diffModeIfdef
				opts.sideBySide = false
			case 'd':
				opts.minimal = true
			case 'v':
				opts.showVersion = true
				return opts, nil, nil
			default:
				return diffOptions{}, nil, exitf(inv, 2, "diff: invalid option -- '%c'\nTry 'diff --help' for more information.", ch)
			}
		}
	}
	if opts.fromFile != "" && opts.toFile != "" {
		return diffOptions{}, nil, exitf(inv, 2, "diff: options '--from-file' and '--to-file' are mutually exclusive\nTry 'diff --help' for more information.")
	}

	var jobs []diffJob
	switch {
	case opts.fromFile != "":
		if len(positionals) == 0 {
			return diffOptions{}, nil, diffMissingOperand(inv, quoteGNUOperand(opts.fromFile))
		}
		for _, name := range positionals {
			jobs = append(jobs, diffJob{left: opts.fromFile, right: name})
		}
	case opts.toFile != "":
		if len(positionals) == 0 {
			return diffOptions{}, nil, diffMissingOperand(inv, quoteGNUOperand(opts.toFile))
		}
		for _, name := range positionals {
			jobs = append(jobs, diffJob{left: name, right: opts.toFile})
		}
	default:
		switch len(positionals) {
		case 0:
			return diffOptions{}, nil, diffMissingOperand(inv, quoteGNUOperand("diff"))
		case 1:
			return diffOptions{}, nil, diffMissingOperand(inv, quoteGNUOperand(positionals[0]))
		case 2:
			jobs = append(jobs, diffJob{left: positionals[0], right: positionals[1]})
		default:
			return diffOptions{}, nil, exitf(inv, 2, "diff: extra operand %s\nTry 'diff --help' for more information.", quoteGNUOperand(positionals[2]))
		}
	}
	return opts, jobs, nil
}

func diffUsageAfter(inv *Invocation, value string) error {
	return exitf(inv, 2, "diff: option %s doesn't allow an argument\nTry 'diff --help' for more information.", quoteGNUOperand(value))
}

func diffMissingOperand(inv *Invocation, after string) error {
	return exitf(inv, 2, "diff: missing operand after %s\nTry 'diff --help' for more information.", after)
}

func diffInvalidNumber(inv *Invocation, value string) error {
	return exitf(inv, 2, "diff: invalid number %s\nTry 'diff --help' for more information.", quoteGNUOperand(value))
}

func diffRequiredIntArg(inv *Invocation, opt, value string, hasValue bool, args []string, idx *int) (int, error) {
	v, err := diffRequiredStringArg(inv, opt, value, hasValue, args, idx)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, diffInvalidNumber(inv, v)
	}
	return n, nil
}

func diffOptionalIntArg(inv *Invocation, value string, hasValue bool, def int) (int, error) {
	if !hasValue {
		return def, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, diffInvalidNumber(inv, value)
	}
	return n, nil
}

func diffRequiredStringArg(inv *Invocation, opt, value string, hasValue bool, args []string, idx *int) (string, error) {
	if hasValue {
		return value, nil
	}
	if *idx+1 >= len(args) {
		return "", diffMissingOperand(inv, quoteGNUOperand(opt))
	}
	*idx++
	return args[*idx], nil
}

func diffShortValue(inv *Invocation, opt, rest string, args []string, idx *int) (string, error) {
	if rest != "" {
		return rest, nil
	}
	if *idx+1 >= len(args) {
		return "", diffMissingOperand(inv, quoteGNUOperand(opt))
	}
	*idx++
	return args[*idx], nil
}

func parseDiffColorMode(value string) (diffColorMode, error) {
	switch value {
	case "never":
		return diffColorNever, nil
	case "always":
		return diffColorAlways, nil
	case "auto":
		return diffColorAuto, nil
	default:
		return diffColorNever, fmt.Errorf("invalid color mode")
	}
}

func diffLoadExcludePatterns(ctx context.Context, inv *Invocation, opts *diffOptions) error {
	for _, name := range opts.excludeFiles {
		data, _, err := readAllFile(ctx, inv, name)
		if err != nil {
			return exitf(inv, 2, "diff: %s: %s", name, readAllErrorText(err))
		}
		for _, line := range textLines(data) {
			if line == "" {
				continue
			}
			opts.excludes = append(opts.excludes, line)
		}
	}
	return nil
}

func diffCompileRegexes(inv *Invocation, opts *diffOptions) error {
	pattern := opts.showFunctionLine
	if opts.showCFunction && pattern == "" {
		pattern = `^[A-Za-z_$]`
	}
	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return exitf(inv, 2, "diff: invalid regular expression %s", quoteGNUOperand(pattern))
		}
		opts.showFunctionRegexp = re
	}
	for _, value := range opts.ignoreMatchingRaw {
		re, err := regexp.Compile(value)
		if err != nil {
			return exitf(inv, 2, "diff: invalid regular expression %s", quoteGNUOperand(value))
		}
		opts.ignoreMatching = append(opts.ignoreMatching, re)
	}
	return nil
}

func (c *Diff) compareJob(ctx context.Context, inv *Invocation, opts *diffOptions, loader *diffLoader, job diffJob) (int, error) {
	leftInfo, leftAbs, leftExists, leftErr := diffStatMaybe(ctx, inv, opts.noDereference, job.left)
	if leftErr != nil {
		_, _ = fmt.Fprintf(inv.Stderr, "diff: %s: %s\n", job.left, readAllErrorText(leftErr))
		return 2, nil
	}
	rightInfo, rightAbs, rightExists, rightErr := diffStatMaybe(ctx, inv, opts.noDereference, job.right)
	if rightErr != nil {
		_, _ = fmt.Fprintf(inv.Stderr, "diff: %s: %s\n", job.right, readAllErrorText(rightErr))
		return 2, nil
	}

	leftDir := leftExists && leftInfo != nil && leftInfo.IsDir()
	rightDir := rightExists && rightInfo != nil && rightInfo.IsDir()

	switch {
	case leftDir && rightDir:
		return c.compareDirectories(ctx, inv, opts, loader, job.left, leftAbs, job.right, rightAbs)
	case leftDir:
		target := path.Join(job.left, path.Base(job.right))
		return c.comparePathPair(ctx, inv, opts, loader, target, job.right, true)
	case rightDir:
		target := path.Join(job.right, path.Base(job.left))
		return c.comparePathPair(ctx, inv, opts, loader, job.left, target, true)
	default:
		return c.comparePathPair(ctx, inv, opts, loader, job.left, job.right, false)
	}
}

func (c *Diff) compareDirectories(ctx context.Context, inv *Invocation, opts *diffOptions, loader *diffLoader, leftName, leftAbs, rightName, rightAbs string) (int, error) {
	leftEntries, err := readDir(ctx, inv, leftAbs)
	if err != nil {
		return 2, exitf(inv, 2, "diff: %s: %s", leftName, readAllErrorText(err))
	}
	rightEntries, err := readDir(ctx, inv, rightAbs)
	if err != nil {
		return 2, exitf(inv, 2, "diff: %s: %s", rightName, readAllErrorText(err))
	}

	leftMap := make(map[string]stdfs.DirEntry, len(leftEntries))
	rightMap := make(map[string]stdfs.DirEntry, len(rightEntries))
	var keys []string
	for _, entry := range leftEntries {
		name := entry.Name()
		if diffExcluded(name, opts.excludes) {
			continue
		}
		key := diffFileNameKey(name, opts.ignoreFileNameCase)
		leftMap[key] = entry
		keys = append(keys, key)
	}
	for _, entry := range rightEntries {
		name := entry.Name()
		if diffExcluded(name, opts.excludes) {
			continue
		}
		key := diffFileNameKey(name, opts.ignoreFileNameCase)
		if _, ok := rightMap[key]; !ok {
			keys = append(keys, key)
		}
		rightMap[key] = entry
	}
	sort.Strings(keys)
	keys = diffUniqueStrings(keys)
	if opts.startingFile != "" {
		startKey := diffFileNameKey(opts.startingFile, opts.ignoreFileNameCase)
		keys = diffFilterStartingFile(keys, startKey)
	}

	status := 0
	for _, key := range keys {
		leftEntry, leftOK := leftMap[key]
		rightEntry, rightOK := rightMap[key]
		switch {
		case leftOK && rightOK:
			leftChild := path.Join(leftName, leftEntry.Name())
			rightChild := path.Join(rightName, rightEntry.Name())
			leftChildAbs := path.Join(leftAbs, leftEntry.Name())
			rightChildAbs := path.Join(rightAbs, rightEntry.Name())

			leftInfo, lerr := diffDirEntryInfo(ctx, inv, leftChildAbs, leftEntry, opts.noDereference)
			if lerr != nil {
				_, _ = fmt.Fprintf(inv.Stderr, "diff: %s: %s\n", leftChild, readAllErrorText(lerr))
				status = max(status, 2)
				continue
			}
			rightInfo, rerr := diffDirEntryInfo(ctx, inv, rightChildAbs, rightEntry, opts.noDereference)
			if rerr != nil {
				_, _ = fmt.Fprintf(inv.Stderr, "diff: %s: %s\n", rightChild, readAllErrorText(rerr))
				status = max(status, 2)
				continue
			}

			switch {
			case leftInfo.IsDir() && rightInfo.IsDir():
				if opts.recursive {
					subStatus, err := c.compareDirectories(ctx, inv, opts, loader, leftChild, leftChildAbs, rightChild, rightChildAbs)
					if err != nil {
						return 0, err
					}
					status = max(status, subStatus)
				} else {
					if _, err := fmt.Fprintf(inv.Stdout, "Common subdirectories: %s and %s\n", leftChild, rightChild); err != nil {
						return 0, diffWriteError(err)
					}
				}
			case leftInfo.IsDir() != rightInfo.IsDir():
				if _, err := fmt.Fprintf(inv.Stdout, "File %s is a directory while file %s is a regular file\n", leftChild, rightChild); err != nil {
					return 0, diffWriteError(err)
				}
				status = max(status, 1)
			default:
				subStatus, err := c.comparePathPair(ctx, inv, opts, loader, leftChild, rightChild, true)
				if err != nil {
					return 0, err
				}
				status = max(status, subStatus)
			}
		case leftOK:
			leftChild := path.Join(leftName, leftEntry.Name())
			if opts.newFile {
				subStatus, err := c.comparePathPair(ctx, inv, opts, loader, leftChild, path.Join(rightName, leftEntry.Name()), true)
				if err != nil {
					return 0, err
				}
				status = max(status, subStatus)
				continue
			}
			if _, err := fmt.Fprintf(inv.Stdout, "Only in %s: %s\n", leftName, leftEntry.Name()); err != nil {
				return 0, diffWriteError(err)
			}
			status = max(status, 1)
		case rightOK:
			rightChild := path.Join(rightName, rightEntry.Name())
			if opts.newFile || opts.unidirectionalNew {
				subStatus, err := c.comparePathPair(ctx, inv, opts, loader, path.Join(leftName, rightEntry.Name()), rightChild, true)
				if err != nil {
					return 0, err
				}
				status = max(status, subStatus)
				continue
			}
			if _, err := fmt.Fprintf(inv.Stdout, "Only in %s: %s\n", rightName, rightEntry.Name()); err != nil {
				return 0, diffWriteError(err)
			}
			status = max(status, 1)
		}
	}
	return status, nil
}

func (c *Diff) comparePathPair(ctx context.Context, inv *Invocation, opts *diffOptions, loader *diffLoader, leftName, rightName string, announce bool) (int, error) {
	left, err := diffLoadInput(ctx, inv, opts, loader, leftName, opts.newFile || opts.unidirectionalNew)
	if err != nil {
		_, _ = fmt.Fprintf(inv.Stderr, "diff: %s: %s\n", leftName, readAllErrorText(err))
		return 2, nil
	}
	right, err := diffLoadInput(ctx, inv, opts, loader, rightName, opts.newFile)
	if err != nil {
		_, _ = fmt.Fprintf(inv.Stderr, "diff: %s: %s\n", rightName, readAllErrorText(err))
		return 2, nil
	}
	if left.isDir || right.isDir {
		dirName := leftName
		if right.isDir {
			dirName = rightName
		}
		if _, err := fmt.Fprintf(inv.Stderr, "diff: %s: Is a directory\n", dirName); err != nil {
			return 0, diffWriteError(err)
		}
		return 2, nil
	}
	if announce && !opts.brief {
		if _, err := fmt.Fprintf(inv.Stdout, "%s %s %s\n", diffComparePrefix(opts), leftName, rightName); err != nil {
			return 0, diffWriteError(err)
		}
	}
	return diffRenderPair(ctx, inv, opts, &left, &right)
}

func diffComparePrefix(opts *diffOptions) string {
	var b strings.Builder
	b.WriteString("diff")
	if opts.recursive {
		b.WriteString(" -r")
	}
	if opts.newFile {
		b.WriteString(" -N")
	} else if opts.unidirectionalNew {
		b.WriteString(" --unidirectional-new-file")
	}
	switch opts.mode {
	case diffModeContext:
		if opts.contextLines == 3 {
			b.WriteString(" -c")
		} else {
			fmt.Fprintf(&b, " -C %d", opts.contextLines)
		}
	case diffModeUnified:
		if opts.contextLines == 3 {
			b.WriteString(" -u")
		} else {
			fmt.Fprintf(&b, " -U %d", opts.contextLines)
		}
	case diffModeEd:
		b.WriteString(" -e")
	case diffModeRCS:
		b.WriteString(" -n")
	case diffModeSideBySide:
		b.WriteString(" -y")
	}
	return b.String()
}

func diffStatMaybe(ctx context.Context, inv *Invocation, noDeref bool, name string) (stdfs.FileInfo, string, bool, error) {
	if noDeref {
		return lstatMaybe(ctx, inv, name)
	}
	return statMaybe(ctx, inv, name)
}

func diffDirEntryInfo(ctx context.Context, inv *Invocation, abs string, entry stdfs.DirEntry, noDeref bool) (stdfs.FileInfo, error) {
	if noDeref {
		return inv.FS.Lstat(ctx, abs)
	}
	if info, err := entry.Info(); err == nil {
		return info, nil
	}
	return inv.FS.Stat(ctx, abs)
}

func diffFileNameKey(name string, fold bool) string {
	if !fold {
		return name
	}
	return strings.ToLower(name)
}

func diffUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func diffFilterStartingFile(values []string, start string) []string {
	i := sort.SearchStrings(values, start)
	if i >= len(values) {
		return nil
	}
	return values[i:]
}

func diffExcluded(name string, patterns []string) bool {
	for _, pattern := range patterns {
		ok, err := path.Match(pattern, name)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func diffLoadInput(ctx context.Context, inv *Invocation, opts *diffOptions, loader *diffLoader, name string, missingAsEmpty bool) (diffInput, error) {
	if name == "-" {
		data, err := loader.loadStdin(ctx, inv)
		if err != nil {
			return diffInput{}, err
		}
		return diffBuildInput(name, "-", nil, true, false, data, opts), nil
	}

	info, abs, exists, err := diffStatMaybe(ctx, inv, opts.noDereference, name)
	if err != nil {
		return diffInput{}, err
	}
	if !exists {
		if missingAsEmpty {
			return diffBuildInput(name, allowPath(inv, name), nil, false, false, nil, opts), nil
		}
		return diffInput{}, stdfs.ErrNotExist
	}
	if info != nil && info.IsDir() {
		return diffInput{name: name, abs: abs, exists: true, isDir: true, info: info}, nil
	}

	if info != nil && opts.noDereference && info.Mode()&stdfs.ModeSymlink != 0 {
		target, err := inv.FS.Readlink(ctx, abs)
		if err != nil {
			return diffInput{}, err
		}
		return diffBuildInput(name, abs, info, true, true, []byte(target), opts), nil
	}

	data, _, err := readAllFile(ctx, inv, name)
	if err != nil {
		return diffInput{}, err
	}
	return diffBuildInput(name, abs, info, true, false, data, opts), nil
}

func (l *diffLoader) loadStdin(ctx context.Context, inv *Invocation) ([]byte, error) {
	if l.stdinLoaded {
		return l.stdinData, nil
	}
	data, err := readAllStdin(ctx, inv)
	if err != nil {
		return nil, err
	}
	l.stdinData = data
	l.stdinLoaded = true
	return data, nil
}

func diffBuildInput(name, abs string, info stdfs.FileInfo, exists, symlink bool, data []byte, opts *diffOptions) diffInput {
	processed := append([]byte(nil), data...)
	if opts.stripTrailingCR {
		processed = bytes.ReplaceAll(processed, []byte("\r\n"), []byte("\n"))
	}
	hasTrailingNewline := len(processed) > 0 && processed[len(processed)-1] == '\n'
	binary := bytes.IndexByte(processed, 0) >= 0

	var lines []diffLine
	lineNo := 1
	visitTextLines(processed, func(line []byte, _ int) bool {
		text := string(line)
		if opts.ignoreBlankLines && strings.TrimSpace(text) == "" {
			lineNo++
			return true
		}
		if diffMatchesIgnorePattern(text, opts.ignoreMatching) {
			lineNo++
			return true
		}
		lines = append(lines, diffLine{
			text:   text,
			key:    diffNormalizeLine(text, opts),
			lineNo: lineNo,
		})
		lineNo++
		return true
	})

	totalLines := 0
	if len(processed) > 0 {
		totalLines = bytes.Count(processed, []byte{'\n'})
		if !hasTrailingNewline {
			totalLines++
		}
	}
	return diffInput{
		name:               name,
		abs:                abs,
		exists:             exists,
		isSymlink:          symlink,
		info:               info,
		data:               processed,
		lines:              lines,
		totalLines:         totalLines,
		hasTrailingNewline: hasTrailingNewline,
		binary:             binary,
	}
}

func diffMatchesIgnorePattern(text string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

func diffNormalizeLine(line string, opts *diffOptions) string {
	if opts.ignoreTabExpansion {
		line = diffExpandTabs(line, opts.tabSize)
	}
	switch {
	case opts.ignoreAllSpace:
		line = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, line)
	case opts.ignoreSpaceChange:
		line = diffCollapseHorizontalSpace(line)
	case opts.ignoreTrailingSpace:
		line = strings.TrimRight(line, " \t")
	}
	if opts.ignoreCase {
		line = strings.ToLower(line)
	}
	return line
}

func diffCollapseHorizontalSpace(line string) string {
	var b strings.Builder
	seenSpace := false
	for _, r := range line {
		if r == ' ' || r == '\t' {
			seenSpace = true
			continue
		}
		if seenSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		seenSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

func diffRenderPair(ctx context.Context, inv *Invocation, opts *diffOptions, left, right *diffInput) (int, error) {
	if left.binary || right.binary {
		if !opts.text {
			if bytes.Equal(left.data, right.data) {
				if opts.reportSame {
					if _, err := fmt.Fprintf(inv.Stdout, "Files %s and %s are identical\n", left.name, right.name); err != nil {
						return 0, diffWriteError(err)
					}
				}
				return 0, nil
			}
			if _, err := fmt.Fprintf(inv.Stdout, "Binary files %s and %s differ\n", left.name, right.name); err != nil {
				return 0, diffWriteError(err)
			}
			return 1, nil
		}
	}

	if diffEqualLines(left.lines, right.lines) {
		if opts.reportSame {
			if _, err := fmt.Fprintf(inv.Stdout, "Files %s and %s are identical\n", left.name, right.name); err != nil {
				return 0, diffWriteError(err)
			}
		}
		return 0, nil
	}
	if opts.brief {
		if _, err := fmt.Fprintf(inv.Stdout, "Files %s and %s differ\n", left.name, right.name); err != nil {
			return 0, diffWriteError(err)
		}
		return 1, nil
	}

	units := buildDiffUnits(left.lines, right.lines)
	changes := diffCollectChanges(units)

	var out strings.Builder
	switch opts.mode {
	case diffModeNormal:
		diffWriteNormal(&out, left, right, changes, opts)
	case diffModeContext:
		diffWriteContext(&out, left, right, units, changes, opts)
	case diffModeUnified:
		diffWriteUnified(&out, left, right, units, changes, opts)
	case diffModeEd:
		diffWriteEd(&out, changes)
	case diffModeRCS:
		diffWriteRCS(&out, changes)
	case diffModeSideBySide:
		diffWriteSideBySide(&out, units, opts)
	case diffModeIfdef:
		diffWriteIfdef(&out, left, units, changes, opts)
	case diffModeCustom:
		diffWriteCustom(&out, units, changes, opts)
	default:
		diffWriteNormal(&out, left, right, changes, opts)
	}

	if opts.paginate {
		if err := diffWritePaginated(ctx, inv, out.String()); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if _, err := io.WriteString(inv.Stdout, out.String()); err != nil {
		return 0, diffWriteError(err)
	}
	return 1, nil
}

func diffWritePaginated(ctx context.Context, inv *Invocation, text string) error {
	if inv.Exec == nil {
		_, err := io.WriteString(inv.Stdout, text)
		return diffWriteError(err)
	}
	result, err := inv.Exec(ctx, &ExecutionRequest{
		Command: []string{"pr"},
		Stdin:   strings.NewReader(text),
	})
	if err != nil {
		_, writeErr := io.WriteString(inv.Stdout, text)
		if writeErr != nil {
			return diffWriteError(writeErr)
		}
		return nil
	}
	if _, err := io.WriteString(inv.Stdout, result.Stdout); err != nil {
		return diffWriteError(err)
	}
	return nil
}

func diffEqualLines(left, right []diffLine) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].key != right[i].key {
			return false
		}
	}
	return true
}

func buildDiffUnits(left, right []diffLine) []diffUnit {
	dp := make([][]int, len(left)+1)
	for i := range dp {
		dp[i] = make([]int, len(right)+1)
	}
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if left[i].key == right[j].key {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			dp[i][j] = max(dp[i+1][j], dp[i][j+1])
		}
	}

	units := make([]diffUnit, 0, len(left)+len(right))
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		switch {
		case left[i].key == right[j].key:
			units = append(units, diffUnit{kind: diffOpEqual, leftPos: i, rightPos: j, left: &left[i], right: &right[j]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			units = append(units, diffUnit{kind: diffOpDelete, leftPos: i, rightPos: j, left: &left[i]})
			i++
		default:
			units = append(units, diffUnit{kind: diffOpInsert, leftPos: i, rightPos: j, right: &right[j]})
			j++
		}
	}
	for ; i < len(left); i++ {
		units = append(units, diffUnit{kind: diffOpDelete, leftPos: i, rightPos: j, left: &left[i]})
	}
	for ; j < len(right); j++ {
		units = append(units, diffUnit{kind: diffOpInsert, leftPos: i, rightPos: j, right: &right[j]})
	}
	return units
}

func diffCollectChanges(units []diffUnit) []diffChange {
	changes := make([]diffChange, 0)
	start := -1
	for i, unit := range units {
		if unit.kind == diffOpEqual {
			if start >= 0 {
				changes = append(changes, diffChange{units: append([]diffUnit(nil), units[start:i]...)})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		changes = append(changes, diffChange{units: append([]diffUnit(nil), units[start:]...)})
	}
	return changes
}

func diffChangeLeftLines(change diffChange) []*diffLine {
	lines := make([]*diffLine, 0, len(change.units))
	for _, unit := range change.units {
		if unit.left != nil {
			lines = append(lines, unit.left)
		}
	}
	return lines
}

func diffChangeRightLines(change diffChange) []*diffLine {
	lines := make([]*diffLine, 0, len(change.units))
	for _, unit := range change.units {
		if unit.right != nil {
			lines = append(lines, unit.right)
		}
	}
	return lines
}

func diffRange(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, end)
}

func diffWriteNormal(out *strings.Builder, left, right *diffInput, changes []diffChange, opts *diffOptions) {
	for _, change := range changes {
		oldLines := diffChangeLeftLines(change)
		newLines := diffChangeRightLines(change)

		leftRef := 0
		rightRef := 0
		if len(oldLines) > 0 {
			leftRef = oldLines[0].lineNo
		} else if len(change.units) > 0 {
			leftRef = change.units[0].leftPos
		}
		if len(newLines) > 0 {
			rightRef = newLines[0].lineNo
		} else if len(change.units) > 0 {
			rightRef = change.units[0].rightPos
		}

		switch {
		case len(oldLines) == 0:
			fmt.Fprintf(out, "%da%s\n", leftRef, diffRange(newLines[0].lineNo, newLines[len(newLines)-1].lineNo))
			for _, line := range newLines {
				diffWritePrefixedLine(out, "> ", right, line, opts)
			}
		case len(newLines) == 0:
			fmt.Fprintf(out, "%sd%d\n", diffRange(oldLines[0].lineNo, oldLines[len(oldLines)-1].lineNo), rightRef)
			for _, line := range oldLines {
				diffWritePrefixedLine(out, "< ", left, line, opts)
			}
		default:
			fmt.Fprintf(out, "%sc%s\n", diffRange(oldLines[0].lineNo, oldLines[len(oldLines)-1].lineNo), diffRange(newLines[0].lineNo, newLines[len(newLines)-1].lineNo))
			for _, line := range oldLines {
				diffWritePrefixedLine(out, "< ", left, line, opts)
			}
			out.WriteString("---\n")
			for _, line := range newLines {
				diffWritePrefixedLine(out, "> ", right, line, opts)
			}
		}
	}
}

func diffWriteUnified(out *strings.Builder, left, right *diffInput, units []diffUnit, changes []diffChange, opts *diffOptions) {
	fmt.Fprintf(out, "--- %s\n", diffHeaderLabel(left, opts, 0))
	fmt.Fprintf(out, "+++ %s\n", diffHeaderLabel(right, opts, 1))
	for _, hunk := range diffBuildHunks(units, changes, opts.contextLines) {
		oldStart, oldCount := diffUnifiedRange(hunk.units, true)
		newStart, newCount := diffUnifiedRange(hunk.units, false)
		fmt.Fprintf(out, "@@ -%s +%s @@%s\n", diffUnifiedRangeText(oldStart, oldCount), diffUnifiedRangeText(newStart, newCount), diffFunctionHeader(left, hunk.units, opts))
		for _, unit := range hunk.units {
			switch unit.kind {
			case diffOpEqual:
				diffWritePrefixedLine(out, " ", left, unit.left, opts)
			case diffOpDelete:
				diffWritePrefixedLine(out, "-", left, unit.left, opts)
			case diffOpInsert:
				diffWritePrefixedLine(out, "+", right, unit.right, opts)
			}
		}
	}
}

func diffWriteContext(out *strings.Builder, left, right *diffInput, units []diffUnit, changes []diffChange, opts *diffOptions) {
	fmt.Fprintf(out, "*** %s\n", diffHeaderLabel(left, opts, 0))
	fmt.Fprintf(out, "--- %s\n", diffHeaderLabel(right, opts, 1))
	for _, hunk := range diffBuildHunks(units, changes, opts.contextLines) {
		hasDelete := false
		hasInsert := false
		for _, unit := range hunk.units {
			switch unit.kind {
			case diffOpDelete:
				hasDelete = true
			case diffOpInsert:
				hasInsert = true
			}
		}
		deletePrefix := "- "
		insertPrefix := "+ "
		if hasDelete && hasInsert {
			deletePrefix = "! "
			insertPrefix = "! "
		}
		fmt.Fprintln(out, "***************")
		oldStart, oldCount := diffUnifiedRange(hunk.units, true)
		newStart, newCount := diffUnifiedRange(hunk.units, false)
		fmt.Fprintf(out, "*** %s ****\n", diffContextRangeText(oldStart, oldCount))
		for _, unit := range hunk.units {
			switch unit.kind {
			case diffOpEqual:
				diffWritePrefixedLine(out, "  ", left, unit.left, opts)
			case diffOpDelete:
				diffWritePrefixedLine(out, deletePrefix, left, unit.left, opts)
			}
		}
		fmt.Fprintf(out, "--- %s ----\n", diffContextRangeText(newStart, newCount))
		for _, unit := range hunk.units {
			switch unit.kind {
			case diffOpEqual:
				diffWritePrefixedLine(out, "  ", right, unit.right, opts)
			case diffOpInsert:
				diffWritePrefixedLine(out, insertPrefix, right, unit.right, opts)
			}
		}
	}
}

func diffBuildHunks(units []diffUnit, changes []diffChange, contextLines int) []diffHunk {
	if len(changes) == 0 {
		return nil
	}
	changeBounds := make([][2]int, 0, len(changes))
	cursor := 0
	for range changes {
		start := -1
		for i := cursor; i < len(units); i++ {
			if units[i].kind != diffOpEqual {
				start = i
				break
			}
		}
		if start < 0 {
			break
		}
		end := start
		for end < len(units) && units[end].kind != diffOpEqual {
			end++
		}
		changeBounds = append(changeBounds, [2]int{start, end})
		cursor = end
	}
	if len(changeBounds) == 0 {
		return nil
	}

	hunks := make([]diffHunk, 0, len(changeBounds))
	start := max(0, diffBackEqual(units, changeBounds[0][0], contextLines))
	end := diffForwardEqual(units, changeBounds[0][1], contextLines)
	for i := 1; i < len(changeBounds); i++ {
		nextStart := max(0, diffBackEqual(units, changeBounds[i][0], contextLines))
		nextEnd := diffForwardEqual(units, changeBounds[i][1], contextLines)
		if nextStart <= end {
			end = nextEnd
			continue
		}
		hunks = append(hunks, diffHunk{units: append([]diffUnit(nil), units[start:end]...)})
		start = nextStart
		end = nextEnd
	}
	hunks = append(hunks, diffHunk{units: append([]diffUnit(nil), units[start:end]...)})
	return hunks
}

func diffBackEqual(units []diffUnit, idx, count int) int {
	for idx > 0 && count > 0 {
		if units[idx-1].kind != diffOpEqual {
			break
		}
		idx--
		count--
	}
	return idx
}

func diffForwardEqual(units []diffUnit, idx, count int) int {
	for idx < len(units) && count > 0 {
		if units[idx].kind != diffOpEqual {
			break
		}
		idx++
		count--
	}
	return idx
}

func diffUnifiedRange(units []diffUnit, old bool) (int, int) {
	count := 0
	start := 0
	found := false
	for _, unit := range units {
		var line *diffLine
		if old {
			line = unit.left
		} else {
			line = unit.right
		}
		if line != nil {
			if !found {
				start = line.lineNo
				found = true
			}
			count++
		}
	}
	if found {
		return start, count
	}
	if len(units) == 0 {
		return 0, 0
	}
	if old {
		return units[0].leftPos, 0
	}
	return units[0].rightPos, 0
}

func diffUnifiedRangeText(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func diffContextRangeText(start, count int) string {
	if count <= 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, start+count-1)
}

func diffHeaderLabel(in *diffInput, opts *diffOptions, idx int) string {
	if in == nil {
		return ""
	}
	if idx < len(opts.labels) {
		return opts.labels[idx]
	}
	if in.info == nil {
		return in.name
	}
	return fmt.Sprintf("%s\t%s", in.name, in.info.ModTime().Format("2006-01-02 15:04:05.000000000 -0700"))
}

func diffFunctionHeader(left *diffInput, units []diffUnit, opts *diffOptions) string {
	if left == nil {
		return ""
	}
	re := opts.showFunctionRegexp
	if re == nil {
		return ""
	}
	startLine := 0
	for _, unit := range units {
		if unit.left != nil {
			startLine = unit.left.lineNo
			break
		}
	}
	match := ""
	for _, line := range left.lines {
		if line.lineNo >= startLine {
			break
		}
		if re.MatchString(line.text) {
			match = line.text
		}
	}
	if match == "" {
		return ""
	}
	if len(match) > 40 && opts.showCFunction {
		match = match[:40]
	}
	return " " + match
}

func diffWriteEd(out *strings.Builder, changes []diffChange) {
	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		oldLines := diffChangeLeftLines(change)
		newLines := diffChangeRightLines(change)
		switch {
		case len(oldLines) == 0:
			fmt.Fprintf(out, "%da\n", change.units[0].leftPos)
			for _, line := range newLines {
				out.WriteString(line.text)
				out.WriteByte('\n')
			}
			out.WriteString(".\n")
		case len(newLines) == 0:
			fmt.Fprintf(out, "%sd\n", diffRange(oldLines[0].lineNo, oldLines[len(oldLines)-1].lineNo))
		default:
			fmt.Fprintf(out, "%sc\n", diffRange(oldLines[0].lineNo, oldLines[len(oldLines)-1].lineNo))
			for _, line := range newLines {
				out.WriteString(line.text)
				out.WriteByte('\n')
			}
			out.WriteString(".\n")
		}
	}
}

func diffWriteRCS(out *strings.Builder, changes []diffChange) {
	for _, change := range changes {
		oldLines := diffChangeLeftLines(change)
		newLines := diffChangeRightLines(change)
		if len(oldLines) > 0 {
			fmt.Fprintf(out, "d%d %d\n", oldLines[0].lineNo, len(oldLines))
		}
		if len(newLines) > 0 {
			insertAfter := 0
			if len(oldLines) > 0 {
				insertAfter = oldLines[len(oldLines)-1].lineNo
			} else {
				insertAfter = change.units[0].leftPos
			}
			fmt.Fprintf(out, "a%d %d\n", insertAfter, len(newLines))
			for _, line := range newLines {
				out.WriteString(line.text)
				out.WriteByte('\n')
			}
		}
	}
}

func diffWriteSideBySide(out *strings.Builder, units []diffUnit, opts *diffOptions) {
	leftWidth := max((opts.width-7)/2, 1)
	for _, unit := range units {
		switch unit.kind {
		case diffOpEqual:
			if opts.suppressCommonLines {
				continue
			}
			leftText := diffClipOutput(unit.left.text, leftWidth, opts)
			rightText := diffClipOutput(unit.right.text, leftWidth, opts)
			sep := " "
			if opts.leftColumn {
				rightText = ""
			}
			fmt.Fprintf(out, "%-*s %s %s\n", leftWidth, leftText, sep, rightText)
		case diffOpDelete:
			leftText := diffClipOutput(unit.left.text, leftWidth, opts)
			fmt.Fprintf(out, "%-*s <\n", leftWidth, leftText)
		case diffOpInsert:
			rightText := diffClipOutput(unit.right.text, leftWidth, opts)
			fmt.Fprintf(out, "%-*s > %s\n", leftWidth, "", rightText)
		}
	}
}

func diffWriteIfdef(out *strings.Builder, left *diffInput, units []diffUnit, changes []diffChange, opts *diffOptions) {
	_ = left
	changeIdx := 0
	for i := 0; i < len(units); {
		if units[i].kind == diffOpEqual {
			out.WriteString(units[i].left.text)
			out.WriteByte('\n')
			i++
			continue
		}
		change := changes[changeIdx]
		changeIdx++
		oldLines := diffChangeLeftLines(change)
		newLines := diffChangeRightLines(change)
		switch {
		case len(oldLines) > 0 && len(newLines) > 0:
			fmt.Fprintf(out, "#ifndef %s\n", opts.ifdefName)
			for _, line := range oldLines {
				out.WriteString(line.text)
				out.WriteByte('\n')
			}
			fmt.Fprintf(out, "#else /* %s */\n", opts.ifdefName)
			for _, line := range newLines {
				out.WriteString(line.text)
				out.WriteByte('\n')
			}
			fmt.Fprintf(out, "#endif /* %s */\n", opts.ifdefName)
		case len(oldLines) > 0:
			fmt.Fprintf(out, "#ifndef %s\n", opts.ifdefName)
			for _, line := range oldLines {
				out.WriteString(line.text)
				out.WriteByte('\n')
			}
			fmt.Fprintf(out, "#endif /* %s */\n", opts.ifdefName)
		case len(newLines) > 0:
			fmt.Fprintf(out, "#ifdef %s\n", opts.ifdefName)
			for _, line := range newLines {
				out.WriteString(line.text)
				out.WriteByte('\n')
			}
			fmt.Fprintf(out, "#endif /* %s */\n", opts.ifdefName)
		}
		i += len(change.units)
	}
}

func diffWriteCustom(out *strings.Builder, units []diffUnit, changes []diffChange, opts *diffOptions) {
	if len(changes) == 0 {
		for _, unit := range units {
			if unit.left != nil {
				out.WriteString(diffApplyLineFormat(opts, "unchanged", unit.left))
			}
		}
		return
	}
	var group []diffUnit
	flush := func(kind string) {
		if len(group) == 0 {
			return
		}
		out.WriteString(diffApplyGroupFormat(opts, kind, group))
		group = group[:0]
	}
	currentKind := "unchanged"
	for _, unit := range units {
		kind := "unchanged"
		switch unit.kind {
		case diffOpDelete:
			kind = "old"
		case diffOpInsert:
			kind = "new"
		}
		if len(group) > 0 && kind != currentKind && (currentKind != "old" || kind != "new") {
			flush(currentKind)
		}
		if len(group) == 0 {
			currentKind = kind
		}
		group = append(group, unit)
		if currentKind == "old" && kind == "new" {
			currentKind = "changed"
		}
	}
	flush(currentKind)
}

func diffApplyGroupFormat(opts *diffOptions, kind string, units []diffUnit) string {
	if format := opts.groupFormats[kind]; format != "" {
		return diffFormatGroup(format, kind, units)
	}
	var b strings.Builder
	switch kind {
	case "unchanged":
		for _, unit := range units {
			if unit.left != nil {
				b.WriteString(diffApplyLineFormat(opts, "unchanged", unit.left))
			}
		}
	case "old", "changed":
		for _, unit := range units {
			if unit.left != nil {
				b.WriteString(diffApplyLineFormat(opts, "old", unit.left))
			}
		}
		if kind == "changed" {
			for _, unit := range units {
				if unit.right != nil {
					b.WriteString(diffApplyLineFormat(opts, "new", unit.right))
				}
			}
		}
	case "new":
		for _, unit := range units {
			if unit.right != nil {
				b.WriteString(diffApplyLineFormat(opts, "new", unit.right))
			}
		}
	}
	return b.String()
}

func diffApplyLineFormat(opts *diffOptions, kind string, line *diffLine) string {
	if line == nil {
		return ""
	}
	format := opts.lineFormat
	if v := opts.lineFormats[kind]; v != "" {
		format = v
	}
	if format == "" {
		return line.text + "\n"
	}
	return diffFormatLine(format, line)
}

func diffFormatLine(format string, line *diffLine) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case '%':
			b.WriteByte('%')
		case 'L':
			b.WriteString(line.text)
			b.WriteByte('\n')
		case 'l':
			b.WriteString(line.text)
		case 'n':
			b.WriteString(strconv.Itoa(line.lineNo))
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}
	return b.String()
}

func diffFormatGroup(format, kind string, units []diffUnit) string {
	var oldLines, newLines, sameLines []string
	var oldFirst, oldLast, newFirst, newLast int
	for _, unit := range units {
		if unit.left != nil {
			oldLines = append(oldLines, unit.left.text+"\n")
			if oldFirst == 0 {
				oldFirst = unit.left.lineNo
			}
			oldLast = unit.left.lineNo
		}
		if unit.right != nil {
			newLines = append(newLines, unit.right.text+"\n")
			if newFirst == 0 {
				newFirst = unit.right.lineNo
			}
			newLast = unit.right.lineNo
		}
		if unit.kind == diffOpEqual && unit.left != nil {
			sameLines = append(sameLines, unit.left.text+"\n")
		}
	}
	letters := map[byte]int{
		'f': oldFirst,
		'l': oldLast,
		'n': max(0, oldLast-oldFirst+1),
		'e': oldFirst - 1,
		'm': oldLast + 1,
		'F': newFirst,
		'L': newLast,
		'N': max(0, newLast-newFirst+1),
		'E': newFirst - 1,
		'M': newLast + 1,
	}
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case '%':
			b.WriteByte('%')
		case '<':
			b.WriteString(strings.Join(oldLines, ""))
		case '>':
			b.WriteString(strings.Join(newLines, ""))
		case '=':
			b.WriteString(strings.Join(sameLines, ""))
		default:
			if value, ok := letters[format[i]]; ok {
				b.WriteString(strconv.Itoa(value))
				continue
			}
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}
	_ = kind
	return b.String()
}

func diffWritePrefixedLine(out *strings.Builder, prefix string, input *diffInput, line *diffLine, opts *diffOptions) {
	if line == nil {
		return
	}
	text := diffPrepareOutputText(line.text, opts)
	out.WriteString(prefix)
	out.WriteString(text)
	out.WriteByte('\n')
	if input != nil && input.totalLines > 0 && !input.hasTrailingNewline && line.lineNo == input.totalLines {
		out.WriteString("\\ No newline at end of file\n")
	}
}

func diffPrepareOutputText(text string, opts *diffOptions) string {
	if opts.expandTabs {
		text = diffExpandTabs(text, opts.tabSize)
	}
	if opts.initialTab {
		text = "\t" + text
	}
	if opts.suppressBlankEmpty && text == "" {
		return ""
	}
	return text
}

func diffExpandTabs(text string, size int) string {
	if size <= 0 {
		size = 8
	}
	var b strings.Builder
	col := 0
	for _, r := range text {
		if r != '\t' {
			b.WriteRune(r)
			col++
			continue
		}
		spaces := size - (col % size)
		if spaces == 0 {
			spaces = size
		}
		for i := 0; i < spaces; i++ {
			b.WriteByte(' ')
		}
		col += spaces
	}
	return b.String()
}

func diffClipOutput(text string, width int, opts *diffOptions) string {
	text = diffPrepareOutputText(text, opts)
	if width <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return string(runes[:width])
}

func diffWriteError(err error) error {
	if err == nil {
		return nil
	}
	return &ExitError{Code: 1, Err: err}
}

const diffHelpText = `Usage: diff [OPTION]... FILES
Compare FILES line by line.

Mandatory arguments to long options are mandatory for short options too.
      --normal                  output a normal diff (the default)
  -q, --brief                   report only when files differ
  -s, --report-identical-files  report when two files are the same
  -c, -C NUM, --context[=NUM]   output NUM (default 3) lines of copied context
  -u, -U NUM, --unified[=NUM]   output NUM (default 3) lines of unified context
  -e, --ed                      output an ed script
  -n, --rcs                     output an RCS format diff
  -y, --side-by-side            output in two columns
  -W, --width=NUM               output at most NUM (default 130) print columns
      --left-column             output only the left column of common lines
      --suppress-common-lines   do not output common lines

  -p, --show-c-function         show which C function each change is in
  -F, --show-function-line=RE   show the most recent line matching RE
      --label LABEL             use LABEL instead of file name and timestamp
                                  (can be repeated)

  -t, --expand-tabs             expand tabs to spaces in output
  -T, --initial-tab             make tabs line up by prepending a tab
      --tabsize=NUM             tab stops every NUM (default 8) print columns
      --suppress-blank-empty    suppress space or tab before empty output lines
  -l, --paginate                pass output through 'pr' to paginate it

  -r, --recursive                 recursively compare any subdirectories found
      --no-dereference            don't follow symbolic links
  -N, --new-file                  treat absent files as empty
      --unidirectional-new-file   treat absent first files as empty
      --ignore-file-name-case     ignore case when comparing file names
      --no-ignore-file-name-case  consider case when comparing file names
  -x, --exclude=PAT               exclude files that match PAT
  -X, --exclude-from=FILE         exclude files that match any pattern in FILE
  -S, --starting-file=FILE        start with FILE when comparing directories
      --from-file=FILE1           compare FILE1 to all operands;
                                    FILE1 can be a directory
      --to-file=FILE2             compare all operands to FILE2;
                                    FILE2 can be a directory

  -i, --ignore-case               ignore case differences in file contents
  -E, --ignore-tab-expansion      ignore changes due to tab expansion
  -Z, --ignore-trailing-space     ignore white space at line end
  -b, --ignore-space-change       ignore changes in the amount of white space
  -w, --ignore-all-space          ignore all white space
  -B, --ignore-blank-lines        ignore changes where lines are all blank
  -I, --ignore-matching-lines=RE  ignore changes where all lines match RE

  -a, --text                      treat all files as text
      --strip-trailing-cr         strip trailing carriage return on input

  -D, --ifdef=NAME                output merged file with '#ifdef NAME' diffs
      --GTYPE-group-format=GFMT   format GTYPE input groups with GFMT
      --line-format=LFMT          format all input lines with LFMT
      --LTYPE-line-format=LFMT    format LTYPE input lines with LFMT
    These format options provide fine-grained control over the output
      of diff, generalizing -D/--ifdef.
    LTYPE is 'old', 'new', or 'unchanged'.  GTYPE is LTYPE or 'changed'.
    GFMT (only) may contain:
      %<  lines from FILE1
      %>  lines from FILE2
      %=  lines common to FILE1 and FILE2
      %[-][WIDTH][.[PREC]]{doxX}LETTER  printf-style spec for LETTER
        LETTERs are as follows for new group, lower case for old group:
          F  first line number
          L  last line number
          N  number of lines = L-F+1
          E  F-1
          M  L+1
      %(A=B?T:E)  if A equals B then T else E
    LFMT (only) may contain:
      %L  contents of line
      %l  contents of line, excluding any trailing newline
      %[-][WIDTH][.[PREC]]{doxX}n  printf-style spec for input line number
    Both GFMT and LFMT may contain:
      %%  %
      %c'C'  the single character C
      %c'\OOO'  the character with octal code OOO
      C    the character C (other characters represent themselves)

  -d, --minimal            try hard to find a smaller set of changes
      --horizon-lines=NUM  keep NUM lines of the common prefix and suffix
      --speed-large-files  assume large files and many scattered small changes
      --color[=WHEN]       color output; WHEN is 'never', 'always', or 'auto';
                             plain --color means --color='auto'
      --palette=PALETTE    the colors to use when --color is active; PALETTE is
                             a colon-separated list of terminfo capabilities

      --help               display this help and exit
  -v, --version            output version information and exit

FILES are 'FILE1 FILE2' or 'DIR1 DIR2' or 'DIR FILE' or 'FILE DIR'.
If --from-file or --to-file is given, there are no restrictions on FILE(s).
If a FILE is '-', read standard input.
Exit status is 0 if inputs are the same, 1 if different, 2 if trouble.

Report bugs to: bug-diffutils@gnu.org
GNU diffutils home page: <https://www.gnu.org/software/diffutils/>
General help using GNU software: <https://www.gnu.org/gethelp/>
`

const diffVersionText = `diff (GNU diffutils) 3.12
Copyright (C) 2025 Free Software Foundation, Inc.
License GPLv3+: GNU GPL version 3 or later <https://gnu.org/licenses/gpl.html>.
This is free software: you are free to change and redistribute it.
There is NO WARRANTY, to the extent permitted by law.

Written by Paul Eggert, Mike Haertel, David Hayes,
Richard Stallman, and Len Tower.
`

var _ Command = (*Diff)(nil)
