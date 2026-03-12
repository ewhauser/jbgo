package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"strings"

	"github.com/ewhauser/gbash/policy"
	"golang.org/x/sys/unix"
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
	showHelp     bool
	showVersion  bool
}

type catOutputState struct {
	lineNumber            int
	atLineStart           bool
	pendingCarriageReturn bool
	oneBlankKept          bool
}

type catRedirectHandle interface {
	RedirectMetadata
	Stat() (stdfs.FileInfo, error)
}

type catHostHandle interface {
	Stat() (stdfs.FileInfo, error)
	Seek(offset int64, whence int) (int64, error)
	Fd() uintptr
}

func NewCat() *Cat {
	return &Cat{}
}

func (c *Cat) Name() string {
	return "cat"
}

func (c *Cat) Run(ctx context.Context, inv *Invocation) error {
	opts, names, err := parseCatArgs(inv)
	if err != nil {
		return err
	}
	if opts.showHelp {
		_, _ = io.WriteString(inv.Stdout, catHelpText)
		return nil
	}
	if opts.showVersion {
		_, _ = io.WriteString(inv.Stdout, catVersionText)
		return nil
	}
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

		data, label, err := readCatInput(ctx, inv, name)
		if err != nil {
			if code, ok := ExitCode(err); ok && code == 126 {
				return err
			}
			failures = append(failures, catErrorMessage(name, label, err))
			continue
		}
		if err := writeCatData(inv.Stdout, data, opts, &state); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if state.pendingCarriageReturn {
		if opts.showNonprint {
			if _, err := io.WriteString(inv.Stdout, "^M"); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		} else {
			if _, err := inv.Stdout.Write([]byte{'\r'}); err != nil {
				return &ExitError{Code: 1, Err: err}
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

func parseCatArgs(inv *Invocation) (opts catOptions, names []string, err error) {
	args := append([]string(nil), inv.Args...)
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			return opts, args[1:], nil
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		if strings.HasPrefix(arg, "--") {
			mode, matched, parseErr := parseCatLongOption(inv, arg, &opts)
			if parseErr != nil {
				return opts, nil, parseErr
			}
			if mode != "" {
				if mode == "help" {
					opts.showHelp = true
				} else {
					opts.showVersion = true
				}
				return opts, nil, nil
			}
			if !matched {
				break
			}
			args = args[1:]
			continue
		}

		matched, mode, parseErr := parseCatShortOptionGroup(inv, arg, &opts)
		if parseErr != nil {
			return opts, nil, parseErr
		}
		if mode != "" {
			if mode == "help" {
				opts.showHelp = true
			} else {
				opts.showVersion = true
			}
			return opts, nil, nil
		}
		if !matched {
			break
		}
		args = args[1:]
	}
	return opts, args, nil
}

func parseCatLongOption(inv *Invocation, arg string, opts *catOptions) (mode string, matched bool, err error) {
	name := strings.TrimPrefix(arg, "--")
	match, ok, err := matchCatLongOption(inv, name)
	if err != nil || !ok {
		return "", ok, err
	}
	switch match {
	case "help":
		return "help", true, nil
	case "version":
		return "version", true, nil
	case "number":
		if opts.number != catNumberNonBlank {
			opts.number = catNumberAll
		}
	case "number-nonblank":
		opts.number = catNumberNonBlank
	case "show-all":
		opts.showTabs = true
		opts.showEnds = true
		opts.showNonprint = true
	case "show-ends":
		opts.showEnds = true
	case "show-tabs":
		opts.showTabs = true
	case "show-nonprinting":
		opts.showNonprint = true
	case "squeeze-blank":
		opts.squeezeBlank = true
	default:
		return "", false, exitf(inv, 1, "cat: unrecognized option '%s'", arg)
	}
	return "", true, nil
}

func matchCatLongOption(inv *Invocation, name string) (match string, matched bool, err error) {
	candidates := []string{
		"help",
		"number",
		"number-nonblank",
		"show-all",
		"show-ends",
		"show-nonprinting",
		"show-tabs",
		"squeeze-blank",
		"version",
	}
	for _, candidate := range candidates {
		if candidate == name {
			return candidate, true, nil
		}
	}
	var matches []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, name) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return "", false, catOptionf(inv, "cat: unrecognized option '%s'", "--"+name)
	case 1:
		return matches[0], true, nil
	default:
		return "", false, catOptionf(inv, "cat: option '%s' is ambiguous", "--"+name)
	}
}

func parseCatShortOptionGroup(inv *Invocation, arg string, opts *catOptions) (matched bool, mode string, err error) {
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'A':
			opts.showTabs = true
			opts.showEnds = true
			opts.showNonprint = true
		case 'b':
			opts.number = catNumberNonBlank
		case 'e':
			opts.showEnds = true
			opts.showNonprint = true
		case 'E':
			opts.showEnds = true
		case 'n':
			if opts.number != catNumberNonBlank {
				opts.number = catNumberAll
			}
		case 's':
			opts.squeezeBlank = true
		case 't':
			opts.showTabs = true
			opts.showNonprint = true
		case 'T':
			opts.showTabs = true
		case 'u':
		case 'v':
			opts.showNonprint = true
		default:
			return false, "", catOptionf(inv, "cat: invalid option -- '%c'", arg[i])
		}
	}
	return true, "", nil
}

