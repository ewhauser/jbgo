package builtins

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"strings"
)

type RM struct{}

type rmInteractiveMode uint8

const (
	rmInteractivePromptProtected rmInteractiveMode = iota
	rmInteractiveNever
	rmInteractiveOnce
	rmInteractiveAlways
)

type rmOptions struct {
	force           bool
	interactive     rmInteractiveMode
	oneFileSystem   bool
	preserveRoot    bool
	recursive       bool
	dir             bool
	verbose         bool
	progress        bool
	presumeInputTTY bool
	promptInput     *rmPromptInput
}

type rmPromptInput struct {
	reader *bufio.Reader
	closer io.Closer
}

type rmResult struct {
	hadErr  bool
	removed bool
}

func NewRM() *RM {
	return &RM{}
}

func (c *RM) Name() string {
	return "rm"
}

func (c *RM) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *RM) Spec() CommandSpec {
	return CommandSpec{
		Name:  "rm",
		About: "Remove (unlink) the FILE(s).",
		Usage: "rm [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "force", Short: 'f', Long: "force", Help: "ignore nonexistent files and arguments, never prompt"},
			{Name: "prompt-always", Short: 'i', Help: "prompt before every removal"},
			{Name: "prompt-once", Short: 'I', Help: "prompt once before removing more than three files, or when removing recursively"},
			{Name: "interactive", Long: "interactive", ValueName: "WHEN", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "prompt according to WHEN: never, once, or always; without WHEN, prompt always"},
			{Name: "one-file-system", Long: "one-file-system", Help: "stay on one file system when removing recursively"},
			{Name: "preserve-root", Long: "preserve-root", Help: "fail to operate recursively on '/'"},
			{Name: "no-preserve-root", Long: "no-preserve-root", Help: "do not treat '/' specially"},
			{Name: "recursive", Short: 'r', ShortAliases: []rune{'R'}, Long: "recursive", Help: "remove directories and their contents recursively"},
			{Name: "dir", Short: 'd', Long: "dir", Help: "remove empty directories"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "explain what is being done"},
			{Name: "progress", Short: 'g', Long: "progress", Help: "accepted for compatibility"},
			{
				Name:    "presume-input-tty",
				Long:    "presume-input-tty",
				Aliases: []string{"-presume-input-tty"},
				Hidden:  true,
				Help:    "accepted for compatibility",
			},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
	}
}

func (c *RM) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseRMMatches(inv, matches)
	if err != nil {
		return err
	}
	defer func() { _ = rmClosePromptInput(opts.promptInput) }()

	files := matches.Args("file")
	if len(files) == 0 {
		if opts.force {
			return nil
		}
		return commandUsageError(inv, c.Name(), "missing operand")
	}

	if opts.interactive == rmInteractiveOnce && (opts.recursive || len(files) > 3) {
		noun := "argument"
		if len(files) != 1 {
			noun = "arguments"
		}
		suffix := "?"
		if opts.recursive {
			suffix = " recursively?"
		}
		ok, err := rmPromptYes(ctx, inv, fmt.Sprintf("remove %d %s%s", len(files), noun, suffix), opts)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}

	hadErr := false
	for _, name := range files {
		result, err := rmRemovePath(ctx, inv, name, opts)
		if err != nil {
			return err
		}
		hadErr = hadErr || result.hadErr
	}
	if hadErr {
		return &ExitError{Code: 1}
	}
	return nil
}

