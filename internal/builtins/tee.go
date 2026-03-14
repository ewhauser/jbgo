package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/ewhauser/gbash/policy"
)

const teeBufferSize = 8 * 1024

var (
	errTeeAbort      = errors.New("tee: abort")
	errTeeAbortQuiet = errors.New("tee: abort quietly")
	errTeeNoWriters  = errors.New("tee: no writers remaining")
)

type teeOutputErrorMode int

const (
	teeOutputErrorWarn teeOutputErrorMode = iota + 1
	teeOutputErrorWarnNoPipe
	teeOutputErrorExit
	teeOutputErrorExitNoPipe
)

type teeOptions struct {
	append           bool
	ignoreInterrupts bool
	outputError      *teeOutputErrorMode
	files            []string
	showHelp         bool
	showVersion      bool
}

type teeWriter struct {
	name          string
	writer        io.Writer
	closer        io.Closer
	path          string
	recordAction  string
	recordOnClose bool
}

type teeMultiWriter struct {
	writers         []*teeWriter
	allWriters      []*teeWriter
	outputError     *teeOutputErrorMode
	ignoredErrors   int
	silentWriteFail bool
	stderr          io.Writer
}

type teeInputReader struct {
	reader io.Reader
	stderr io.Writer
}

type teeFlusher interface {
	Flush() error
}

type Tee struct{}

func NewTee() *Tee {
	return &Tee{}
}

func (c *Tee) Name() string {
	return "tee"
}

func (c *Tee) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Tee) Spec() CommandSpec {
	return teeCommandSpec()
}

func (c *Tee) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := teeOptionsFromMatches(matches)
	if err != nil {
		return err
	}

	// GNU tee can ignore SIGINT with -i, but command execution here is in-process
	// with the shell runtime, so mutating process-wide signal handlers is unsafe.
	// We accept the flag and keep sandbox-local behavior unchanged.
	_ = opts.ignoreInterrupts

	writers, hadOpenErrors, err := openTeeWriters(ctx, inv, opts)
	if err != nil {
		return err
	}
	multi := newTeeMultiWriter(writers, opts.outputError, inv.Stderr)
	input := &teeInputReader{reader: inv.Stdin, stderr: inv.Stderr}

	copyErr := teeCopy(input, multi)
	closeErr := multi.closeAndTrace(inv)
	switch {
	case errors.Is(copyErr, errTeeNoWriters):
		copyErr = nil
	case errors.Is(copyErr, errTeeAbort), errors.Is(copyErr, errTeeAbortQuiet):
		copyErr = nil
	}

	if hadOpenErrors || multi.ignoredErrors != 0 || multi.silentWriteFail || closeErr != nil || copyErr != nil {
		if closeErr != nil {
			return &ExitError{Code: 1, Err: closeErr}
		}
		if copyErr != nil {
			return &ExitError{Code: 1, Err: copyErr}
		}
		return &ExitError{Code: 1}
	}
	return nil
}

func teeCommandSpec() CommandSpec {
	return CommandSpec{
		Name:      "tee",
		About:     "Copy standard input to each FILE, and also to standard output.",
		Usage:     "tee [OPTION]... [FILE]...",
		AfterHelp: "MODE determines behavior with write errors on outputs:\n  warn         diagnose errors writing to any output\n  warn-nopipe  diagnose errors writing to any output not a pipe\n  exit         exit on error writing to any output\n  exit-nopipe  exit on error writing to any output not a pipe",
		Options: []OptionSpec{
			{Name: "append", Short: 'a', Long: "append", Help: "append to the given FILEs, do not overwrite"},
			{Name: "ignore-interrupts", Short: 'i', Long: "ignore-interrupts", Help: "ignore interrupt signals"},
			{Name: "ignore-pipe-errors", Short: 'p', Help: "diagnose errors writing to non pipes"},
			{Name: "output-error", Long: "output-error", ValueName: "MODE", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "set behavior on write error; see MODE below"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: false,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		HelpRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, teeHelpText)
			return err
		},
	}
}

func parseTeeArgs(inv *Invocation) (teeOptions, error) {
	spec := teeCommandSpec()
	matches, action, err := ParseCommandSpec(inv, &spec)
	if err != nil {
		return teeOptions{}, err
	}
	opts, err := teeOptionsFromMatches(matches)
	if err != nil {
		return teeOptions{}, err
	}
	opts.showHelp = action == "help"
	opts.showVersion = action == "version"
	return opts, nil
}

