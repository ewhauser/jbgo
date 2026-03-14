package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"slices"
	"strings"
	"syscall"
)

type Realpath struct{}

type realpathResolveMode int

const (
	realpathResolvePhysical realpathResolveMode = iota
	realpathResolveLogical
	realpathResolveNone
)

type realpathMissingHandling int

const (
	realpathMissingNormal realpathMissingHandling = iota
	realpathMissingExisting
	realpathMissingMissing
)

type realpathOptions struct {
	quiet           bool
	lineEnding      string
	resolveMode     realpathResolveMode
	missingHandling realpathMissingHandling
	relativeTo      string
	relativeBase    string
	files           []string
}

func NewRealpath() *Realpath {
	return &Realpath{}
}

func (c *Realpath) Name() string {
	return "realpath"
}

func (c *Realpath) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Realpath) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print the resolved path.",
		Usage: "realpath [OPTION]... FILE...",
		Options: []OptionSpec{
			{
				Name:  "quiet",
				Short: 'q',
				Long:  "quiet",
				Help:  "do not print warnings for invalid paths",
			},
			{
				Name:    "strip",
				Short:   's',
				Long:    "strip",
				Aliases: []string{"no-symlinks"},
				Help:    "only strip '.' and '..' components; aliases: --no-symlinks",
			},
			{
				Name:  "zero",
				Short: 'z',
				Long:  "zero",
				Help:  "separate output filenames with NUL rather than newline",
			},
			{
				Name:  "logical",
				Short: 'L',
				Long:  "logical",
				Help:  "resolve '..' components before symlinks",
			},
			{
				Name:  "physical",
				Short: 'P',
				Long:  "physical",
				Help:  "resolve symlinks as encountered (default)",
			},
			{
				Name:  "canonicalize",
				Short: 'E',
				Long:  "canonicalize",
				Help:  "all but the last component must exist (default)",
			},
			{
				Name:  "canonicalize-existing",
				Short: 'e',
				Long:  "canonicalize-existing",
				Help:  "all components must exist",
			},
			{
				Name:  "canonicalize-missing",
				Short: 'm',
				Long:  "canonicalize-missing",
				Help:  "no path components are required to exist",
			},
			{
				Name:      "relative-to",
				Long:      "relative-to",
				ValueName: "DIR",
				Arity:     OptionRequiredValue,
				Help:      "print the resolved path relative to DIR",
			},
			{
				Name:      "relative-base",
				Long:      "relative-base",
				ValueName: "DIR",
				Arity:     OptionRequiredValue,
				Help:      "print absolute paths unless paths are below DIR",
			},
			{
				Name:  "version",
				Short: 'V',
				Long:  "version",
				Help:  "output version information and exit",
			},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
		},
	}
}

func (c *Realpath) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}

	opts, err := realpathOptionsFromMatches(ctx, inv, matches)
	if err != nil {
		return err
	}

	hadError := false
	for _, name := range opts.files {
		resolved, err := realpathCanonicalize(ctx, inv, name, opts.missingHandling, opts.resolveMode)
		if err != nil {
			hadError = true
			if err := realpathWriteError(inv, opts.quiet, name, err); err != nil {
				return err
			}
			continue
		}
		resolved = realpathProcessRelative(resolved, opts.relativeBase, opts.relativeTo)
		if _, err := fmt.Fprint(inv.Stdout, resolved, opts.lineEnding); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if hadError {
		return &ExitError{Code: 1}
	}
	return nil
}

func realpathOptionsFromMatches(ctx context.Context, inv *Invocation, matches *ParsedCommand) (realpathOptions, error) {
	opts := realpathOptions{
		quiet:           matches.Has("quiet"),
		lineEnding:      "\n",
		resolveMode:     realpathResolvePhysical,
		missingHandling: realpathMissingNormal,
		files:           matches.Args("file"),
	}
	if matches.Has("zero") {
		opts.lineEnding = "\x00"
	}
	if err := realpathRejectEmptyOperands(inv, opts.files, matches); err != nil {
		return realpathOptions{}, err
	}

	for _, name := range matches.OptionOrder() {
		switch name {
		case "strip":
			opts.resolveMode = realpathResolveNone
		case "logical":
			opts.resolveMode = realpathResolveLogical
		case "physical":
			opts.resolveMode = realpathResolvePhysical
		case "canonicalize":
			opts.missingHandling = realpathMissingNormal
		case "canonicalize-existing":
			opts.missingHandling = realpathMissingExisting
		case "canonicalize-missing":
			opts.missingHandling = realpathMissingMissing
		}
	}

	var err error
	opts.relativeTo, opts.relativeBase, err = realpathPrepareRelativeOptions(
		ctx,
		inv,
		matches,
		opts.missingHandling,
		opts.resolveMode,
	)
	if err != nil {
		return realpathOptions{}, err
	}

	return opts, nil
}