func parseRMMatches(inv *Invocation, matches *ParsedCommand) (rmOptions, error) {
	opts := rmOptions{
		interactive:  rmInteractivePromptProtected,
		preserveRoot: true,
		promptInput:  &rmPromptInput{},
	}

	for _, occurrence := range matches.OptionOccurrences() {
		switch occurrence.Name {
		case "force":
			opts.force = true
			opts.interactive = rmInteractiveNever
		case "prompt-always":
			opts.force = false
			opts.interactive = rmInteractiveAlways
		case "prompt-once":
			opts.force = false
			opts.interactive = rmInteractiveOnce
		case "interactive":
			mode, err := parseRMInteractiveMode(inv, occurrence.Value, occurrence.HasValue)
			if err != nil {
				return rmOptions{}, err
			}
			if mode != rmInteractiveNever {
				opts.force = false
			}
			opts.interactive = mode
		case "one-file-system":
			// The sandbox filesystem abstraction does not currently expose mount
			// device boundaries, so this remains a compatibility no-op.
			opts.oneFileSystem = true
		case "preserve-root":
			opts.preserveRoot = true
		case "no-preserve-root":
			if occurrence.Raw != "--no-preserve-root" {
				return rmOptions{}, exitf(inv, 1, "rm: you may not abbreviate the --no-preserve-root option")
			}
			opts.preserveRoot = false
		case "recursive":
			opts.recursive = true
		case "dir":
			opts.dir = true
		case "verbose":
			opts.verbose = true
		case "progress":
			// Accepted for compatibility. The sandbox command surface does not
			// expose a progress UI for builtins.
			opts.progress = true
		case "presume-input-tty":
			opts.presumeInputTTY = true
		}
	}

	return opts, nil
}

func parseRMInteractiveMode(inv *Invocation, value string, hasValue bool) (rmInteractiveMode, error) {
	if hasValue && value == "" {
		return rmInteractivePromptProtected, exitf(inv, 1, "rm: invalid argument %s for '--interactive'", quoteGNUOperand(value))
	}

	switch value {
	case "":
		return rmInteractiveAlways, nil
	case "always", "yes":
		return rmInteractiveAlways, nil
	case "once":
		return rmInteractiveOnce, nil
	case "never", "no", "none":
		return rmInteractiveNever, nil
	default:
		return rmInteractivePromptProtected, exitf(inv, 1, "rm: invalid argument %s for '--interactive'", quoteGNUOperand(value))
	}
}

func rmRemovePath(ctx context.Context, inv *Invocation, raw string, opts rmOptions) (rmResult, error) {
	display := rmDisplayPath(raw)
	info, abs, exists, err := lstatMaybe(ctx, inv, raw)
	if err != nil {
		if opts.force && errorsIsNotExist(err) {
			return rmResult{}, nil
		}
		if err := rmWriteCannotRemove(inv, display, rmRemovalErrorText(err, false)); err != nil {
			return rmResult{}, err
		}
		return rmResult{hadErr: true}, nil
	}
	if !exists {
		if opts.force {
			return rmResult{}, nil
		}
		if err := rmWriteCannotRemove(inv, display, "No such file or directory"); err != nil {
			return rmResult{}, err
		}
		return rmResult{hadErr: true}, nil
	}
	if info == nil {
		if err := rmWriteCannotRemove(inv, display, "No such file or directory"); err != nil {
			return rmResult{}, err
		}
		return rmResult{hadErr: true}, nil
	}

	if info.IsDir() {
		return rmRemoveDirectory(ctx, inv, display, abs, info, opts)
	}
	return rmRemoveFile(ctx, inv, display, abs, info, opts)
}

func rmRemoveDirectory(ctx context.Context, inv *Invocation, display, abs string, info stdfs.FileInfo, opts rmOptions) (rmResult, error) {
	if rmRefersToCurrentOrParent(display) {
		if err := rmWriteRefusal(inv, display); err != nil {
			return rmResult{}, err
		}
		return rmResult{hadErr: true}, nil
	}

	if opts.recursive {
		if rmIsPreserveRootViolation(ctx, inv, abs, opts.preserveRoot) {
			if err := rmWritePreserveRootError(inv, display); err != nil {
				return rmResult{}, err
			}
			return rmResult{hadErr: true}, nil
		}
		return rmRemoveDirectoryRecursive(ctx, inv, display, abs, info, opts)
	}

	if opts.dir {
		return rmRemoveEmptyDirectory(ctx, inv, display, abs, info, opts)
	}

	if err := rmWriteCannotRemove(inv, display, "Is a directory"); err != nil {
		return rmResult{}, err
	}
	return rmResult{hadErr: true}, nil
}