func teeOptionsFromMatches(matches *ParsedCommand) (teeOptions, error) {
	opts := teeOptions{
		append:           matches.Has("append"),
		ignoreInterrupts: matches.Has("ignore-interrupts"),
		files:            matches.Args("file"),
	}
	if matches.Has("output-error") {
		mode, err := parseTeeOutputErrorValue(&Invocation{}, matches.Value("output-error"), matches.Value("output-error") != "")
		if err != nil {
			return teeOptions{}, err
		}
		opts.outputError = mode
	}
	if opts.outputError == nil && matches.Has("ignore-pipe-errors") {
		mode := teeOutputErrorWarnNoPipe
		opts.outputError = &mode
	}
	return opts, nil
}

func parseTeeOutputErrorValue(inv *Invocation, raw string, hasValue bool) (*teeOutputErrorMode, error) {
	if !hasValue {
		mode := teeOutputErrorWarnNoPipe
		return &mode, nil
	}
	value, err := matchTeeOutputErrorValue(raw)
	if err != nil {
		return nil, teeInvalidOutputErrorValue(inv, raw)
	}
	mode := map[string]teeOutputErrorMode{
		"warn":        teeOutputErrorWarn,
		"warn-nopipe": teeOutputErrorWarnNoPipe,
		"exit":        teeOutputErrorExit,
		"exit-nopipe": teeOutputErrorExitNoPipe,
	}[value]
	return &mode, nil
}

