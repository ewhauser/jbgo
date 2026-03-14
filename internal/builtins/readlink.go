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

	gbfs "github.com/ewhauser/gbash/fs"
)

type Readlink struct{}

const readlinkMaxSymlinkDepth = 40

type readlinkOptions struct {
	canonicalize         bool
	canonicalizeExisting bool
	canonicalizeMissing  bool
	noNewline            bool
	verbose              bool
	zero                 bool
}

type readlinkCanonicalMode int

const (
	readlinkModeNone readlinkCanonicalMode = iota
	readlinkModeCanonicalize
	readlinkModeCanonicalizeExisting
	readlinkModeCanonicalizeMissing
)

func NewReadlink() *Readlink {
	return &Readlink{}
}

func (c *Readlink) Name() string {
	return "readlink"
}

func (c *Readlink) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Readlink) Spec() CommandSpec {
	return CommandSpec{
		Name:  "readlink",
		About: "Print value of a symbolic link or canonical file name",
		Usage: "readlink [OPTION]... FILE...",
		Options: []OptionSpec{
			{Name: "canonicalize", Short: 'f', Long: "canonicalize", Help: "canonicalize by following every symlink in every component of the given name recursively; all but the last component must exist"},
			{Name: "canonicalize-existing", Short: 'e', Long: "canonicalize-existing", Help: "canonicalize by following every symlink in every component of the given name recursively, all components must exist"},
			{Name: "canonicalize-missing", Short: 'm', Long: "canonicalize-missing", Help: "canonicalize by following every symlink in every component of the given name recursively, without requirements on components existence"},
			{Name: "no-newline", Short: 'n', Long: "no-newline", Help: "do not output the trailing delimiter"},
			{Name: "quiet", Short: 'q', Long: "quiet", Help: "suppress most error messages", Overrides: []string{"quiet", "silent", "verbose"}},
			{Name: "silent", Short: 's', Long: "silent", Help: "suppress most error messages", Overrides: []string{"quiet", "silent", "verbose"}},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "report error messages", Overrides: []string{"quiet", "silent", "verbose"}},
			{Name: "zero", Short: 'z', Long: "zero", Help: "end each output line with NUL, not newline"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			StopAtFirstPositional: true,
		},
		VersionRenderer: func(w io.Writer, _ CommandSpec) error {
			return RenderSimpleVersion(w, "readlink")
		},
	}
}

func (c *Readlink) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &matches.Spec)
	}
	if matches.Has("version") {
		return RenderCommandVersion(inv.Stdout, &matches.Spec)
	}

	opts := parseReadlinkMatches(matches)
	args := matches.Args("file")
	if _, ok := inv.Env["POSIXLY_CORRECT"]; ok {
		opts.verbose = true
	}

	lineEnding := "\n"
	if opts.noNewline && len(args) == 1 {
		lineEnding = ""
	} else if opts.zero {
		lineEnding = "\x00"
	}

	mode := resolveReadlinkCanonicalMode(opts)
	for _, name := range args {
		var (
			out string
			err error
		)
		if mode == readlinkModeNone {
			out, err = readlinkValue(ctx, inv, name)
		} else {
			out, err = canonicalizeReadlink(ctx, inv, name, mode)
		}
		if err != nil {
			return readlinkCommandError(inv, name, err, opts.verbose)
		}
		if _, err := fmt.Fprint(inv.Stdout, out); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if lineEnding != "" {
			if _, err := fmt.Fprint(inv.Stdout, lineEnding); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}
	return nil
}

func parseReadlinkMatches(matches *ParsedCommand) readlinkOptions {
	opts := readlinkOptions{}
	for _, name := range matches.OptionOrder() {
		switch name {
		case "canonicalize":
			opts.canonicalize = true
		case "canonicalize-existing":
			opts.canonicalizeExisting = true
		case "canonicalize-missing":
			opts.canonicalizeMissing = true
		case "no-newline":
			opts.noNewline = true
		case "quiet", "silent":
			opts.verbose = false
		case "verbose":
			opts.verbose = true
		case "zero":
			opts.zero = true
		}
	}
	return opts
}

func resolveReadlinkCanonicalMode(opts readlinkOptions) readlinkCanonicalMode {
	switch {
	case opts.canonicalizeExisting:
		return readlinkModeCanonicalizeExisting
	case opts.canonicalizeMissing:
		return readlinkModeCanonicalizeMissing
	case opts.canonicalize:
		return readlinkModeCanonicalize
	default:
		return readlinkModeNone
	}
}

func readlinkValue(ctx context.Context, inv *Invocation, name string) (string, error) {
	return inv.FS.Readlink(ctx, inv.FS.Resolve(name))
}