func rmRemoveDirectoryRecursive(ctx context.Context, inv *Invocation, display, abs string, info stdfs.FileInfo, opts rmOptions) (rmResult, error) {
	if opts.interactive == rmInteractiveAlways {
		entries, err := inv.FS.ReadDir(ctx, abs)
		if err == nil && len(entries) > 0 {
			ok, err := rmPromptYes(ctx, inv, fmt.Sprintf("descend into directory %s", quoteGNUOperand(display)), opts)
			if err != nil {
				return rmResult{}, err
			}
			if !ok {
				return rmResult{}, nil
			}
		}
	}

	entries, err := inv.FS.ReadDir(ctx, abs)
	if err != nil {
		if err := rmWriteCannotRemove(inv, display, rmRemovalErrorText(err, true)); err != nil {
			return rmResult{}, err
		}
		return rmResult{hadErr: true}, nil
	}

	result := rmResult{}
	hasUnremovedChild := false
	for _, entry := range entries {
		childDisplay := joinChildPath(display, entry.Name())
		childAbs := joinChildPath(abs, entry.Name())

		childInfo, infoErr := entry.Info()
		if infoErr != nil {
			childInfo, _, infoErr = lstatPath(ctx, inv, childAbs)
			if infoErr != nil {
				if err := rmWriteCannotRemove(inv, childDisplay, rmRemovalErrorText(infoErr, false)); err != nil {
					return rmResult{}, err
				}
				result.hadErr = true
				hasUnremovedChild = true
				continue
			}
		}

		childResult, err := rmRemoveExistingPath(ctx, inv, childDisplay, childAbs, childInfo, opts)
		if err != nil {
			return rmResult{}, err
		}
		result.hadErr = result.hadErr || childResult.hadErr
		hasUnremovedChild = hasUnremovedChild || !childResult.removed
	}

	if hasUnremovedChild {
		return result, nil
	}

	ok, err := rmPromptDirectory(ctx, inv, display, info, opts)
	if err != nil {
		return rmResult{}, err
	}
	if !ok {
		return result, nil
	}

	if err := inv.FS.Remove(ctx, abs, false); err != nil {
		if !result.hadErr {
			if err := rmWriteCannotRemove(inv, display, rmRemovalErrorText(err, true)); err != nil {
				return rmResult{}, err
			}
		}
		result.hadErr = true
		return result, nil
	}

	if err := rmWriteVerboseDirectory(inv, display, opts.verbose); err != nil {
		return rmResult{}, err
	}
	result.removed = true
	return result, nil
}

func rmRemoveExistingPath(ctx context.Context, inv *Invocation, display, abs string, info stdfs.FileInfo, opts rmOptions) (rmResult, error) {
	if info.IsDir() {
		return rmRemoveDirectory(ctx, inv, display, abs, info, opts)
	}
	return rmRemoveFile(ctx, inv, display, abs, info, opts)
}

func rmRemoveEmptyDirectory(ctx context.Context, inv *Invocation, display, abs string, info stdfs.FileInfo, opts rmOptions) (rmResult, error) {
	ok, err := rmPromptDirectory(ctx, inv, display, info, opts)
	if err != nil {
		return rmResult{}, err
	}
	if !ok {
		return rmResult{}, nil
	}

	if err := inv.FS.Remove(ctx, abs, false); err != nil {
		if err := rmWriteCannotRemove(inv, display, rmRemovalErrorText(err, true)); err != nil {
			return rmResult{}, err
		}
		return rmResult{hadErr: true}, nil
	}
	if err := rmWriteVerboseDirectory(inv, display, opts.verbose); err != nil {
		return rmResult{}, err
	}
	return rmResult{removed: true}, nil
}

func rmRemoveFile(ctx context.Context, inv *Invocation, display, abs string, info stdfs.FileInfo, opts rmOptions) (rmResult, error) {
	ok, err := rmPromptFile(ctx, inv, display, info, opts)
	if err != nil {
		return rmResult{}, err
	}
	if !ok {
		return rmResult{}, nil
	}

	if err := inv.FS.Remove(ctx, abs, false); err != nil {
		if err := rmWriteCannotRemove(inv, display, rmRemovalErrorText(err, false)); err != nil {
			return rmResult{}, err
		}
		return rmResult{hadErr: true}, nil
	}
	if err := rmWriteVerboseFile(inv, display, opts.verbose); err != nil {
		return rmResult{}, err
	}
	return rmResult{removed: true}, nil
}