func catWouldUnsafeOverwrite(ctx context.Context, inv *Invocation, name string) (bool, error) {
	output, ok := inv.Stdout.(catRedirectHandle)
	if ok {
		if name == "-" {
			input, ok := inv.Stdin.(catRedirectHandle)
			if !ok || input.RedirectPath() != output.RedirectPath() {
				return false, nil
			}
			return catIsUnsafeOverwrite(input.RedirectOffset(), output), nil
		}

		abs, err := allowPath(ctx, inv, policy.FileActionRead, name)
		if err != nil {
			return false, err
		}
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
		return catIsUnsafeHostOverwrite(inputHost, outputHost)
	}

	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return false, err
	}
	outputInfo, err := outputHost.Stat()
	if err != nil || !testSameFile(info, outputInfo) {
		return false, nil
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

func catIsUnsafeHostOverwrite(input, output catHostHandle) (bool, error) {
	inputInfo, err := input.Stat()
	if err != nil {
		return false, nil
	}
	outputInfo, err := output.Stat()
	if err != nil || !testSameFile(inputInfo, outputInfo) {
		return false, nil
	}
	return catUnsafeByOffsets(catHostOffset(input), outputInfo.Size(), catHostAppendMode(output), catHostOffset(output)), nil
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

func catHostAppendMode(file catHostHandle) bool {
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return false
	}
	return flags&unix.O_APPEND != 0
}

func readCatInput(ctx context.Context, inv *Invocation, name string) (data []byte, label string, err error) {
	if name == "-" {
		data, err := io.ReadAll(inv.Stdin)
		return data, name, err
	}
	abs, err := allowPath(ctx, inv, policy.FileActionRead, name)
	if err != nil {
		return nil, "", err
	}
	file, openErr := inv.FS.Open(ctx, abs)
	if openErr != nil {
		if errors.Is(openErr, stdfs.ErrInvalid) {
			info, _, statErr := statPath(ctx, inv, name)
			if statErr == nil && info != nil && info.IsDir() {
				return nil, abs, errors.New("is a directory")
			}
		}
		return nil, abs, openErr
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr == nil && info != nil && info.IsDir() {
		return nil, abs, errors.New("is a directory")
	}
	data, err = io.ReadAll(file)
	return data, abs, err
}

func catErrorMessage(name, label string, err error) string {
	if err == nil {
		return name
	}
	message := err.Error()
	if message == "is a directory" {
		message = "Is a directory"
	}
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

func writeCatNewLine(w io.Writer, opts catOptions, state *catOutputState) error {
	if state == nil {
		return nil
	}
	if !state.atLineStart || !opts.squeezeBlank || !state.oneBlankKept {
		state.oneBlankKept = true
		if state.atLineStart && opts.number == catNumberAll {
			if err := writeCatLineNumber(w, state.lineNumber); err != nil {
				return err
			}
			state.lineNumber++
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

func catOptionf(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, format+"\nTry 'cat --help' for more information.", args...)
}

var _ Command = (*Cat)(nil)
