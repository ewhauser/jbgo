package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/ewhauser/gbash/internal/commandutil"
)

type Cat struct{}

type catNumberingMode int

const (
	catNumberNone catNumberingMode = iota
	catNumberNonBlank
	catNumberAll
)

type catOptions struct {
	number       catNumberingMode
	squeezeBlank bool
	showTabs     bool
	showEnds     bool
	showNonprint bool
}

type catOutputState struct {
	lineNumber            int
	atLineStart           bool
	pendingCarriageReturn bool
	oneBlankKept          bool
}

type catInput struct {
	data   []byte
	reader io.Reader
	label  string
	close  func()
}

type catRedirectHandle interface {
	commandutil.RedirectMetadata
	Stat() (stdfs.FileInfo, error)
}

type catHostHandle interface {
	Stat() (stdfs.FileInfo, error)
	Seek(offset int64, whence int) (int64, error)
	Fd() uintptr
}

type catStatHandle interface {
	Stat() (stdfs.FileInfo, error)
}

func NewCat() *Cat {
	return &Cat{}
}

func (c *Cat) Name() string {
	return "cat"
}

func (c *Cat) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Cat) Spec() CommandSpec {
	return CommandSpec{
		Name:  "cat",
		Usage: "cat [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "show-all", Short: 'A', Long: "show-all", Help: "equivalent to -vET"},
			{Name: "number-nonblank", Short: 'b', Long: "number-nonblank", Help: "number nonempty output lines"},
			{Name: "show-nonprinting-ends", Short: 'e', Help: "equivalent to -vE"},
			{Name: "show-ends", Short: 'E', Long: "show-ends", Help: "display $ at end of each line"},
			{Name: "number", Short: 'n', Long: "number", Help: "number all output lines"},
			{Name: "squeeze-blank", Short: 's', Long: "squeeze-blank", Help: "suppress repeated empty output lines"},
			{Name: "show-nonprinting-tabs", Short: 't', Help: "equivalent to -vT"},
			{Name: "show-tabs", Short: 'T', Long: "show-tabs", Help: "display TAB characters as ^I"},
			{Name: "ignored-u", Short: 'u', Help: "ignored"},
			{Name: "show-nonprinting", Short: 'v', Long: "show-nonprinting", Help: "use ^ and M- notation, except for LFD and TAB"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
		},
		HelpRenderer:    renderStaticHelp(catHelpText),
		VersionRenderer: renderStaticVersion(catVersionText),
	}
}

func (c *Cat) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return renderStaticHelp(catHelpText)(inv.Stdout, c.Spec())
	}
	if matches.Has("version") {
		return renderStaticVersion(catVersionText)(inv.Stdout, c.Spec())
	}
	opts := catOptionsFromParsed(matches)
	names := matches.Args("file")
	if len(names) == 0 {
		names = []string{"-"}
	}

	state := catOutputState{
		lineNumber:  1,
		atLineStart: true,
	}
	var failures []string
	for _, name := range names {
		unsafe, err := catWouldUnsafeOverwrite(ctx, inv, name)
		if err != nil {
			return err
		}
		if unsafe {
			failures = append(failures, fmt.Sprintf("%s: input file is output file", name))
			continue
		}

		input, err := readCatInput(ctx, inv, name)
		if err != nil {
			if code, ok := ExitCode(err); ok && code == 126 {
				return err
			}
			failures = append(failures, catErrorMessage(name, catInputLabel(input, name), err))
			continue
		}
		if err := writeCatInput(inv.Stdout, input, opts, &state); err != nil {
			if input.close != nil {
				input.close()
			}
			return catWriteExitError(err)
		}
		if input.close != nil {
			input.close()
		}
	}

	if state.pendingCarriageReturn {
		if opts.showNonprint {
			if _, err := io.WriteString(inv.Stdout, "^M"); err != nil {
				return catWriteExitError(err)
			}
		} else {
			if _, err := inv.Stdout.Write([]byte{'\r'}); err != nil {
				return catWriteExitError(err)
			}
		}
	}

	if len(failures) == 0 {
		return nil
	}
	for _, failure := range failures {
		_, _ = fmt.Fprintf(inv.Stderr, "cat: %s\n", failure)
	}
	return &ExitError{Code: len(failures), Err: errors.New("cat: " + failures[0])}
}