func rmPromptFile(ctx context.Context, inv *Invocation, display string, info stdfs.FileInfo, opts rmOptions) (bool, error) {
	if opts.interactive == rmInteractiveNever {
		return true, nil
	}
	if opts.interactive == rmInteractiveOnce {
		return true, nil
	}

	if info.Mode()&stdfs.ModeSymlink != 0 {
		if opts.interactive == rmInteractiveAlways {
			return rmPromptYes(ctx, inv, fmt.Sprintf("remove symbolic link %s", quoteGNUOperand(display)), opts)
		}
		return true, nil
	}

	if opts.interactive == rmInteractiveAlways {
		if info.Size() == 0 {
			return rmPromptYes(ctx, inv, fmt.Sprintf("remove regular empty file %s", quoteGNUOperand(display)), opts)
		}
		return rmPromptYes(ctx, inv, fmt.Sprintf("remove file %s", quoteGNUOperand(display)), opts)
	}

	if opts.interactive == rmInteractivePromptProtected && !rmInputIsTTY(inv, opts) {
		return true, nil
	}
	if rmIsOwnerWritable(info.Mode()) {
		return true, nil
	}
	if info.Size() == 0 {
		return rmPromptYes(ctx, inv, fmt.Sprintf("remove write-protected regular empty file %s", quoteGNUOperand(display)), opts)
	}
	return rmPromptYes(ctx, inv, fmt.Sprintf("remove write-protected regular file %s", quoteGNUOperand(display)), opts)
}

func rmPromptDirectory(ctx context.Context, inv *Invocation, display string, info stdfs.FileInfo, opts rmOptions) (bool, error) {
	if opts.interactive == rmInteractiveNever {
		return true, nil
	}
	if opts.interactive == rmInteractiveOnce {
		return true, nil
	}

	readable := rmIsOwnerReadable(info.Mode())
	writable := rmIsOwnerWritable(info.Mode())
	if opts.interactive == rmInteractivePromptProtected && !rmInputIsTTY(inv, opts) {
		return true, nil
	}

	switch {
	case !readable && !writable:
		return rmPromptYes(ctx, inv, fmt.Sprintf("attempt removal of inaccessible directory %s", quoteGNUOperand(display)), opts)
	case !readable && opts.interactive == rmInteractiveAlways:
		return rmPromptYes(ctx, inv, fmt.Sprintf("attempt removal of inaccessible directory %s", quoteGNUOperand(display)), opts)
	case !writable:
		return rmPromptYes(ctx, inv, fmt.Sprintf("remove write-protected directory %s", quoteGNUOperand(display)), opts)
	case opts.interactive == rmInteractiveAlways:
		return rmPromptYes(ctx, inv, fmt.Sprintf("remove directory %s", quoteGNUOperand(display)), opts)
	default:
		return true, nil
	}
}

func rmPromptYes(ctx context.Context, inv *Invocation, prompt string, opts rmOptions) (bool, error) {
	reader, err := rmPromptReader(ctx, inv, opts)
	if err != nil {
		return false, err
	}

	if inv != nil && inv.Stderr != nil {
		suffix := " "
		if !strings.HasSuffix(prompt, "?") {
			suffix = "? "
		}
		if _, err := fmt.Fprintf(inv.Stderr, "rm: %s%s", prompt, suffix); err != nil {
			return false, &ExitError{Code: 1, Err: err}
		}
		if flusher, ok := inv.Stderr.(interface{ Flush() error }); ok {
			if err := flusher.Flush(); err != nil {
				return false, &ExitError{Code: 1, Err: err}
			}
		}
	}

	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, exitf(inv, 1, "rm: Failed to read from standard input")
	}
	if line == "" {
		return false, nil
	}

	switch line[0] {
	case 'y', 'Y':
		return true, nil
	default:
		return false, nil
	}
}

func rmPromptReader(ctx context.Context, inv *Invocation, opts rmOptions) (*bufio.Reader, error) {
	if opts.promptInput != nil && opts.promptInput.reader != nil {
		return opts.promptInput.reader, nil
	}

	if inv != nil && inv.Stdin != nil {
		reader := bufio.NewReader(inv.Stdin)
		if opts.promptInput != nil {
			opts.promptInput.reader = reader
		}
		return reader, nil
	}
	file, _, err := openRead(ctx, inv, "/dev/tty")
	if err == nil {
		reader := bufio.NewReader(file)
		if opts.promptInput != nil {
			opts.promptInput.reader = reader
			opts.promptInput.closer = file
		}
		return reader, nil
	}
	return nil, exitf(inv, 1, "rm: failed to open standard input")
}

