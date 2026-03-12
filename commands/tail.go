package commands

import (
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"reflect"
	"strconv"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
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
	fromLine           bool
	quiet              bool
	verbose            bool
	files              []string
	follow             tailFollowMode
	retry              bool
	sleepInterval      time.Duration
	maxUnchangedStats  int
	disableInotifyHint bool
}

type tailFollowState struct {
	path            string
	file            gbfs.File
	identity        string
	offset          int64
	exists          bool
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
	opts, err := parseTailArgs(inv)
	if err != nil {
		return err
	}

	process := func(data []byte) []byte {
		if opts.hasBytes {
			return lastBytes(data, opts.bytes)
		}
		if opts.fromLine {
			return linesFrom(data, opts.lines)
		}
		return lastLines(data, opts.lines)
	}

	showHeaders := opts.verbose || (!opts.quiet && len(opts.files) > 1)
	outputState := &tailOutputState{}
	if len(opts.files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		if err := writeTailOutput(inv, outputState, "", process(data), false, false); err != nil {
			return err
		}
		return nil
	}

	states := make([]tailFollowState, 0, len(opts.files))
	exitCode := 0
	for _, file := range opts.files {
		data, followFile, err := readTailInitialFile(ctx, inv, file, opts.follow)
		if err != nil {
			writeTailMissingError(inv, file)
			if opts.follow != tailFollowNone && opts.retry {
				states = append(states, tailFollowState{
					path:            file,
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

		identity := ""
		if opts.follow == tailFollowName {
			info, _, statErr := statPath(ctx, inv, file)
			if statErr == nil {
				identity = tailFileIdentity(info)
			}
		}
		states = append(states, tailFollowState{
			path:          file,
			file:          followFile,
			identity:      identity,
			offset:        int64(len(data)),
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
		return &ExitError{Code: 1}
	}

	ticker := time.NewTicker(opts.sleepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for i := range states {
				state := &states[i]
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
		}
	}
}

func readTailInitialFile(ctx context.Context, inv *Invocation, name string, follow tailFollowMode) ([]byte, gbfs.File, error) {
	if follow == tailFollowDescriptor {
		file, _, err := openRead(ctx, inv, name)
		if err != nil {
			return nil, nil, err
		}
		data, err := io.ReadAll(file)
		if err != nil {
			_ = file.Close()
			return nil, nil, &ExitError{Code: 1, Err: err}
		}
		return data, file, nil
	}

	data, _, err := readAllFile(ctx, inv, name)
	if err != nil {
		return nil, nil, err
	}
	return data, nil, nil
}

func closeTailFollowStates(states []tailFollowState) {
	for i := range states {
		if states[i].file != nil {
			_ = states[i].file.Close()
		}
	}
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
	info, _, exists, err := statMaybe(ctx, inv, policy.FileActionStat, state.path)
	if err != nil {
		return &ExitError{Code: exitCodeForError(err), Err: err}
	}
	if !exists {
		if state.exists {
			state.exists = false
			state.offset = 0
			state.identity = ""
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

	identity := tailFileIdentity(info)
	replaced := state.exists && state.identity != "" && identity != "" && state.identity != identity
	if !state.exists && state.announcedAbsent && opts.follow == tailFollowName && opts.retry {
		if _, err := fmt.Fprintf(inv.Stderr, "tail: '%s' has appeared;  following new file\n", state.path); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	} else if replaced {
		if _, err := fmt.Fprintf(inv.Stderr, "tail: '%s' has been replaced;  following new file\n", state.path); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		state.offset = 0
	}

	data, _, err := readAllFile(ctx, inv, state.path)
	if err != nil {
		return &ExitError{Code: exitCodeForError(err), Err: err}
	}

	if !state.exists || replaced {
		state.exists = true
		state.identity = identity
		state.announcedAbsent = false
		state.offset = int64(len(data))
		return writeTailOutput(inv, outputState, state.path, process(data), showHeaders, false)
	}

	if int64(len(data)) < state.offset {
		state.offset = 0
	}
	if int64(len(data)) == state.offset {
		state.identity = identity
		return nil
	}
	if err := writeTailOutput(inv, outputState, state.path, data[state.offset:], showHeaders, false); err != nil {
		return err
	}
	state.identity = identity
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
		data, followFile, err := readTailInitialFile(ctx, inv, state.path, tailFollowDescriptor)
		if err != nil {
			if !state.announcedAbsent && opts.retry {
				writeTailMissingError(inv, state.path)
				state.announcedAbsent = true
			}
			return nil
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
			state.offset = 0
		}
	}

	data, err := io.ReadAll(state.file)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
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

func parseTailArgs(inv *Invocation) (tailOptions, error) {
	args := inv.Args
	opts := tailOptions{
		lines:             10,
		sleepInterval:     time.Second,
		maxUnchangedStats: 5,
	}

	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			args = args[1:]
			opts.files = append(opts.files, args...)
			return opts, nil
		case arg == "-n" || arg == "--lines":
			if len(args) < 2 {
				return tailOptions{}, exitf(inv, 1, "tail: missing argument to -n")
			}
			count, fromLine, err := parseHeadTailCount(args[1], true)
			if err != nil {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of lines")
			}
			opts.lines = count
			opts.fromLine = fromLine
			args = args[2:]
		case strings.HasPrefix(arg, "--lines="):
			count, err := strconv.Atoi(strings.TrimPrefix(arg, "--lines="))
			if err != nil || count < 0 {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of lines")
			}
			opts.lines = count
			opts.fromLine = false
			args = args[1:]
		case arg == "-c" || arg == "--bytes":
			if len(args) < 2 {
				return tailOptions{}, exitf(inv, 1, "tail: missing argument to -c")
			}
			count, err := strconv.Atoi(args[1])
			if err != nil || count < 0 {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of bytes")
			}
			opts.bytes = count
			opts.hasBytes = true
			args = args[2:]
		case strings.HasPrefix(arg, "--bytes="):
			count, err := strconv.Atoi(strings.TrimPrefix(arg, "--bytes="))
			if err != nil || count < 0 {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of bytes")
			}
			opts.bytes = count
			opts.hasBytes = true
			args = args[1:]
		case strings.HasPrefix(arg, "-n"):
			count, fromLine, err := parseHeadTailCount(strings.TrimPrefix(arg, "-n"), true)
			if err != nil {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of lines")
			}
			opts.lines = count
			opts.fromLine = fromLine
			args = args[1:]
		case strings.HasPrefix(arg, "-c"):
			count, err := strconv.Atoi(strings.TrimPrefix(arg, "-c"))
			if err != nil || count < 0 {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of bytes")
			}
			opts.bytes = count
			opts.hasBytes = true
			args = args[1:]
		case arg == "-q" || arg == "--quiet" || arg == "--silent":
			opts.quiet = true
			args = args[1:]
		case arg == "-v" || arg == "--verbose":
			opts.verbose = true
			args = args[1:]
		case arg == "-f" || arg == "--follow":
			opts.follow = tailFollowDescriptor
			args = args[1:]
		case strings.HasPrefix(arg, "--follow="):
			switch strings.TrimPrefix(arg, "--follow=") {
			case "descriptor":
				opts.follow = tailFollowDescriptor
			case "name":
				opts.follow = tailFollowName
			default:
				return tailOptions{}, exitf(inv, 1, "tail: unsupported follow mode %s", arg)
			}
			args = args[1:]
		case arg == "-F":
			opts.follow = tailFollowName
			opts.retry = true
			args = args[1:]
		case arg == "--retry":
			opts.retry = true
			args = args[1:]
		case arg == "---disable-inotify":
			opts.disableInotifyHint = true
			args = args[1:]
		case arg == "-s" || arg == "--sleep-interval":
			if len(args) < 2 {
				return tailOptions{}, exitf(inv, 1, "tail: missing argument to -s")
			}
			interval, err := parseTailSleepInterval(args[1])
			if err != nil {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of seconds")
			}
			opts.sleepInterval = interval
			args = args[2:]
		case strings.HasPrefix(arg, "--sleep-interval="):
			interval, err := parseTailSleepInterval(strings.TrimPrefix(arg, "--sleep-interval="))
			if err != nil {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of seconds")
			}
			opts.sleepInterval = interval
			args = args[1:]
		case strings.HasPrefix(arg, "-s"):
			interval, err := parseTailSleepInterval(strings.TrimPrefix(arg, "-s"))
			if err != nil {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of seconds")
			}
			opts.sleepInterval = interval
			args = args[1:]
		case arg == "--max-unchanged-stats":
			if len(args) < 2 {
				return tailOptions{}, exitf(inv, 1, "tail: missing argument to --max-unchanged-stats")
			}
			value, err := strconv.Atoi(args[1])
			if err != nil || value < 0 {
				return tailOptions{}, exitf(inv, 1, "tail: invalid maximum number of unchanged stats between opens")
			}
			opts.maxUnchangedStats = value
			args = args[2:]
		case strings.HasPrefix(arg, "--max-unchanged-stats="):
			value, err := strconv.Atoi(strings.TrimPrefix(arg, "--max-unchanged-stats="))
			if err != nil || value < 0 {
				return tailOptions{}, exitf(inv, 1, "tail: invalid maximum number of unchanged stats between opens")
			}
			opts.maxUnchangedStats = value
			args = args[1:]
		case len(arg) > 1 && arg[0] == '-' && arg[1] >= '0' && arg[1] <= '9':
			count, err := strconv.Atoi(arg[1:])
			if err != nil {
				return tailOptions{}, exitf(inv, 1, "tail: invalid number of lines")
			}
			opts.lines = count
			args = args[1:]
		case strings.HasPrefix(arg, "-"):
			return tailOptions{}, exitf(inv, 1, "tail: unsupported flag %s", arg)
		default:
			opts.files = append(opts.files, arg)
			args = args[1:]
		}
	}

	return opts, nil
}

func parseTailSleepInterval(raw string) (time.Duration, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid interval")
	}
	return time.Duration(value * float64(time.Second)), nil
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

func writeTailInaccessibleError(inv *Invocation, file string) {
	_, _ = fmt.Fprintf(inv.Stderr, "tail: '%s' has become inaccessible: No such file or directory\n", file)
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

func tailFileIdentity(info stdfs.FileInfo) string {
	if info == nil {
		return ""
	}
	sys := info.Sys()
	if sys == nil {
		return ""
	}
	value := reflect.ValueOf(sys)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return ""
	}
	dev := value.FieldByName("Dev")
	ino := value.FieldByName("Ino")
	if !dev.IsValid() || !ino.IsValid() {
		return ""
	}
	return fmt.Sprintf("%v:%v", dev.Interface(), ino.Interface())
}

var _ Command = (*Tail)(nil)