func matchTeeOutputErrorValue(raw string) (string, error) {
	values := []string{"warn", "warn-nopipe", "exit", "exit-nopipe"}
	exact := ""
	matches := make([]string, 0, len(values))
	for _, value := range values {
		if value == raw {
			exact = value
		}
		if strings.HasPrefix(value, raw) {
			matches = append(matches, value)
		}
	}
	if exact != "" {
		return exact, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", errors.New("no output-error match")
}

func openTeeWriters(ctx context.Context, inv *Invocation, opts teeOptions) ([]*teeWriter, bool, error) {
	writers := make([]*teeWriter, 0, len(opts.files)+1)
	hadOpenErrors := false

	for _, name := range opts.files {
		abs, err := allowPath(ctx, inv, policy.FileActionWrite, name)
		if err != nil {
			return nil, false, err
		}
		if err := ensureParentDirExists(ctx, inv, abs); err != nil {
			reported := teeWriteOpenError(inv.Stderr, name, err)
			if teeOutputErrorExitsOnOpen(opts.outputError) {
				return nil, false, &ExitError{Code: 1, Err: reported}
			}
			hadOpenErrors = true
			continue
		}

		flag := os.O_CREATE | os.O_WRONLY
		recordAction := "write"
		recordOnClose := !opts.append
		if opts.append {
			flag |= os.O_APPEND
			recordAction = "append"
		} else {
			flag |= os.O_TRUNC
		}

		file, err := inv.FS.OpenFile(ctx, abs, flag, 0o644)
		if err != nil {
			reported := teeWriteOpenError(inv.Stderr, name, err)
			if teeOutputErrorExitsOnOpen(opts.outputError) {
				return nil, false, &ExitError{Code: 1, Err: reported}
			}
			hadOpenErrors = true
			continue
		}
		writers = append(writers, &teeWriter{
			name:          name,
			writer:        file,
			closer:        file,
			path:          abs,
			recordAction:  recordAction,
			recordOnClose: recordOnClose,
		})
	}

	writers = append([]*teeWriter{{
		name:   "standard output",
		writer: inv.Stdout,
	}}, writers...)
	return writers, hadOpenErrors, nil
}

func teeOutputErrorExitsOnOpen(mode *teeOutputErrorMode) bool {
	return mode != nil && (*mode == teeOutputErrorExit || *mode == teeOutputErrorExitNoPipe)
}

func newTeeMultiWriter(writers []*teeWriter, outputError *teeOutputErrorMode, stderr io.Writer) *teeMultiWriter {
	return &teeMultiWriter{
		writers:     writers,
		allWriters:  append([]*teeWriter(nil), writers...),
		outputError: outputError,
		stderr:      stderr,
	}
}

func teeCopy(input io.Reader, output *teeMultiWriter) error {
	var buf [teeBufferSize]byte
	for {
		n, err := input.Read(buf[:])
		if n > 0 {
			if writeErr := output.writeAndFlush(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func (w *teeMultiWriter) writeAndFlush(buf []byte) error {
	kept := w.writers[:0]
	for _, writer := range w.writers {
		if err := teeWriteAll(writer.writer, buf); err != nil {
			action, actionErr := w.handleWriteError(writer, err)
			if actionErr != nil {
				return actionErr
			}
			if action == "keep" {
				kept = append(kept, writer)
			}
			continue
		}
		if err := teeFlush(writer.writer); err != nil {
			action, actionErr := w.handleWriteError(writer, err)
			if actionErr != nil {
				return actionErr
			}
			if action == "keep" {
				kept = append(kept, writer)
			}
			continue
		}
		writer.recordOnClose = true
		kept = append(kept, writer)
	}
	w.writers = kept
	if len(w.writers) == 0 {
		return errTeeNoWriters
	}
	return nil
}

func (w *teeMultiWriter) handleWriteError(writer *teeWriter, err error) (string, error) {
	switch mode := w.outputError; {
	case mode == nil:
		if teeIsBrokenPipe(err) {
			w.silentWriteFail = true
			return "drop", errTeeAbortQuiet
		}
		teeWriteWriterError(w.stderr, writer.name, err)
		w.ignoredErrors++
		return "drop", nil
	case *mode == teeOutputErrorWarn:
		teeWriteWriterError(w.stderr, writer.name, err)
		w.ignoredErrors++
		return "drop", nil
	case *mode == teeOutputErrorWarnNoPipe:
		if !teeIsBrokenPipe(err) {
			teeWriteWriterError(w.stderr, writer.name, err)
			w.ignoredErrors++
		}
		return "drop", nil
	case *mode == teeOutputErrorExit:
		teeWriteWriterError(w.stderr, writer.name, err)
		return "drop", errTeeAbort
	case *mode == teeOutputErrorExitNoPipe:
		if teeIsBrokenPipe(err) {
			return "drop", nil
		}
		teeWriteWriterError(w.stderr, writer.name, err)
		return "drop", errTeeAbort
	default:
		return "drop", err
	}
}

func (w *teeMultiWriter) closeAndTrace(inv *Invocation) error {
	var firstErr error
	for _, writer := range w.allWriters {
		if writer.path != "" && writer.recordOnClose {
			recordFileMutation(inv.TraceRecorder(), writer.recordAction, writer.path, writer.path, writer.path)
		}
		if writer.closer != nil {
			if err := writer.closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func teeWriteAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func teeFlush(writer io.Writer) error {
	if flusher, ok := writer.(teeFlusher); ok {
		return flusher.Flush()
	}
	return nil
}

func (r *teeInputReader) Read(buf []byte) (int, error) {
	n, err := r.reader.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintf(r.stderr, "tee: error reading standard input: %v\n", err)
	}
	return n, err
}

func teeIsBrokenPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE)
}

func teeWriteOpenError(stderr io.Writer, name string, err error) error {
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "tee: %s: %v\n", name, err)
	}
	return fmt.Errorf("tee: %s: %w", name, err)
}

func teeWriteWriterError(stderr io.Writer, name string, err error) {
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "tee: %s: %v\n", name, err)
	}
}

func teeInvalidOutputErrorValue(inv *Invocation, value string) error {
	return exitf(inv, 1, "tee: invalid argument '%s' for '--output-error'\nValid arguments are:\n  - 'warn'\n  - 'warn-nopipe'\n  - 'exit'\n  - 'exit-nopipe'", value)
}

var teeHelpText = strings.TrimLeft(`
Usage: tee [OPTION]... [FILE]...
Copy standard input to each FILE, and also to standard output.

  -a, --append              append to the given FILEs, do not overwrite
  -i, --ignore-interrupts   ignore interrupt signals
  -p                        diagnose errors writing to non pipes
      --output-error[=MODE] set behavior on write error; see MODE below
  -h, --help                display this help and exit
      --version             output version information and exit

MODE determines behavior with write errors on outputs:
  warn         diagnose errors writing to any output
  warn-nopipe  diagnose errors writing to any output not a pipe
  exit         exit on error writing to any output
  exit-nopipe  exit on error writing to any output not a pipe
`, "\n")

var _ Command = (*Tee)(nil)