func catOptionsFromParsed(matches *ParsedCommand) catOptions {
	opts := catOptions{
		squeezeBlank: matches.Has("squeeze-blank"),
		showTabs:     matches.Has("show-all") || matches.Has("show-tabs") || matches.Has("show-nonprinting-tabs"),
		showEnds:     matches.Has("show-all") || matches.Has("show-ends") || matches.Has("show-nonprinting-ends"),
		showNonprint: matches.Has("show-all") || matches.Has("show-nonprinting") || matches.Has("show-nonprinting-ends") || matches.Has("show-nonprinting-tabs"),
	}
	if matches.Has("number-nonblank") {
		opts.number = catNumberNonBlank
	} else if matches.Has("number") {
		opts.number = catNumberAll
	}
	return opts
}

func catWouldUnsafeOverwrite(ctx context.Context, inv *Invocation, name string) (bool, error) {
	output, ok := inv.Stdout.(catRedirectHandle)
	if ok && output.RedirectPath() != "" {
		if name == "-" {
			input, ok := inv.Stdin.(catRedirectHandle)
			if !ok || input.RedirectPath() == "" || input.RedirectPath() != output.RedirectPath() {
				return false, nil
			}
			return catIsUnsafeOverwrite(input.RedirectOffset(), output), nil
		}

		abs := allowPath(inv, name)
		if abs != output.RedirectPath() {
			return false, nil
		}
		return catIsUnsafeOverwrite(0, output), nil
	}

	outputHost, ok := inv.Stdout.(catHostHandle)
	if !ok {
		return false, nil
	}
	if name == "-" {
		inputHost, ok := inv.Stdin.(catHostHandle)
		if !ok {
			return false, nil
		}
		return catIsUnsafeHostOverwrite(inputHost, outputHost), nil
	}

	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return false, err
	}
	outputInfo, err := outputHost.Stat()
	if err != nil {
		return false, nil
	}
	if !testSameFile(info, outputInfo) {
		file, _, openErr := openRead(ctx, inv, name)
		if openErr != nil {
			return false, openErr
		}
		defer func() { _ = file.Close() }()
		info, err = file.Stat()
		if err != nil || !testSameFile(info, outputInfo) {
			return false, nil
		}
	}
	return catUnsafeByOffsets(0, outputInfo.Size(), catHostAppendMode(outputHost), catHostOffset(outputHost)), nil
}

func catIsUnsafeOverwrite(inputOffset int64, output catRedirectHandle) bool {
	info, err := output.Stat()
	if err != nil || info == nil || info.Size() == 0 {
		return false
	}
	return catUnsafeByOffsets(inputOffset, info.Size(), output.RedirectFlags()&os.O_APPEND != 0, output.RedirectOffset())
}

func catIsUnsafeHostOverwrite(input, output catHostHandle) bool {
	inputInfo, err := input.Stat()
	if err != nil {
		return false
	}
	outputInfo, err := output.Stat()
	if err != nil || !testSameFile(inputInfo, outputInfo) {
		return false
	}
	return catUnsafeByOffsets(catHostOffset(input), outputInfo.Size(), catHostAppendMode(output), catHostOffset(output))
}

func catUnsafeByOffsets(inputOffset, outputSize int64, appendMode bool, outputOffset int64) bool {
	if outputSize == 0 {
		return false
	}
	if appendMode {
		return inputOffset < outputSize
	}
	return inputOffset < outputOffset
}

