package builtins

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"

	"github.com/ewhauser/gbash/internal/commandutil"
)

type DU struct{}

type duCountMode uint8

const (
	duCountAllocated duCountMode = iota
	duCountApparent
	duCountInodes
)

type duHumanMode uint8

const (
	duHumanNone duHumanMode = iota
	duHumanBinary
	duHumanSI
)

type duSymlinkMode uint8

const (
	duFollowNone duSymlinkMode = iota
	duFollowArgs
	duFollowAll
)

type duOptions struct {
	showAll          bool
	summary          bool
	total            bool
	separateDirs     bool
	countLinks       bool
	oneFileSystem    bool
	maxDepth         int
	hasMaxDepth      bool
	countMode        duCountMode
	humanMode        duHumanMode
	blockSize        int64
	followMode       duSymlinkMode
	excludePatterns  []string
	files0From       string
	threshold        duThreshold
	warnBytes        bool
	warnApparentSize bool
}

type duThreshold struct {
	set      bool
	negative bool
	value    int64
}

type duWalkState struct {
	opts            duOptions
	rootDevice      uint64
	rootDeviceKnown bool
	exitCode        int
	seenFiles       map[fileInfoIdentityKey]struct{}
	seenDirs        map[fileInfoIdentityKey]struct{}
	activeDirs      map[fileInfoIdentityKey]struct{}
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
		Usage: "du [OPTION]... [FILE]...\n  or:  du [OPTION]... --files0-from=F",
		Options: []OptionSpec{
			{Name: "all", Short: 'a', Long: "all", Help: "write counts for all files, not just directories"},
			{Name: "apparent-size", Short: 'A', Long: "apparent-size", Help: "print apparent sizes rather than device usage"},
			{Name: "bytes", Short: 'b', Help: "equivalent to '--apparent-size --block-size=1'"},
			{Name: "block-size", Short: 'B', Long: "block-size", Arity: OptionRequiredValue, ValueName: "SIZE", Help: "scale sizes by SIZE before printing them"},
			{Name: "total", Short: 'c', Long: "total", Help: "produce a grand total"},
			{Name: "max-depth", Short: 'd', Long: "max-depth", Arity: OptionRequiredValue, ValueName: "N", Help: "print the total for a directory only if it is N or fewer levels below the command line argument"},
			{Name: "dereference-args", Short: 'D', Long: "dereference-args", Help: "dereference only symbolic links listed on the command line"},
			{Name: "exclude", Long: "exclude", Arity: OptionRequiredValue, ValueName: "PATTERN", Repeatable: true, Help: "exclude files that match PATTERN"},
			{Name: "exclude-from", Long: "exclude-from", Arity: OptionRequiredValue, ValueName: "FILE", Repeatable: true, Help: "exclude files that match any pattern in FILE"},
			{Name: "files0-from", Long: "files0-from", Arity: OptionRequiredValue, ValueName: "F", Help: "summarize files specified by NUL-terminated names in file F"},
			{Name: "human-readable", Short: 'h', Long: "human-readable", Help: "print sizes in human readable format"},
			{Name: "inodes", Long: "inodes", Help: "list inode usage information instead of block usage"},
			{Name: "kibibytes", Short: 'k', Help: "like --block-size=1K"},
			{Name: "dereference", Short: 'L', Long: "dereference", Help: "dereference all symbolic links"},
			{Name: "count-links", Short: 'l', Long: "count-links", Help: "count sizes many times if hard linked"},
			{Name: "separate-dirs", Short: 'S', Long: "separate-dirs", Help: "for directories do not include size of subdirectories"},
			{Name: "summarize", Short: 's', Long: "summarize", Help: "display only a total for each argument"},
			{Name: "si", Long: "si", Help: "like -h, but use powers of 1000 not 1024"},
			{Name: "threshold", Short: 't', Long: "threshold", Arity: OptionRequiredValue, ValueName: "SIZE", Help: "exclude entries smaller than SIZE if positive, or entries greater than SIZE if negative"},
			{Name: "one-file-system", Short: 'x', Long: "one-file-system", Help: "skip directories on different file systems"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *DU) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseDUMatches(ctx, inv, matches)
	if err != nil {
		return err
	}

	targets, targetExitCode, err := duTargets(ctx, inv, &opts, matches.Args("file"))
	if err != nil {
		return err
	}
	if opts.files0From == "" && len(targets) == 0 {
		targets = []string{"."}
	}

	if opts.countMode == duCountInodes {
		if opts.warnBytes {
			if err := duWriteMessage(inv.Stderr, "du: warning: -b is ineffective with --inodes"); err != nil {
				return err
			}
		}
		if opts.warnApparentSize {
			if err := duWriteMessage(inv.Stderr, "du: warning: --apparent-size is ineffective with --inodes"); err != nil {
				return err
			}
		}
	}

	exitCode := targetExitCode
	var grandTotal int64
	state := &duWalkState{
		opts:      opts,
		seenFiles: make(map[fileInfoIdentityKey]struct{}),
		seenDirs:  make(map[fileInfoIdentityKey]struct{}),
	}
	for _, target := range targets {
		total, rootExitCode, err := c.walkRoot(ctx, inv, state, target)
		if err != nil {
			return err
		}
		grandTotal += total
		if rootExitCode > exitCode {
			exitCode = rootExitCode
		}
	}

	if opts.total && len(targets) > 0 {
		if err := duWriteLine(inv.Stdout, duFormatCount(grandTotal, &opts), "total"); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseDUMatches(ctx context.Context, inv *Invocation, matches *ParsedCommand) (duOptions, error) {
	opts := duOptions{
		blockSize: 1024,
	}
	for _, occurrence := range matches.OptionOccurrences() {
		switch occurrence.Name {
		case "all":
			opts.showAll = true
		case "apparent-size":
			opts.countMode = duCountApparent
			opts.warnApparentSize = true
		case "block-size":
			if err := duApplyBlockSizeOption(inv, &opts, occurrence.Value); err != nil {
				return duOptions{}, err
			}
		case "bytes":
			opts.countMode = duCountApparent
			opts.humanMode = duHumanNone
			opts.blockSize = 1
			opts.warnBytes = true
		case "count-links":
			opts.countLinks = true
		case "dereference":
			opts.followMode = duFollowAll
		case "dereference-args":
			opts.followMode = duFollowArgs
		case "exclude":
			opts.excludePatterns = append(opts.excludePatterns, occurrence.Value)
		case "exclude-from":
			patterns, err := duReadExcludePatterns(ctx, inv, occurrence.Value)
			if err != nil {
				return duOptions{}, err
			}
			opts.excludePatterns = append(opts.excludePatterns, patterns...)
		case "files0-from":
			opts.files0From = occurrence.Value
		case "human-readable":
			opts.humanMode = duHumanBinary
			opts.blockSize = 1
		case "inodes":
			opts.countMode = duCountInodes
		case "kibibytes":
			opts.humanMode = duHumanNone
			opts.blockSize = 1024
		case "max-depth":
			maxDepth, err := strconv.Atoi(occurrence.Value)
			if err != nil || maxDepth < 0 {
				return duOptions{}, exitf(inv, 1, "du: invalid maximum depth %s", quoteGNUOperand(occurrence.Value))
			}
			opts.maxDepth = maxDepth
			opts.hasMaxDepth = true
		case "one-file-system":
			opts.oneFileSystem = true
		case "separate-dirs":
			opts.separateDirs = true
		case "si":
			opts.humanMode = duHumanSI
			opts.blockSize = 1
		case "summarize":
			opts.summary = true
		case "threshold":
			threshold, err := parseDUThreshold(inv, occurrence.Raw, occurrence.Value)
			if err != nil {
				return duOptions{}, err
			}
			opts.threshold = threshold
		case "total":
			opts.total = true
		}
	}
	if matches.Has("inodes") {
		opts.countMode = duCountInodes
	}
	return opts, nil
}

func duApplyBlockSizeOption(inv *Invocation, opts *duOptions, value string) error {
	switch value {
	case "human-readable":
		opts.humanMode = duHumanBinary
		opts.blockSize = 1
		return nil
	case "si":
		opts.humanMode = duHumanSI
		opts.blockSize = 1
		return nil
	default:
		blockSize, err := parseBlockSizeValue(inv, "du", value)
		if err != nil {
			return err
		}
		opts.humanMode = duHumanNone
		opts.blockSize = blockSize
		return nil
	}
}

func parseDUThreshold(inv *Invocation, origin, value string) (duThreshold, error) {
	if value == "" {
		return duThreshold{}, exitf(inv, 1, "du: invalid %s argument %s", origin, quoteGNUOperand(value))
	}

	raw := value
	negative := false
	switch raw[0] {
	case '-':
		negative = true
		raw = raw[1:]
	case '+':
		raw = raw[1:]
	}
	if raw == "" {
		return duThreshold{}, exitf(inv, 1, "du: invalid %s argument %s", origin, quoteGNUOperand(value))
	}

	multiplier := int64(1)
	switch last := raw[len(raw)-1]; last {
	case 'K', 'k':
		multiplier = 1024
		raw = raw[:len(raw)-1]
	case 'M', 'm':
		multiplier = 1024 * 1024
		raw = raw[:len(raw)-1]
	case 'G', 'g':
		multiplier = 1024 * 1024 * 1024
		raw = raw[:len(raw)-1]
	case 'T', 't':
		multiplier = 1024 * 1024 * 1024 * 1024
		raw = raw[:len(raw)-1]
	case 'P', 'p':
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
		raw = raw[:len(raw)-1]
	case 'E', 'e':
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024 * 1024
		raw = raw[:len(raw)-1]
	}

	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 0 || (negative && number == 0) {
		return duThreshold{}, exitf(inv, 1, "du: invalid %s argument %s", origin, quoteGNUOperand(value))
	}
	total, ok := checkedNonNegativeInt64Product(number, multiplier)
	if !ok {
		return duThreshold{}, exitf(inv, 1, "du: invalid %s argument %s", origin, quoteGNUOperand(value))
	}

	return duThreshold{
		set:      true,
		negative: negative,
		value:    total,
	}, nil
}

func duReadExcludePatterns(ctx context.Context, inv *Invocation, source string) ([]string, error) {
	file, _, err := openRead(ctx, inv, source)
	if err != nil {
		return nil, exitf(inv, 1, "du: cannot open %s for reading: %s", quoteGNUOperand(source), readAllErrorText(err))
	}
	defer func() { _ = file.Close() }()

	data, err := readAllReader(ctx, inv, file)
	if err != nil {
		return nil, exitf(inv, 1, "du: %s: read error: %s", wcErrorOperand(source), readAllErrorText(err))
	}

	lines := strings.Split(string(data), "\n")
	patterns := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

func duTargets(ctx context.Context, inv *Invocation, opts *duOptions, files []string) ([]string, int, error) {
	if opts.files0From == "" {
		return append([]string(nil), files...), 0, nil
	}
	if len(files) > 0 {
		return nil, 0, exitf(inv, 1, "du: extra operand %s\nfile operands cannot be combined with --files0-from\nTry 'du --help' for more information.", quoteGNUOperand(files[0]))
	}
	return duReadFiles0Targets(ctx, inv, opts.files0From)
}

func duReadFiles0Targets(ctx context.Context, inv *Invocation, source string) ([]string, int, error) {
	var (
		reader io.Reader
		closer io.Closer
	)

	switch source {
	case "-":
		reader = inv.Stdin
	default:
		info, _, exists, err := statMaybe(ctx, inv, source)
		if err != nil {
			return nil, 0, err
		}
		if !exists {
			return nil, 0, exitf(inv, 1, "du: cannot open %s for reading: %s", quoteGNUOperand(source), duErrorText(stdfs.ErrNotExist))
		}
		if info != nil && info.IsDir() {
			return nil, 0, exitf(inv, 1, "du: %s: read error: Is a directory", wcErrorOperand(source))
		}
		file, _, err := openRead(ctx, inv, source)
		if err != nil {
			return nil, 0, exitf(inv, 1, "du: cannot open %s for reading: %s", quoteGNUOperand(source), readAllErrorText(err))
		}
		reader = file
		closer = file
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	reader = commandutil.ReaderWithContext(ctx, reader)
	reader = wcLimitReaderForInvocation(inv, reader)
	buf := bufio.NewReader(reader)

	exitCode := 0
	index := 0
	seen := make(map[string]struct{})
	targets := make([]string, 0)

	for {
		record, done, err := wcReadFiles0Record(buf)
		if err != nil {
			if writeErr := duWriteMessage(inv.Stderr, fmt.Sprintf("du: %s: read error: %s", wcErrorOperand(source), readAllErrorText(err))); writeErr != nil {
				return nil, 0, writeErr
			}
			exitCode = 1
			break
		}
		if done {
			break
		}

		index++
		if len(record) == 0 {
			if err := duWriteMessage(inv.Stderr, fmt.Sprintf("du: %s:%d: invalid zero-length file name", wcFiles0SourceOperand(source), index)); err != nil {
				return nil, 0, err
			}
			exitCode = 1
			continue
		}

		target := string(record)
		if source == "-" && target == "-" {
			if err := duWriteMessage(inv.Stderr, "du: when reading file names from standard input, no file name of '-' allowed"); err != nil {
				return nil, 0, err
			}
			exitCode = 1
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	return targets, exitCode, nil
}

func (c *DU) walkRoot(ctx context.Context, inv *Invocation, state *duWalkState, rawTarget string) (int64, int, error) {
	visible := duVisibleRoot(rawTarget)
	if duExcluded(state.opts.excludePatterns, visible) {
		return 0, 0, nil
	}
	state.rootDevice = 0
	state.rootDeviceKnown = false
	state.activeDirs = make(map[fileInfoIdentityKey]struct{})
	linfo, abs, err := lstatPath(ctx, inv, rawTarget)
	if err != nil {
		if writeErr := duWriteAccessError(inv, visible, err); writeErr != nil {
			return 0, 0, writeErr
		}
		return 0, 1, nil
	}

	info := linfo
	effectiveAbs := abs
	symlinkDepth := 0
	if duShouldFollowRoot(&state.opts, visible, linfo) {
		info, effectiveAbs, err = duResolveSymlinkInfo(ctx, inv, abs)
		if err != nil {
			if writeErr := duWriteAccessError(inv, visible, err); writeErr != nil {
				return 0, 0, writeErr
			}
			return 0, 1, nil
		}
		symlinkDepth = 1
	}

	if state.opts.oneFileSystem {
		if device, ok := fileInfoDevice(duPreferredRootDeviceInfo(info, linfo)); ok {
			state.rootDevice = device
			state.rootDeviceKnown = true
		}
	}

	total, _, err := c.walkNode(ctx, inv, state, effectiveAbs, visible, info, 0, symlinkDepth)
	return total, state.exitCode, err
}

func (c *DU) walkNode(
	ctx context.Context,
	inv *Invocation,
	state *duWalkState,
	abs, visible string,
	info stdfs.FileInfo,
	depth int,
	symlinkDepth int,
) (int64, bool, error) {
	if state.opts.oneFileSystem && depth > 0 && state.rootDeviceKnown {
		if device, ok := fileInfoDevice(info); ok && device != state.rootDevice {
			return 0, info.IsDir(), nil
		}
	}

	isDir := info.IsDir()
	if isDir {
		if key, ok := fileInfoIdentity(info); ok {
			if _, active := state.activeDirs[key]; active {
				return 0, true, nil
			}
			if _, seen := state.seenDirs[key]; seen {
				return 0, true, nil
			}
			state.seenDirs[key] = struct{}{}
			state.activeDirs[key] = struct{}{}
			defer delete(state.activeDirs, key)
		}
	} else if !state.opts.countLinks {
		if key, ok := fileInfoIdentity(info); ok {
			if _, seen := state.seenFiles[key]; seen {
				return 0, false, nil
			}
			state.seenFiles[key] = struct{}{}
		}
	}

	selfCount := duSelfCount(info, &state.opts)
	total := selfCount

	if isDir {
		entries, err := readDir(ctx, inv, abs)
		if err != nil {
			if writeErr := duWriteReadDirError(inv, visible, err); writeErr != nil {
				return 0, true, writeErr
			}
			state.exitCode = 1
			if err := duMaybePrint(inv, &state.opts, visible, total, depth, true); err != nil {
				return 0, true, err
			}
			return total, true, nil
		}

		for _, entry := range entries {
			childTotal, childIsDir, err := c.walkChild(ctx, inv, state, abs, visible, entry.Name(), depth+1, symlinkDepth)
			if err != nil {
				return 0, true, err
			}
			if childIsDir {
				if !state.opts.separateDirs {
					total += childTotal
				}
				continue
			}
			total += childTotal
		}
	}

	if err := duMaybePrint(inv, &state.opts, visible, total, depth, isDir); err != nil {
		return 0, isDir, err
	}
	return total, isDir, nil
}

func (c *DU) walkChild(
	ctx context.Context,
	inv *Invocation,
	state *duWalkState,
	parentAbs, parentVisible, childName string,
	depth int,
	symlinkDepth int,
) (int64, bool, error) {
	childAbs := joinChildPath(parentAbs, childName)
	childVisible := duJoinVisiblePath(parentVisible, childName)

	if duExcluded(state.opts.excludePatterns, childVisible) {
		return 0, false, nil
	}

	linfo, _, err := lstatPath(ctx, inv, childAbs)
	if err != nil {
		if writeErr := duWriteAccessError(inv, childVisible, err); writeErr != nil {
			return 0, false, writeErr
		}
		state.exitCode = 1
		return 0, false, nil
	}

	info := linfo
	effectiveAbs := childAbs
	nextSymlinkDepth := symlinkDepth
	if state.opts.followMode == duFollowAll && linfo.Mode()&stdfs.ModeSymlink != 0 {
		nextSymlinkDepth++
		if nextSymlinkDepth > readlinkMaxSymlinkDepth {
			if writeErr := duWriteAccessError(inv, childVisible, &os.PathError{Op: "stat", Path: childAbs, Err: syscall.ELOOP}); writeErr != nil {
				return 0, false, writeErr
			}
			state.exitCode = 1
			return 0, false, nil
		}
		info, effectiveAbs, err = duResolveSymlinkInfo(ctx, inv, childAbs)
		if err != nil {
			if writeErr := duWriteAccessError(inv, childVisible, err); writeErr != nil {
				return 0, false, writeErr
			}
			state.exitCode = 1
			return 0, false, nil
		}
	}

	return c.walkNode(ctx, inv, state, effectiveAbs, childVisible, info, depth, nextSymlinkDepth)
}

func duShouldFollowRoot(opts *duOptions, visible string, info stdfs.FileInfo) bool {
	if info == nil || info.Mode()&stdfs.ModeSymlink == 0 {
		return false
	}
	if opts.followMode == duFollowAll || opts.followMode == duFollowArgs {
		return true
	}
	return visible != "/" && strings.HasSuffix(visible, "/")
}

func duPreferredRootDeviceInfo(info, fallback stdfs.FileInfo) stdfs.FileInfo {
	if _, ok := fileInfoDevice(info); ok {
		return info
	}
	return fallback
}

func duSelfCount(info stdfs.FileInfo, opts *duOptions) int64 {
	switch opts.countMode {
	case duCountInodes:
		return 1
	case duCountApparent:
		if info.IsDir() {
			return 0
		}
		return max(info.Size(), 0)
	default:
		return fileInfoAllocatedBytes(info)
	}
}

func duMaybePrint(inv *Invocation, opts *duOptions, visible string, total int64, depth int, isDir bool) error {
	if !duShouldPrint(opts, depth, isDir) {
		return nil
	}
	if !duPassesThreshold(total, opts.threshold) {
		return nil
	}
	return duWriteLine(inv.Stdout, duFormatCount(total, opts), visible)
}

func duShouldPrint(opts *duOptions, depth int, isDir bool) bool {
	if depth == 0 {
		return true
	}
	if opts.summary {
		return false
	}
	if opts.hasMaxDepth && depth > opts.maxDepth {
		return false
	}
	if isDir {
		return true
	}
	return opts.showAll
}

func duPassesThreshold(value int64, threshold duThreshold) bool {
	if !threshold.set {
		return true
	}
	if threshold.negative {
		return value <= threshold.value
	}
	return value >= threshold.value
}

func duFormatCount(value int64, opts *duOptions) string {
	switch opts.humanMode {
	case duHumanBinary:
		return duFormatHumanCount(value, 1024, []string{"K", "M", "G", "T", "P", "E"})
	case duHumanSI:
		return duFormatHumanCount(value, 1000, []string{"k", "M", "G", "T", "P", "E"})
	}
	if opts.countMode == duCountInodes {
		return strconv.FormatInt(value, 10)
	}
	blockSize := opts.blockSize
	if blockSize <= 0 {
		blockSize = 1024
	}
	return strconv.FormatInt(duCeilDiv(value, blockSize), 10)
}

func duCeilDiv(value, unit int64) int64 {
	if value <= 0 {
		return 0
	}
	return (value + unit - 1) / unit
}

func duFormatHumanCount(value int64, base float64, suffixes []string) string {
	amount := float64(value)
	suffix := ""
	for i := 0; i < len(suffixes) && amount >= base; i++ {
		amount /= base
		suffix = suffixes[i]
	}
	if suffix == "" {
		return strconv.FormatInt(value, 10)
	}
	if amount < 10 {
		return fmt.Sprintf("%.1f%s", math.Ceil((amount-1e-12)*10)/10, suffix)
	}
	return fmt.Sprintf("%.0f%s", math.Ceil(amount-1e-12), suffix)
}

func duVisibleRoot(raw string) string {
	switch {
	case raw == "":
		return "."
	case raw == "/" || strings.Trim(raw, "/") == "":
		return "/"
	case strings.HasSuffix(raw, "/"):
		return strings.TrimRight(raw, "/") + "/"
	default:
		return raw
	}
}

func duJoinVisiblePath(parent, child string) string {
	switch parent {
	case "":
		return child
	case ".":
		return "./" + child
	case "/":
		return "/" + child
	default:
		if strings.HasSuffix(parent, "/") {
			return parent + child
		}
		return parent + "/" + child
	}
}

func duExcluded(patterns []string, visible string) bool {
	if len(patterns) == 0 {
		return false
	}
	trimmed := strings.TrimRight(visible, "/")
	if visible == "/" {
		trimmed = "/"
	}
	base := path.Base(trimmed)
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		if matched, _ := path.Match(pattern, base); matched {
			return true
		}
		if matched, _ := path.Match(pattern, trimmed); matched {
			return true
		}
	}
	return false
}

func duWriteLine(w io.Writer, size, label string) error {
	if _, err := fmt.Fprintf(w, "%s\t%s\n", size, label); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func duWriteAccessError(inv *Invocation, operand string, err error) error {
	return duWriteMessage(inv.Stderr, fmt.Sprintf("du: cannot access %s: %s", quoteGNUOperand(operand), duErrorText(err)))
}

func duWriteReadDirError(inv *Invocation, operand string, err error) error {
	return duWriteMessage(inv.Stderr, fmt.Sprintf("du: cannot read directory %s: %s", quoteGNUOperand(operand), duErrorText(err)))
}

func duWriteMessage(w io.Writer, message string) error {
	if _, err := io.WriteString(w, message+"\n"); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func duResolveSymlinkInfo(ctx context.Context, inv *Invocation, abs string) (stdfs.FileInfo, string, error) {
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
			return nil, "", &os.PathError{Op: "stat", Path: current, Err: syscall.ELOOP}
		}
		target, err := inv.FS.Readlink(ctx, current)
		if err != nil {
			return nil, "", err
		}
		if path.IsAbs(target) {
			current = path.Clean(target)
			continue
		}
		current = path.Clean(path.Join(path.Dir(current), target))
	}
	return nil, "", &os.PathError{Op: "stat", Path: abs, Err: syscall.ELOOP}
}

func duErrorText(err error) string {
	switch {
	case errorsIsNotExist(err):
		return "No such file or directory"
	case errorsIsDirectory(err):
		return "Is a directory"
	case errors.Is(err, syscall.ELOOP):
		return "too many links"
	case os.IsPermission(err):
		return "Permission denied"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr != nil && pathErr.Err != nil {
		return duErrorText(pathErr.Err)
	}
	return err.Error()
}

var _ Command = (*DU)(nil)
var _ SpecProvider = (*DU)(nil)
var _ ParsedRunner = (*DU)(nil)
