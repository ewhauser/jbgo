package builtins

import (
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Tail struct{}

type tailFollowMode int

const (
	tailFollowNone tailFollowMode = iota
	tailFollowDescriptor
	tailFollowName
)

type tailOptions struct {
	lines              int
	bytes              int
	hasBytes           bool
	fromBytes          bool
	fromLine           bool
	quiet              bool
	verbose            bool
	files              []string
	follow             tailFollowMode
	retry              bool
	sleepInterval      time.Duration
	maxUnchangedStats  int
	disableInotifyHint bool
	debug              bool
	pid                int
}

type tailFollowState struct {
	path            string
	file            gbfs.File
	fileInfo        stdfs.FileInfo
	offset          int64
	active          bool
	exists          bool
	untailable      bool
	headerPrinted   bool
	announcedAbsent bool
}

type tailOutputState struct {
	lastFile  string
	hasOutput bool
}

func NewTail() *Tail {
	return &Tail{}
}

func (c *Tail) Name() string {
	return "tail"
}

func (c *Tail) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Tail) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil {
		return nil
	}
	parseInv := *inv
	parseInv.Args = normalizeTailInvocation(inv.Args)
	return &parseInv
}

func (c *Tail) Spec() CommandSpec {
	return CommandSpec{
		Name:  "tail",
		Usage: "tail [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "lines", Short: 'n', Long: "lines", ValueName: "K", Arity: OptionRequiredValue, Help: "output the last K lines, instead of the last 10; or use +K to output starting with the Kth"},
			{Name: "bytes", Short: 'c', Long: "bytes", ValueName: "K", Arity: OptionRequiredValue, Help: "output the last K bytes; or use +K to output starting with the Kth"},
			{Name: "quiet", Short: 'q', Long: "quiet", Aliases: []string{"silent"}, Help: "never output headers giving file names"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "always output headers giving file names"},
			{Name: "follow", Short: 'f', Long: "follow", ValueName: "HOW", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "output appended data as the file grows; an absent HOW defaults to 'descriptor'"},
			{Name: "follow-name-retry", Short: 'F', Help: "same as --follow=name --retry"},
			{Name: "retry", Long: "retry", Help: "keep trying to open a file if it is inaccessible"},
			{Name: "pid", Long: "pid", ValueName: "PID", Arity: OptionRequiredValue, Repeatable: true, Help: "with -f, terminate after process ID, PID dies"},
			{Name: "disable-inotify", Long: "disable-inotify", Help: "accepted for compatibility; polling mode is already used"},
			{Name: "debug", Long: "debug", Help: "print diagnostic information to standard error"},
			{Name: "sleep-interval", Short: 's', Long: "sleep-interval", ValueName: "N", Arity: OptionRequiredValue, Help: "with -f, sleep for approximately N seconds between iterations"},
			{Name: "max-unchanged-stats", Long: "max-unchanged-stats", ValueName: "N", Arity: OptionRequiredValue, Help: "with --follow=name, reopen a FILE which has not changed size after N iterations"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
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

func (c *Tail) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := tailOptionsFromParsed(inv, matches)
	if err != nil {
		return err
	}

	process := func(data []byte) []byte {
		if opts.hasBytes {
			if opts.fromBytes {
				return bytesFrom(data, opts.bytes)
			}
			return lastBytes(data, opts.bytes)
		}
		if opts.fromLine {
			return linesFrom(data, opts.lines)
		}
		return lastLines(data, opts.lines)
	}

	showHeaders := opts.verbose || (!opts.quiet && len(opts.files) > 1)
	outputState := &tailOutputState{}
	if err := writeTailWarnings(inv, &opts); err != nil {
		return err
	}
	if len(opts.files) == 0 {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return err
		}
		if err := writeTailOutput(inv, outputState, "", process(data), false, false); err != nil {
			return err
		}
		return nil
	}

	states := make([]tailFollowState, 0, len(opts.files))
	followedStdin := false
	exitCode := 0
	for _, file := range opts.files {
		if file == "-" {
			if opts.follow == tailFollowName {
				writeTailCannotFollowStdinByName(inv)
				exitCode = 1
				continue
			}
			if err := ensureTailStdinAvailable(inv); err != nil {
				writeTailCannotFstatStdin(inv)
				exitCode = 1
				continue
			}
			data, err := readAllStdin(ctx, inv)
			if err != nil {
				return err
			}
			if len(data) == 0 && opts.follow != tailFollowNone {
				writeTailCannotFstatStdin(inv)
				exitCode = 1
				continue
			}
			if err := writeTailOutput(inv, outputState, tailDisplayName(file), process(data), showHeaders, showHeaders); err != nil {
				return err
			}
			if opts.follow != tailFollowNone {
				followedStdin = true
			}
			continue
		}
		data, followFile, info, err := readTailInitialFile(ctx, inv, file, opts.follow)
		if err != nil {
			if opts.follow == tailFollowName && opts.retry && tailPathIsUntailable(ctx, inv, file) {
				writeTailErrorReadingDirectory(inv, file)
				writeTailCannotFollowFileType(inv, file)
				states = append(states, tailFollowState{
					path:            file,
					active:          true,
					untailable:      true,
					headerPrinted:   false,
					announcedAbsent: true,
				})
				continue
			}
			switch {
			case errorsIsDirectory(err):
				writeTailErrorReadingDirectory(inv, file)
			case errorsIsNotExist(err):
				writeTailMissingError(inv, file)
			default:
				_, _ = fmt.Fprintf(inv.Stderr, "tail: error reading '%s': %s\n", file, readAllErrorText(err))
			}
			if opts.follow != tailFollowNone && opts.retry {
				states = append(states, tailFollowState{
					path:            file,
					active:          true,
					headerPrinted:   false,
					announcedAbsent: true,
				})
				continue
			}
			exitCode = 1
			continue
		}

		headerPrinted := false
		if showHeaders {
			headerPrinted = true
		}
		if err := writeTailOutput(inv, outputState, file, process(data), showHeaders, showHeaders); err != nil {
			return err
		}

		states = append(states, tailFollowState{
			path:          file,
			file:          followFile,
			fileInfo:      info,
			offset:        int64(len(data)),
			active:        true,
			exists:        true,
			headerPrinted: headerPrinted,
		})
	}
	defer closeTailFollowStates(states)

	if opts.follow == tailFollowNone {
		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}
		return nil
	}

	if len(states) == 0 {
		if followedStdin {
			return nil
		}
		writeTailNoFilesRemainingError(inv)
		return &ExitError{Code: 1}
	}

	ticker := time.NewTicker(opts.sleepInterval)
	defer ticker.Stop()

	if opts.debug {
		if _, err := fmt.Fprintln(inv.Stderr, "tail: using polling mode"); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	for {
		if opts.follow != tailFollowNone && opts.pid != 0 {
			alive, err := tailPIDIsAlive(inv)
			if err != nil {
				return err
			}
			if !alive {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for i := range states {
				state := &states[i]
				if !state.active {
					continue
				}
				var err error
				if opts.follow == tailFollowDescriptor {
					err = c.pollTailDescriptor(ctx, inv, state, showHeaders, &opts, process, outputState)
				} else {
					err = c.pollTailByName(ctx, inv, state, showHeaders, &opts, process, outputState)
				}
				if err != nil {
					return err
				}
			}
			if !tailHasActiveStates(states) {
				writeTailNoFilesRemainingError(inv)
				return &ExitError{Code: 1}
			}
			if opts.follow == tailFollowName && !opts.retry && !tailHasExistingStates(states) {
				writeTailNoFilesRemainingError(inv)
				return &ExitError{Code: 1}
			}
		}
	}
}

func normalizeTailInvocation(args []string) []string {
	normalized := append([]string(nil), args...)
	for i, arg := range normalized {
		if arg == "--" {
			break
		}
		switch {
		case arg == "---disable-inotify":
			normalized[i] = "--disable-inotify"
		case strings.HasPrefix(arg, "--lines=+"):
			normalized[i] = "--lines=" + strings.TrimPrefix(arg, "--lines=+")
		case len(arg) > 1 && arg[0] == '-' && isDecimalDigits(arg[1:]):
			normalized[i] = "-n" + arg[1:]
		}
	}
	return normalized
}

func readTailInitialFile(ctx context.Context, inv *Invocation, name string, follow tailFollowMode) ([]byte, gbfs.File, stdfs.FileInfo, error) {
	file, _, err := openRead(ctx, inv, name)
	if err != nil {
		return nil, nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, nil, &ExitError{Code: 1, Err: err}
	}
	data, err := readAllReader(ctx, inv, file)
	if err != nil {
		_ = file.Close()
		return nil, nil, nil, err
	}
	if follow == tailFollowDescriptor {
		return data, file, info, nil
	}
	if err := file.Close(); err != nil {
		return nil, nil, nil, &ExitError{Code: 1, Err: err}
	}
	return data, nil, info, nil
}

func closeTailFollowStates(states []tailFollowState) {
	for i := range states {
		if states[i].file != nil {
			_ = states[i].file.Close()
		}
	}
}

func tailHasActiveStates(states []tailFollowState) bool {
	for i := range states {
		if states[i].active {
			return true
		}
	}
	return false
}

func tailHasExistingStates(states []tailFollowState) bool {
	for i := range states {
		if !states[i].active {
			continue
		}
		if states[i].file != nil || states[i].exists {
			return true
		}
	}
	return false
}

func (c *Tail) pollTailByName(
	ctx context.Context,
	inv *Invocation,
	state *tailFollowState,
	showHeaders bool,
	opts *tailOptions,
	process func([]byte) []byte,
	outputState *tailOutputState,
) error {
	info, _, exists, err := statMaybe(ctx, inv, state.path)
	if err != nil {
		return &ExitError{Code: exitCodeForError(err), Err: err}
	}
	if !exists {
		if state.exists {
			state.exists = false
			state.offset = 0
			state.untailable = false
			if opts.follow == tailFollowName {
				writeTailInaccessibleError(inv, state.path)
				state.announcedAbsent = true
				return nil
			}
		}
		if !state.announcedAbsent && (opts.retry || opts.follow == tailFollowName) {
			writeTailMissingError(inv, state.path)
			state.announcedAbsent = true
		}
		return nil
	}
	if info.IsDir() {
		state.exists = false
		state.offset = 0
		state.fileInfo = nil
		state.untailable = true
		return nil
	}

	sameFile, identityKnown := tailSameFileInfo(state.fileInfo, info)
	replaced := state.exists && identityKnown && !sameFile
	if !state.exists && state.announcedAbsent && opts.follow == tailFollowName {
		if state.untailable {
			writeTailBecameAccessible(inv, state.path)
		} else {
			writeTailAppearedFollowingNewFile(inv, state.path)
		}
	} else if replaced {
		writeTailReplacedFollowingNewFile(inv, state.path)
		state.offset = 0
	}

	data, _, err := readAllFile(ctx, inv, state.path)
	if err != nil {
		return &ExitError{Code: exitCodeForError(err), Err: err}
	}

	if !state.exists || replaced {
		state.exists = true
		state.fileInfo = info
		state.announcedAbsent = false
		state.untailable = false
		state.offset = int64(len(data))
		return writeTailOutput(inv, outputState, state.path, process(data), showHeaders, false)
	}

	if int64(len(data)) < state.offset {
		state.offset = 0
	}
	if int64(len(data)) == state.offset {
		state.fileInfo = info
		return nil
	}
	if err := writeTailOutput(inv, outputState, state.path, data[state.offset:], showHeaders, false); err != nil {
		return err
	}
	state.fileInfo = info
	state.offset = int64(len(data))
	return nil
}

func (c *Tail) pollTailDescriptor(
	ctx context.Context,
	inv *Invocation,
	state *tailFollowState,
	showHeaders bool,
	opts *tailOptions,
	process func([]byte) []byte,
	outputState *tailOutputState,
) error {
	if state.file == nil {
		if opts.retry && tailPathIsUntailable(ctx, inv, state.path) {
			writeTailUntailableGivingUp(inv, state.path)
			state.active = false
			return nil
		}
		data, followFile, _, err := readTailInitialFile(ctx, inv, state.path, tailFollowDescriptor)
		if err != nil {
			if !state.announcedAbsent && opts.retry {
				writeTailMissingError(inv, state.path)
				state.announcedAbsent = true
			}
			return nil
		}
		if state.announcedAbsent {
			writeTailAppearedFollowingNewFile(inv, state.path)
		}
		state.file = followFile
		state.exists = true
		state.announcedAbsent = false
		state.offset = int64(len(data))
		if err := writeTailOutput(inv, outputState, state.path, process(data), showHeaders, false); err != nil {
			return err
		}
		return nil
	}

	info, err := state.file.Stat()
	if err == nil && info.Size() < state.offset {
		if err := seekTailFileStart(state.file); err == nil {
			writeTailFileTruncated(inv, state.path)
			state.offset = 0
		}
	}

	data, err := readAllReader(ctx, inv, state.file)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := writeTailOutput(inv, outputState, state.path, data, showHeaders, false); err != nil {
		return err
	}
	state.offset += int64(len(data))
	return nil
}

func seekTailFileStart(file gbfs.File) error {
	seeker, ok := file.(interface {
		Seek(offset int64, whence int) (int64, error)
	})
	if !ok {
		return fmt.Errorf("file does not support seek")
	}
	_, err := seeker.Seek(0, io.SeekStart)
	return err
}

func parseTailSleepInterval(raw string) (time.Duration, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid interval")
	}
	return time.Duration(value * float64(time.Second)), nil
}

func tailOptionsFromParsed(inv *Invocation, matches *ParsedCommand) (tailOptions, error) {
	opts := tailOptions{
		lines:              10,
		quiet:              matches.Has("quiet"),
		verbose:            matches.Has("verbose"),
		files:              matches.Args("file"),
		sleepInterval:      time.Second,
		maxUnchangedStats:  5,
		retry:              matches.Has("retry"),
		debug:              matches.Has("debug"),
		disableInotifyHint: matches.Has("disable-inotify"),
	}
	if matches.Has("lines") {
		count, fromLine, err := parseHeadTailCount(matches.Value("lines"), true)
		if err != nil {
			return tailOptions{}, exitf(inv, 1, "tail: invalid number of lines")
		}
		opts.lines = count
		opts.fromLine = fromLine
	}
	if matches.Has("bytes") {
		count, fromBytes, err := parseHeadTailCount(matches.Value("bytes"), true)
		if err != nil {
			return tailOptions{}, exitf(inv, 1, "tail: invalid number of bytes")
		}
		opts.bytes = count
		opts.hasBytes = true
		opts.fromBytes = fromBytes
	}
	if matches.Has("follow") {
		switch follow := matches.Value("follow"); follow {
		case "", "descriptor":
			opts.follow = tailFollowDescriptor
		case "name":
			opts.follow = tailFollowName
		default:
			return tailOptions{}, exitf(inv, 1, "tail: unsupported follow mode --follow=%s", follow)
		}
	}
	if matches.Has("follow-name-retry") {
		opts.follow = tailFollowName
		opts.retry = true
	}
	if matches.Has("pid") {
		pid, err := strconv.Atoi(matches.Value("pid"))
		if err != nil || pid < 0 {
			return tailOptions{}, exitf(inv, 1, "tail: invalid PID %q", matches.Value("pid"))
		}
		opts.pid = pid
	}
	if matches.Has("sleep-interval") {
		interval, err := parseTailSleepInterval(matches.Value("sleep-interval"))
		if err != nil {
			return tailOptions{}, exitf(inv, 1, "tail: invalid number of seconds")
		}
		opts.sleepInterval = interval
	}
	if matches.Has("max-unchanged-stats") {
		value, err := strconv.Atoi(matches.Value("max-unchanged-stats"))
		if err != nil || value < 0 {
			return tailOptions{}, exitf(inv, 1, "tail: invalid maximum number of unchanged stats between opens")
		}
		opts.maxUnchangedStats = value
	}
	return opts, nil
}

func tailPathIsUntailable(ctx context.Context, inv *Invocation, name string) bool {
	info, _, exists, err := statMaybe(ctx, inv, name)
	if err != nil || !exists {
		return false
	}
	return info.IsDir()
}

func tailPIDIsAlive(inv *Invocation) (bool, error) {
	return false, exitf(inv, 1, "tail: --pid is unsupported in this sandbox")
}

func writeTailWarnings(inv *Invocation, opts *tailOptions) error {
	switch {
	case opts.retry && opts.follow == tailFollowNone:
		if _, err := fmt.Fprintln(inv.Stderr, "tail: warning: --retry ignored; --retry is useful only when following"); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	case opts.retry && opts.follow == tailFollowDescriptor:
		if _, err := fmt.Fprintln(inv.Stderr, "tail: warning: --retry only effective for the initial open"); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func writeTailHeader(inv *Invocation, file string) error {
	if _, err := fmt.Fprintf(inv.Stdout, "==> %s <==\n", file); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func writeTailMissingError(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: cannot open '%s' for reading: No such file or directory\n", file)
}

func writeTailErrorReadingDirectory(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: error reading '%s': Is a directory\n", file)
}

func writeTailCannotFollowFileType(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: %s: cannot follow end of this type of file\n", file)
}

func writeTailInaccessibleError(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: '%s' has become inaccessible: No such file or directory\n", file)
}

func writeTailBecameAccessible(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: '%s' has become accessible\n", file)
}

func writeTailAppearedFollowingNewFile(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: '%s' has appeared;  following new file\n", file)
}

func writeTailReplacedFollowingNewFile(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: '%s' has been replaced;  following new file\n", file)
}

func writeTailUntailableGivingUp(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: '%s' has been replaced with an untailable file; giving up on this name\n", file)
}

func writeTailFileTruncated(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: %s: file truncated\n", file)
}

func writeTailNoFilesRemainingError(inv *Invocation) {
	_, _ = fmt.Fprintln(inv.Stderr, "tail: no files remaining")
}

func writeTailCannotFstatStdin(inv *Invocation) {
	_, _ = fmt.Fprintln(inv.Stderr, "tail: cannot fstat 'standard input'")
}

func writeTailCannotFollowStdinByName(inv *Invocation) {
	_, _ = fmt.Fprintln(inv.Stderr, "tail: cannot follow '-' by name")
}

func writeTailOutput(inv *Invocation, outputState *tailOutputState, file string, data []byte, showHeaders, forceHeader bool) error {
	if outputState == nil {
		outputState = &tailOutputState{}
	}
	headerNeeded := showHeaders && (forceHeader || outputState.lastFile != file)
	if headerNeeded {
		if outputState.hasOutput {
			if _, err := fmt.Fprintln(inv.Stdout); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if err := writeTailHeader(inv, file); err != nil {
			return err
		}
		outputState.hasOutput = true
		outputState.lastFile = file
	}
	if len(data) == 0 {
		return nil
	}
	if _, err := inv.Stdout.Write(data); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	outputState.hasOutput = true
	outputState.lastFile = file
	return nil
}

func tailSameFileInfo(prev, curr stdfs.FileInfo) (same, known bool) {
	if prev == nil || curr == nil {
		return false, false
	}
	if tailSupportsOSSameFile(prev) && tailSupportsOSSameFile(curr) {
		return os.SameFile(prev, curr), true
	}
	devPrev, inoPrev, okPrev := tailDeviceAndInode(prev)
	devCurr, inoCurr, okCurr := tailDeviceAndInode(curr)
	if okPrev && okCurr {
		return devPrev == devCurr && inoPrev == inoCurr, true
	}
	return false, false
}

func tailSupportsOSSameFile(info stdfs.FileInfo) bool {
	if info == nil || info.Sys() == nil {
		return false
	}
	return os.SameFile(info, info)
}

func tailDeviceAndInode(info stdfs.FileInfo) (dev, ino uint64, ok bool) {
	if info == nil {
		return 0, 0, false
	}
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return 0, 0, false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, 0, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0, 0, false
	}
	devField := value.FieldByName("Dev")
	inoField := value.FieldByName("Ino")
	if !devField.IsValid() || !inoField.IsValid() {
		return 0, 0, false
	}
	return tailUintField(devField), tailUintField(inoField), true
}

func tailUintField(field reflect.Value) uint64 {
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int())
	default:
		return 0
	}
}

func ensureTailStdinAvailable(inv *Invocation) error {
	statter, ok := inv.Stdin.(interface {
		Stat() (stdfs.FileInfo, error)
	})
	if !ok {
		return nil
	}
	_, err := statter.Stat()
	return err
}

func tailDisplayName(name string) string {
	if name == "-" {
		return "standard input"
	}
	return name
}

var _ Command = (*Tail)(nil)
var _ SpecProvider = (*Tail)(nil)
var _ ParsedRunner = (*Tail)(nil)
var _ ParseInvocationNormalizer = (*Tail)(nil)