func catHostOffset(file catHostHandle) int64 {
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

func readCatInput(ctx context.Context, inv *Invocation, name string) (catInput, error) {
	if name == "-" {
		return readCatStdin(ctx, inv)
	}
	abs := allowPath(inv, name)
	file, openErr := inv.FS.Open(ctx, abs)
	if openErr != nil {
		if errors.Is(openErr, stdfs.ErrInvalid) {
			info, _, statErr := statPath(ctx, inv, name)
			if statErr == nil && info != nil && info.IsDir() {
				return catInput{label: abs}, errors.New("is a directory")
			}
		}
		return catInput{label: abs}, openErr
	}
	info, statErr := file.Stat()
	if statErr == nil && info != nil && info.IsDir() {
		_ = file.Close()
		return catInput{label: abs}, errors.New("is a directory")
	}
	shouldStream := statErr != nil || catShouldStreamInfo(info)
	if shouldStream {
		return catInput{
			reader: commandutil.ReaderWithContext(ctx, file),
			label:  abs,
			close: func() {
				_ = file.Close()
			},
		}, nil
	}
	data, err := readAllReader(ctx, inv, file)
	_ = file.Close()
	return catInput{data: data, label: abs}, err
}

func readCatStdin(ctx context.Context, inv *Invocation) (catInput, error) {
	reader := io.Reader(strings.NewReader(""))
	if inv != nil && inv.Stdin != nil {
		reader = inv.Stdin
	}
	if info, ok := catReaderInfo(reader); ok && !catShouldStreamInfo(info) {
		data, err := readAllReader(ctx, inv, reader)
		return catInput{data: data, label: "-"}, err
	}
	return catInput{
		reader: commandutil.ReaderWithContext(ctx, reader),
		label:  "-",
	}, nil
}

func catReaderInfo(reader io.Reader) (stdfs.FileInfo, bool) {
	handle, ok := reader.(catStatHandle)
	if !ok {
		return nil, false
	}
	info, err := handle.Stat()
	if err != nil || info == nil {
		return nil, false
	}
	return info, true
}

func catShouldStreamInfo(info stdfs.FileInfo) bool {
	return info == nil || !info.Mode().IsRegular()
}

func catInputLabel(input catInput, fallback string) string {
	if input.label != "" {
		return input.label
	}
	return fallback
}

func catErrorMessage(name, label string, err error) string {
	if err == nil {
		return name
	}
	message := readAllErrorText(err)
	for _, prefix := range []string{
		label + ": ",
		"open " + label + ": ",
		"stat " + label + ": ",
		name + ": ",
		"open " + name + ": ",
		"stat " + name + ": ",
	} {
		if prefix == ": " || prefix == "open : " || prefix == "stat : " {
			continue
		}
		message = strings.TrimPrefix(message, prefix)
	}
	return fmt.Sprintf("%s: %s", name, message)
}

func writeCatInput(w io.Writer, input catInput, opts catOptions, state *catOutputState) error {
	if input.reader != nil {
		return writeCatReader(w, input.reader, opts, state)
	}
	return writeCatData(w, input.data, opts, state)
}

func writeCatReader(w io.Writer, reader io.Reader, opts catOptions, state *catOutputState) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if writeErr := writeCatData(w, buf[:n], opts, state); writeErr != nil {
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

func writeCatData(w io.Writer, data []byte, opts catOptions, state *catOutputState) error {
	if state == nil {
		state = &catOutputState{lineNumber: 1, atLineStart: true}
	}

	for _, b := range data {
		if state.pendingCarriageReturn {
			if b == '\n' {
				if opts.showEnds {
					if _, err := io.WriteString(w, "^M"); err != nil {
						return err
					}
				} else {
					if _, err := w.Write([]byte{'\r'}); err != nil {
						return err
					}
				}
				state.pendingCarriageReturn = false
				if err := writeCatNewLine(w, opts, state); err != nil {
					return err
				}
				continue
			}
			if opts.showNonprint {
				if _, err := io.WriteString(w, "^M"); err != nil {
					return err
				}
			} else {
				if _, err := w.Write([]byte{'\r'}); err != nil {
					return err
				}
			}
			state.pendingCarriageReturn = false
			state.atLineStart = false
		}

		if b == '\r' && !opts.showNonprint {
			state.pendingCarriageReturn = true
			continue
		}
		if b == '\n' {
			if err := writeCatNewLine(w, opts, state); err != nil {
				return err
			}
			continue
		}

		state.oneBlankKept = false
		if state.atLineStart && opts.number != catNumberNone {
			if err := writeCatLineNumber(w, state.lineNumber); err != nil {
				return err
			}
			state.lineNumber++
		}
		if err := writeCatByte(w, b, opts); err != nil {
			return err
		}
		state.atLineStart = false
	}
	return nil
}

func catWriteExitError(err error) error {
	if err == nil {
		return nil
	}
	if catBrokenPipe(err) {
		return &ExitError{Code: 141}
	}
	return &ExitError{Code: 1, Err: err}
}

func catBrokenPipe(err error) bool {
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "broken pipe") || strings.Contains(lower, "closed pipe")
}

func writeCatNewLine(w io.Writer, opts catOptions, state *catOutputState) error {
	if state == nil {
		return nil
	}
	if state.atLineStart {
		if opts.squeezeBlank && state.oneBlankKept {
			return nil
		}
		if opts.number == catNumberAll {
			if err := writeCatLineNumber(w, state.lineNumber); err != nil {
				return err
			}
			state.lineNumber++
		}
		state.oneBlankKept = true
	} else {
		state.oneBlankKept = false
	}
	if opts.showEnds {
		if _, err := io.WriteString(w, "$\n"); err != nil {
			return err
		}
	} else {
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	state.atLineStart = true
	return nil
}

func writeCatLineNumber(w io.Writer, lineNumber int) error {
	_, err := fmt.Fprintf(w, "%6d\t", lineNumber)
	return err
}

func writeCatByte(w io.Writer, b byte, opts catOptions) error {
	if opts.showNonprint {
		switch {
		case b == '\t':
			if opts.showTabs {
				_, err := io.WriteString(w, "^I")
				return err
			}
			_, err := w.Write([]byte{'\t'})
			return err
		case b <= 8 || (b >= 10 && b <= 31):
			_, err := w.Write([]byte{'^', b + 64})
			return err
		case b >= 32 && b <= 126:
			_, err := w.Write([]byte{b})
			return err
		case b == 127:
			_, err := io.WriteString(w, "^?")
			return err
		case b >= 128 && b <= 159:
			_, err := w.Write([]byte{'M', '-', '^', b - 64})
			return err
		case b >= 160 && b <= 254:
			_, err := w.Write([]byte{'M', '-', b - 128})
			return err
		default:
			_, err := io.WriteString(w, "M-^?")
			return err
		}
	}
	if opts.showTabs && b == '\t' {
		_, err := io.WriteString(w, "^I")
		return err
	}
	_, err := w.Write([]byte{b})
	return err
}

const catHelpText = `Usage: cat [OPTION]... [FILE]...
Concatenate FILE(s) to standard output.

  -A, --show-all           equivalent to -vET
  -b, --number-nonblank    number nonempty output lines
  -e                       equivalent to -vE
  -E, --show-ends          display $ at end of each line
  -n, --number             number all output lines
  -s, --squeeze-blank      suppress repeated empty output lines
  -t                       equivalent to -vT
  -T, --show-tabs          display TAB characters as ^I
  -u                       ignored
  -v, --show-nonprinting   use ^ and M- notation, except for LFD and TAB
      --help               display this help and exit
      --version            output version information and exit
`

const catVersionText = "cat (gbash) dev\n"

var _ Command = (*Cat)(nil)
var _ SpecProvider = (*Cat)(nil)
var _ ParsedRunner = (*Cat)(nil)