func rmClosePromptInput(input *rmPromptInput) error {
	if input == nil || input.closer == nil {
		return nil
	}
	err := input.closer.Close()
	input.closer = nil
	input.reader = nil
	return err
}

func rmInputIsTTY(inv *Invocation, opts rmOptions) bool {
	if opts.presumeInputTTY {
		return true
	}
	_, ok := ttyTerminalPath(inv)
	return ok
}

func rmIsOwnerReadable(mode stdfs.FileMode) bool {
	return mode&0o400 != 0
}

func rmIsOwnerWritable(mode stdfs.FileMode) bool {
	return mode&0o200 != 0
}

func rmIsPreserveRootViolation(ctx context.Context, inv *Invocation, abs string, preserveRoot bool) bool {
	if !preserveRoot {
		return false
	}
	resolved, err := inv.FS.Realpath(ctx, abs)
	return err == nil && resolved == "/"
}

func rmWriteCannotRemove(inv *Invocation, name, reason string) error {
	if inv == nil || inv.Stderr == nil {
		return nil
	}
	_, err := fmt.Fprintf(inv.Stderr, "rm: cannot remove %s: %s\n", quoteGNUOperand(name), reason)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func rmWriteRefusal(inv *Invocation, name string) error {
	if inv == nil || inv.Stderr == nil {
		return nil
	}
	_, err := fmt.Fprintf(inv.Stderr, "rm: refusing to remove '.' or '..' directory: skipping %s\n", quoteGNUOperand(name))
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func rmWritePreserveRootError(inv *Invocation, name string) error {
	if inv == nil || inv.Stderr == nil {
		return nil
	}
	message := "rm: it is dangerous to operate recursively on '/'\n"
	if name != "/" {
		message = fmt.Sprintf("rm: it is dangerous to operate recursively on %s (same as '/')\n", quoteGNUOperand(name))
	}
	message += "rm: use --no-preserve-root to override this failsafe\n"
	if _, err := io.WriteString(inv.Stderr, message); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func rmWriteVerboseFile(inv *Invocation, name string, verbose bool) error {
	if !verbose || inv == nil || inv.Stdout == nil {
		return nil
	}
	if _, err := fmt.Fprintf(inv.Stdout, "removed %s\n", quoteGNUOperand(name)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func rmWriteVerboseDirectory(inv *Invocation, name string, verbose bool) error {
	if !verbose || inv == nil || inv.Stdout == nil {
		return nil
	}
	if _, err := fmt.Fprintf(inv.Stdout, "removed directory %s\n", quoteGNUOperand(name)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func rmRemovalErrorText(err error, directory bool) string {
	switch {
	case err == nil:
		return ""
	case errorsIsNotExist(err):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "permission denied"):
		return "Permission denied"
	case directory && (errors.Is(err, stdfs.ErrInvalid) || strings.Contains(lower, "directory not empty") || strings.Contains(lower, "not empty")):
		return "Directory not empty"
	case strings.Contains(lower, "device or resource busy"):
		return "Device or resource busy"
	case errorsIsDirectory(err):
		return "Is a directory"
	default:
		return err.Error()
	}
}

func rmDisplayPath(name string) string {
	if name == "" {
		return name
	}
	name = rmCleanTrailingSlashes(name)
	if rmRefersToCurrentOrParent(name) {
		return name
	}
	return path.Clean(name)
}

func rmCleanTrailingSlashes(name string) string {
	if len(name) <= 1 || !strings.HasSuffix(name, "/") {
		return name
	}
	end := len(name) - 1
	for end > 0 && name[end-1] == '/' {
		end--
	}
	return name[:end+1]
}

func rmRefersToCurrentOrParent(name string) bool {
	switch name {
	case ".", "./", "..", "../":
		return true
	}
	return strings.HasSuffix(name, "/.") ||
		strings.HasSuffix(name, "/./") ||
		strings.HasSuffix(name, "/..") ||
		strings.HasSuffix(name, "/../")
}

var _ Command = (*RM)(nil)
var _ SpecProvider = (*RM)(nil)
var _ ParsedRunner = (*RM)(nil)