func canonicalizeReadlink(ctx context.Context, inv *Invocation, name string, mode readlinkCanonicalMode) (string, error) {
	absolute, pending := splitReadlinkPath(name)
	current := []string(nil)
	if !absolute {
		cwd := inv.FS.Getwd()
		if physicalCwd, err := inv.FS.Realpath(ctx, "."); err == nil {
			cwd = physicalCwd
		} else if mode == readlinkModeCanonicalizeExisting {
			return "", err
		}
		current = splitReadlinkSegments(cwd)
	}
	trailingSlash := strings.HasSuffix(name, "/")
	depth := 0

	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]

		switch part {
		case "", ".":
			continue
		case "..":
			if len(current) > 0 {
				current = current[:len(current)-1]
			}
			continue
		}

		next := appendPathSegment(current, part)
		info, err := inv.FS.Lstat(ctx, next)
		if err != nil {
			if !errors.Is(err, stdfs.ErrNotExist) {
				return "", err
			}
			switch mode {
			case readlinkModeCanonicalizeExisting:
				return "", err
			case readlinkModeCanonicalize:
				if len(pending) > 0 {
					return "", err
				}
				current = append(current, part)
				return finalizeReadlinkPath(ctx, inv, current, trailingSlash, mode)
			case readlinkModeCanonicalizeMissing:
				current = applyReadlinkLexical(append(current, part), pending)
				return finalizeReadlinkPath(ctx, inv, current, trailingSlash, mode)
			default:
				return "", err
			}
		}

		if info.Mode()&stdfs.ModeSymlink != 0 {
			target, err := inv.FS.Readlink(ctx, next)
			if err != nil {
				return "", err
			}
			depth++
			if depth > readlinkMaxSymlinkDepth {
				return "", syscall.ELOOP
			}
			targetAbsolute, targetParts := splitReadlinkPath(target)
			if targetAbsolute {
				current = nil
			}
			pending = append(targetParts, pending...)
			continue
		}

		current = append(current, part)
		if len(pending) > 0 && !info.IsDir() {
			if mode == readlinkModeCanonicalizeMissing {
				current = applyReadlinkLexical(current, pending)
				return finalizeReadlinkPath(ctx, inv, current, trailingSlash, mode)
			}
			return "", syscall.ENOTDIR
		}
	}

	return finalizeReadlinkPath(ctx, inv, current, trailingSlash, mode)
}

func finalizeReadlinkPath(ctx context.Context, inv *Invocation, current []string, trailingSlash bool, mode readlinkCanonicalMode) (string, error) {
	resolved := pathFromReadlinkSegments(current)
	if !trailingSlash || mode == readlinkModeCanonicalizeMissing {
		return resolved, nil
	}

	info, err := inv.FS.Stat(ctx, resolved)
	if err != nil {
		if mode == readlinkModeCanonicalize && errors.Is(err, stdfs.ErrNotExist) {
			return resolved, nil
		}
		return "", err
	}
	if !info.IsDir() {
		return "", syscall.ENOTDIR
	}
	return resolved, nil
}

func splitReadlinkPath(name string) (absolute bool, parts []string) {
	absolute = strings.HasPrefix(name, "/")
	trimmed := strings.TrimPrefix(name, "/")
	if trimmed == "" {
		return absolute, nil
	}
	parts = strings.Split(trimmed, "/")
	filtered := parts[:0]
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		filtered = append(filtered, part)
	}
	return absolute, filtered
}

func splitReadlinkSegments(abs string) []string {
	_, parts := splitReadlinkPath(gbfs.Clean(abs))
	return append([]string(nil), parts...)
}

func appendPathSegment(parts []string, part string) string {
	if len(parts) == 0 {
		return "/" + part
	}
	return "/" + strings.Join(append(parts, part), "/")
}

func applyReadlinkLexical(current, pending []string) []string {
	out := append([]string(nil), current...)
	for _, part := range pending {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, part)
		}
	}
	return out
}

func pathFromReadlinkSegments(parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

func readlinkCommandError(inv *Invocation, name string, err error, verbose bool) error {
	if !verbose {
		return &ExitError{Code: 1}
	}
	return exitf(inv, 1, "readlink: %s: %s", name, readlinkErrorText(err))
}

func readlinkErrorText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return readlinkErrorText(pathErr.Err)
	}

	switch {
	case errors.Is(err, stdfs.ErrInvalid), errors.Is(err, syscall.EINVAL):
		return "Invalid argument"
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.EISDIR):
		return "Not a directory"
	case errors.Is(err, syscall.ELOOP):
		return "Too many levels of symbolic links"
	default:
		return err.Error()
	}
}

var _ Command = (*Readlink)(nil)