func realpathRejectEmptyOperands(inv *Invocation, files []string, matches *ParsedCommand) error {
	if matches.Has("relative-to") && matches.Value("relative-to") == "" {
		return exitf(inv, 1, "realpath: invalid operand: empty string")
	}
	if matches.Has("relative-base") && matches.Value("relative-base") == "" {
		return exitf(inv, 1, "realpath: invalid operand: empty string")
	}
	if slices.Contains(files, "") {
		return exitf(inv, 1, "realpath: invalid operand: empty string")
	}
	return nil
}

func realpathPrepareRelativeOptions(
	ctx context.Context,
	inv *Invocation,
	matches *ParsedCommand,
	missingHandling realpathMissingHandling,
	resolveMode realpathResolveMode,
) (relativeTo, relativeBase string, err error) {
	if matches.Has("relative-to") {
		raw := matches.Value("relative-to")
		relativeTo, err = realpathCanonicalizeRelativeOption(ctx, inv, raw, missingHandling, resolveMode)
		if err != nil {
			return "", "", exitf(inv, 1, "realpath: %s: %s", raw, readlinkErrorText(err))
		}
	}
	if matches.Has("relative-base") {
		raw := matches.Value("relative-base")
		relativeBase, err = realpathCanonicalizeRelativeOption(ctx, inv, raw, missingHandling, resolveMode)
		if err != nil {
			return "", "", exitf(inv, 1, "realpath: %s: %s", raw, readlinkErrorText(err))
		}
	}
	if relativeBase != "" && relativeTo != "" && !realpathHasPathPrefix(relativeTo, relativeBase) {
		return "", "", nil
	}
	return relativeTo, relativeBase, nil
}

func realpathCanonicalizeRelativeOption(
	ctx context.Context,
	inv *Invocation,
	name string,
	missingHandling realpathMissingHandling,
	resolveMode realpathResolveMode,
) (string, error) {
	resolved, err := realpathCanonicalize(ctx, inv, name, missingHandling, resolveMode)
	if err != nil {
		return "", err
	}
	if missingHandling != realpathMissingExisting {
		return resolved, nil
	}
	info, err := inv.FS.Stat(ctx, resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", syscall.ENOTDIR
	}
	return resolved, nil
}

func realpathCanonicalize(
	ctx context.Context,
	inv *Invocation,
	original string,
	missingHandling realpathMissingHandling,
	resolveMode realpathResolveMode,
) (string, error) {
	trailingSlash := strings.HasSuffix(original, "/")
	absoluteInput, err := realpathAbsoluteInput(ctx, inv, original)
	if err != nil {
		return "", err
	}
	if resolveMode == realpathResolveLogical {
		absoluteInput = gbashCleanPreserveRoot(absoluteInput)
	}
	if resolveMode == realpathResolveNone {
		resolved := realpathLexicalAbsolute(absoluteInput)
		return resolved, realpathPostCheck(ctx, inv, resolved, missingHandling, trailingSlash)
	}

	current := []string(nil)
	pending := realpathPathSegmentsRaw(absoluteInput)
	symlinksFollowed := 0

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
		default:
			current = append(current, part)
		}

		currentPath := realpathFromSegments(current)
		info, err := inv.FS.Lstat(ctx, currentPath)
		if err != nil {
			if missingHandling == realpathMissingExisting || (missingHandling == realpathMissingNormal && len(pending) > 0) {
				return "", err
			}
			continue
		}
		if info.Mode()&stdfs.ModeSymlink == 0 {
			continue
		}

		target, err := inv.FS.Readlink(ctx, currentPath)
		if err != nil {
			return "", err
		}
		symlinksFollowed++
		if symlinksFollowed > readlinkMaxSymlinkDepth {
			return "", syscall.ELOOP
		}

		current = current[:len(current)-1]
		targetAbsolute := path.IsAbs(target)
		if targetAbsolute {
			current = nil
		}
		pending = append(realpathPathSegmentsRaw(target), pending...)
	}

	resolved := realpathFromSegments(current)
	return resolved, realpathPostCheck(ctx, inv, resolved, missingHandling, trailingSlash)
}

func realpathAbsoluteInput(ctx context.Context, inv *Invocation, original string) (string, error) {
	if path.IsAbs(original) {
		return original, nil
	}
	cwd, err := inv.FS.Realpath(ctx, ".")
	if err != nil {
		return "", err
	}
	if cwd == "/" {
		return "/" + original, nil
	}
	return cwd + "/" + original, nil
}

func realpathPostCheck(
	ctx context.Context,
	inv *Invocation,
	resolved string,
	missingHandling realpathMissingHandling,
	trailingSlash bool,
) error {
	switch missingHandling {
	case realpathMissingExisting:
		if !trailingSlash {
			return nil
		}
		info, err := inv.FS.Stat(ctx, resolved)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return syscall.ENOTDIR
		}
	case realpathMissingNormal:
		info, err := inv.FS.Stat(ctx, resolved)
		switch {
		case err == nil:
			if trailingSlash && !info.IsDir() {
				return syscall.ENOTDIR
			}
		case !errors.Is(err, stdfs.ErrNotExist):
			return err
		default:
			parent := path.Dir(resolved)
			if parent == "." {
				parent = "/"
			}
			if _, err := inv.FS.ReadDir(ctx, parent); err != nil {
				return err
			}
		}
	case realpathMissingMissing:
		return nil
	}
	return nil
}

func realpathWriteError(inv *Invocation, quiet bool, name string, err error) error {
	if quiet {
		return nil
	}
	if inv == nil || inv.Stderr == nil {
		return &ExitError{Code: 1, Err: err}
	}
	if _, writeErr := fmt.Fprintf(inv.Stderr, "realpath: %s: %s\n", name, readlinkErrorText(err)); writeErr != nil {
		return &ExitError{Code: 1, Err: writeErr}
	}
	return nil
}

func realpathProcessRelative(resolved, relativeBase, relativeTo string) string {
	if relativeBase != "" {
		if realpathHasPathPrefix(resolved, relativeBase) {
			base := relativeBase
			if relativeTo != "" {
				base = relativeTo
			}
			return realpathMakeRelative(resolved, base)
		}
		return resolved
	}
	if relativeTo != "" {
		return realpathMakeRelative(resolved, relativeTo)
	}
	return resolved
}

func realpathMakeRelative(target, base string) string {
	targetParts := realpathPathSegmentsRaw(gbashCleanPreserveRoot(target))
	baseParts := realpathPathSegmentsRaw(gbashCleanPreserveRoot(base))

	common := 0
	for common < len(targetParts) && common < len(baseParts) && targetParts[common] == baseParts[common] {
		common++
	}

	parts := make([]string, 0, len(baseParts)-common+len(targetParts)-common)
	for i := common; i < len(baseParts); i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, targetParts[common:]...)
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

func realpathHasPathPrefix(candidate, prefix string) bool {
	candidateParts := realpathPathSegmentsRaw(gbashCleanPreserveRoot(candidate))
	prefixParts := realpathPathSegmentsRaw(gbashCleanPreserveRoot(prefix))
	if len(prefixParts) > len(candidateParts) {
		return false
	}
	for i, part := range prefixParts {
		if candidateParts[i] != part {
			return false
		}
	}
	return true
}

func realpathLexicalAbsolute(name string) string {
	return realpathFromSegments(realpathLexicalSegments(realpathPathSegmentsRaw(name)))
}

func realpathLexicalSegments(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
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

func realpathPathSegmentsRaw(name string) []string {
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return nil
	}
	parts := strings.Split(name, "/")
	filtered := parts[:0]
	for _, part := range parts {
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered
}

func realpathFromSegments(parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

func gbashCleanPreserveRoot(name string) string {
	if name == "" {
		return "/"
	}
	cleaned := path.Clean(name)
	if cleaned == "." {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		return "/" + cleaned
	}
	return cleaned
}

var _ Command = (*Realpath)(nil)
var _ SpecProvider = (*Realpath)(nil)
var _ ParsedRunner = (*Realpath)(nil)
