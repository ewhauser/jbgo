package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"strings"
)

type CP struct{}

type cpDereferenceMode int

const (
	cpDerefDefault cpDereferenceMode = iota
	cpDerefCommandLine
	cpDerefAlways
	cpDerefNever
)

type cpCopyMode int

const (
	cpCopyFile cpCopyMode = iota
	cpCopyHardLink
	cpCopySymbolicLink
)

type cpUpdateMode int

const (
	cpUpdateAll cpUpdateMode = iota
	cpUpdateOlder
	cpUpdateNone
	cpUpdateNoneFail
	cpUpdateInvalid
)

func NewCP() *CP {
	return &CP{}
}

func (c *CP) Name() string {
	return "cp"
}

func (c *CP) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *CP) Spec() CommandSpec {
	return CommandSpec{
		Name:  "cp",
		About: "Copy SOURCE to DEST, or multiple SOURCE(s) to DIRECTORY.",
		Usage: "cp [OPTION]... SOURCE... DEST",
		Options: []OptionSpec{
			{Name: "archive", Short: 'a', Long: "archive", Help: "same as -R -p"},
			{Name: "backup", Short: 'b', Long: "backup", Help: "make a backup of each existing destination file"},
			{Name: "attributes-only", Long: "attributes-only", Help: "don't copy the file data, just the attributes"},
			{Name: "debug", Long: "debug", Help: "explain how a file is copied"},
			{Name: "no-dereference", Short: 'd', Long: "no-dereference", Help: "copy symbolic links as symbolic links"},
			{Name: "force", Short: 'f', Long: "force", Help: "overwrite an existing destination file"},
			{Name: "dereference-command-line", Short: 'H', Help: "follow command line symbolic links in SOURCE"},
			{Name: "dereference", Short: 'L', Help: "always follow symbolic links in SOURCE"},
			{Name: "hard-link", Short: 'l', Long: "link", Help: "hard link files instead of copying"},
			{Name: "recursive", Short: 'r', ShortAliases: []rune{'R'}, Long: "recursive", Help: "copy directories recursively"},
			{Name: "no-clobber", Short: 'n', Long: "no-clobber", Help: "do not overwrite an existing file"},
			{Name: "physical", Short: 'P', Help: "never follow symbolic links in SOURCE"},
			{Name: "preserve", Short: 'p', Long: "preserve", Help: "preserve mode bits"},
			{Name: "reflink", Long: "reflink", ValueName: "WHEN", Arity: OptionRequiredValue, Help: "control clone/CoW copies"},
			{Name: "remove-destination", Long: "remove-destination", Help: "remove each existing destination file before opening it"},
			{Name: "symbolic-link", Short: 's', Long: "symbolic-link", Help: "make symbolic links instead of copying"},
			{Name: "no-target-directory", Short: 'T', Long: "no-target-directory", Help: "treat DEST as a normal file"},
			{Name: "target-directory", Short: 't', Long: "target-directory", ValueName: "DIRECTORY", Arity: OptionRequiredValue, Help: "copy all SOURCE arguments into DIRECTORY"},
			{Name: "update", Short: 'u', Long: "update", ValueName: "WHEN", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "control which existing files are replaced"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "explain what is being done"},
		},
		Args: []ArgSpec{
			{Name: "source", ValueName: "SOURCE", Repeatable: true, Help: "source paths followed by destination"},
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

func (c *CP) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts := parseCPMatches(matches)
	if err := validateCPOptions(inv, &opts); err != nil {
		return err
	}
	args := matches.Positionals()
	sources, destArg, err := cpOperands(inv, &opts, args)
	if err != nil {
		return err
	}
	multipleSources := len(sources) > 1

	for _, source := range sources {
		srcInfo, srcAbs, srcLinkInfo, err := resolveCPSource(ctx, inv, source, &opts, true)
		if err != nil {
			return exitf(inv, 1, "cp: cannot stat %q: No such file or directory", source)
		}

		destAbs, _, _, err := resolveCPDestination(ctx, inv, &opts, source, destArg, multipleSources)
		if err != nil {
			return err
		}
		destInfo, _, destExists, err := lstatMaybe(ctx, inv, destAbs)
		if err != nil {
			return err
		}
		if cpSameFile(ctx, inv, srcAbs, srcInfo, destAbs, destInfo, destExists) {
			if opts.copyMode == cpCopyHardLink {
				continue
			}
			return exitf(inv, 1, "cp: %s and %s are the same file", quoteGNUOperand(source), quoteGNUOperand(path.Base(destAbs)))
		}
		skip, err := cpShouldSkipExisting(ctx, inv, srcInfo, srcLinkInfo != nil, destAbs, destInfo, destExists, &opts)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		_, _, err = prepareCPDestination(ctx, inv, source, srcAbs, srcInfo, srcLinkInfo != nil, destAbs, destInfo, destExists, &opts)
		if err != nil {
			return err
		}

		if srcInfo.IsDir() {
			if !opts.recursive {
				return exitf(inv, 1, "cp: omitting directory %q", source)
			}
			if destAbs == srcAbs || strings.HasPrefix(destAbs, srcAbs+"/") {
				return exitf(inv, 1, "cp: cannot copy %q into itself", source)
			}
			if err := copyTree(ctx, inv, srcAbs, destAbs); err != nil {
				return err
			}
			continue
		}

		switch opts.copyMode {
		case cpCopySymbolicLink:
			if err := cpCreateSymbolicLink(ctx, inv, source, destAbs); err != nil {
				return err
			}
			if opts.verbose {
				if _, err := fmt.Fprintf(inv.Stdout, "'%s' -> '%s'\n", source, destAbs); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			continue
		case cpCopyHardLink:
			linkSource := srcAbs
			if srcLinkInfo == nil {
				if resolved, err := inv.FS.Realpath(ctx, srcAbs); err == nil {
					linkSource = resolved
				}
			}
			if err := cpCreateHardLink(ctx, inv, source, linkSource, destAbs); err != nil {
				return err
			}
			if opts.verbose {
				if _, err := fmt.Fprintf(inv.Stdout, "'%s' -> '%s'\n", source, destAbs); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			continue
		}

		if srcLinkInfo != nil {
			if err := copySymlink(ctx, inv, srcAbs, destAbs); err != nil {
				return err
			}
			if opts.verbose {
				if _, err := fmt.Fprintf(inv.Stdout, "'%s' -> '%s'\n", source, destAbs); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			continue
		}

		if err := copyFileContents(ctx, inv, srcAbs, destAbs, srcInfo.Mode().Perm()); err != nil {
			return err
		}

		if opts.verbose {
			if _, err := fmt.Fprintf(inv.Stdout, "'%s' -> '%s'\n", source, destAbs); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	return nil
}

type cpOptions struct {
	recursive         bool
	noClobber         bool
	preserve          bool
	verbose           bool
	force             bool
	dereference       cpDereferenceMode
	noTargetDirectory bool
	targetDirectory   string
	removeDestination bool
	copyMode          cpCopyMode
	updateMode        cpUpdateMode
	updateArg         string
}

func parseCPMatches(matches *ParsedCommand) cpOptions {
	opts := cpOptions{updateMode: cpUpdateAll}
	if matches == nil {
		return opts
	}
	for _, name := range matches.OptionOrder() {
		switch name {
		case "archive":
			opts.recursive = true
			opts.preserve = true
			opts.dereference = cpDerefNever
		case "recursive":
			opts.recursive = true
		case "no-clobber":
			opts.noClobber = true
		case "preserve":
			opts.preserve = true
		case "verbose":
			opts.verbose = true
		case "force":
			opts.force = true
		case "no-dereference", "physical":
			opts.dereference = cpDerefNever
		case "dereference-command-line":
			opts.dereference = cpDerefCommandLine
		case "dereference":
			opts.dereference = cpDerefAlways
		case "no-target-directory":
			opts.noTargetDirectory = true
		case "target-directory":
			opts.targetDirectory = matches.Value("target-directory")
		case "remove-destination":
			opts.removeDestination = true
		case "hard-link":
			opts.copyMode = cpCopyHardLink
		case "symbolic-link":
			opts.copyMode = cpCopySymbolicLink
		case "update":
			opts.updateArg = matches.Value("update")
			switch opts.updateArg {
			case "", "older":
				opts.updateMode = cpUpdateOlder
			case "all":
				opts.updateMode = cpUpdateAll
			case "none":
				opts.updateMode = cpUpdateNone
			case "none-fail":
				opts.updateMode = cpUpdateNoneFail
			default:
				opts.updateMode = cpUpdateInvalid
			}
		case "backup", "attributes-only", "debug", "reflink":
			// Accepted for forward GNU compatibility; semantics will be filled in incrementally.
		}
	}
	return opts
}

func validateCPOptions(inv *Invocation, opts *cpOptions) error {
	if opts == nil {
		return nil
	}
	if opts.updateMode == cpUpdateInvalid {
		return commandUsageError(inv, "cp", "invalid argument %q for '--update'", opts.updateArg)
	}
	return nil
}

func cpOperands(inv *Invocation, opts *cpOptions, args []string) (sources []string, destArg string, err error) {
	if opts != nil && opts.targetDirectory != "" {
		if opts.noTargetDirectory {
			return nil, "", exitf(inv, 1, "cp: cannot combine --target-directory and --no-target-directory")
		}
		if len(args) == 0 {
			return nil, "", exitf(inv, 1, "cp: missing file operand")
		}
		return args, opts.targetDirectory, nil
	}
	if len(args) < 2 {
		return nil, "", exitf(inv, 1, "cp: missing destination file operand")
	}
	return args[:len(args)-1], args[len(args)-1], nil
}

func resolveCPDestination(ctx context.Context, inv *Invocation, opts *cpOptions, sourceArg, destArg string, multipleSources bool) (destAbs string, destInfo stdfs.FileInfo, destExists bool, err error) {
	if opts != nil && opts.noTargetDirectory {
		info, abs, exists, err := lstatMaybe(ctx, inv, destArg)
		if err != nil {
			return "", nil, false, err
		}
		if multipleSources {
			return "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		if strings.HasSuffix(destArg, "/") {
			return "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		return abs, info, exists, nil
	}
	destInfo, destAbs, destExists, err = lstatMaybe(ctx, inv, destArg)
	if err != nil {
		return "", nil, false, err
	}
	destIsDir := false
	if destExists {
		if destInfo.IsDir() { //nolint:nilaway // destInfo is non-nil when destExists is true
			destIsDir = true
		} else if destInfo.Mode()&stdfs.ModeSymlink != 0 {
			if resolvedInfo, _, statErr := statPath(ctx, inv, destArg); statErr == nil {
				destIsDir = resolvedInfo.IsDir()
			} else if errors.Is(statErr, stdfs.ErrPermission) {
				return "", nil, false, exitf(inv, 1, "cp: target directory %s: Permission denied", quoteGNUOperand(destArg))
			}
		}
	}
	if multipleSources {
		if !destExists || !destIsDir {
			return "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		return path.Join(destAbs, cpSourceBase(sourceArg)), destInfo, true, nil
	}
	if destExists && destIsDir {
		return path.Join(destAbs, cpSourceBase(sourceArg)), destInfo, true, nil
	}
	if strings.HasSuffix(destArg, "/") {
		return "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
	}
	return destAbs, destInfo, destExists, nil
}

func cpSourceBase(source string) string {
	trimmed := strings.TrimRight(source, "/")
	if trimmed == "" {
		return path.Base(source)
	}
	return path.Base(trimmed)
}

func resolveCPSource(ctx context.Context, inv *Invocation, source string, opts *cpOptions, commandLine bool) (info stdfs.FileInfo, abs string, linkInfo stdfs.FileInfo, err error) {
	linkInfo, abs, err = lstatPath(ctx, inv, source)
	if err != nil {
		return nil, "", nil, err
	}
	if linkInfo.Mode()&stdfs.ModeSymlink == 0 {
		return linkInfo, abs, nil, nil
	}
	deref := true
	if opts != nil {
		deref = !opts.recursive
	}
	if commandLine && strings.HasSuffix(source, "/") {
		deref = true
	}
	if opts != nil {
		switch opts.dereference {
		case cpDerefAlways:
			deref = true
		case cpDerefNever:
			deref = false
		case cpDerefCommandLine:
			deref = commandLine
		}
	}
	if !deref {
		return linkInfo, abs, linkInfo, nil
	}
	info, abs, err = statPath(ctx, inv, source)
	if err != nil {
		return nil, "", nil, err
	}
	return info, abs, nil, nil
}

func cpSameFile(ctx context.Context, inv *Invocation, srcAbs string, srcInfo stdfs.FileInfo, destAbs string, destInfo stdfs.FileInfo, destExists bool) bool {
	if !destExists {
		return false
	}
	if srcAbs == destAbs {
		return true
	}
	if realSrc, err := inv.FS.Realpath(ctx, srcAbs); err == nil {
		if realDst, err := inv.FS.Realpath(ctx, destAbs); err == nil && realSrc == realDst {
			return true
		}
	}
	if srcInfo != nil && destInfo != nil && testSameFile(srcInfo, destInfo) {
		return true
	}
	return false
}

func prepareCPDestination(ctx context.Context, inv *Invocation, source, srcAbs string, srcInfo stdfs.FileInfo, copyingSymlink bool, destAbs string, destInfo stdfs.FileInfo, destExists bool, opts *cpOptions) (stdfs.FileInfo, bool, error) {
	if !destExists {
		return destInfo, false, nil
	}
	if destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0 {
		if opts != nil && opts.removeDestination {
			if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
				return nil, false, &ExitError{Code: 1, Err: err}
			}
			return nil, false, nil
		}
		if !copyingSymlink {
			if _, _, err := statPath(ctx, inv, destAbs); err != nil {
				switch {
				case errors.Is(err, stdfs.ErrPermission):
					return nil, true, exitf(inv, 1, "cp: cannot stat %s: Permission denied", quoteGNUOperand(path.Base(destAbs)))
				case cpIsSymlinkLoop(err):
					if opts != nil && opts.force {
						if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
							return nil, false, &ExitError{Code: 1, Err: err}
						}
						return nil, false, nil
					}
				case cpPosixlyCorrect(inv):
					return destInfo, true, nil
				}
				return nil, true, exitf(inv, 1, "cp: not writing through dangling symlink %s", quoteGNUOperand(path.Base(destAbs)))
			}
		}
	}
	_ = source
	_ = srcAbs
	_ = srcInfo
	return destInfo, true, nil
}

func cpPosixlyCorrect(inv *Invocation) bool {
	if inv == nil || inv.Env == nil {
		return false
	}
	_, ok := inv.Env["POSIXLY_CORRECT"]
	return ok
}

func cpShouldSkipExisting(ctx context.Context, inv *Invocation, srcInfo stdfs.FileInfo, copyingSymlink bool, destAbs string, destInfo stdfs.FileInfo, destExists bool, opts *cpOptions) (bool, error) {
	if !destExists {
		return false, nil
	}
	if opts == nil {
		return false, nil
	}
	if opts.noClobber {
		return true, nil
	}
	switch opts.updateMode {
	case cpUpdateAll:
		return false, nil
	case cpUpdateNone:
		return true, nil
	case cpUpdateNoneFail:
		return false, exitf(inv, 1, "cp: not replacing %s", quoteGNUOperand(destAbs))
	case cpUpdateOlder:
		effectiveDestInfo := destInfo
		if !copyingSymlink && destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0 {
			if resolvedInfo, _, err := statPath(ctx, inv, destAbs); err == nil {
				effectiveDestInfo = resolvedInfo
			}
		}
		if srcInfo == nil || effectiveDestInfo == nil {
			return false, nil
		}
		return !srcInfo.ModTime().After(effectiveDestInfo.ModTime()), nil
	default:
		return false, nil
	}
}

func cpIsSymlinkLoop(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "too many link")
}

func copySymlink(ctx context.Context, inv *Invocation, srcAbs, dstAbs string) error {
	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	target, err := inv.FS.Readlink(ctx, srcAbs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if info, _, exists, err := lstatMaybe(ctx, inv, dstAbs); err != nil {
		return err
	} else if exists {
		if info.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", dstAbs)
		}
		if err := inv.FS.Remove(ctx, dstAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := inv.FS.Symlink(ctx, target, dstAbs); err != nil {
		if errors.Is(err, stdfs.ErrExist) {
			if rmErr := inv.FS.Remove(ctx, dstAbs, true); rmErr == nil {
				err = inv.FS.Symlink(ctx, target, dstAbs)
			}
		}
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	recordFileMutation(inv.TraceRecorder(), "copy", dstAbs, srcAbs, dstAbs)
	return nil
}

func cpCreateSymbolicLink(ctx context.Context, inv *Invocation, target, dstAbs string) error {
	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	linkTarget := target
	if !path.IsAbs(target) {
		linkTarget = lnRelativeTarget(inv, target, dstAbs)
	}
	if info, _, exists, err := lstatMaybe(ctx, inv, dstAbs); err != nil {
		return err
	} else if exists {
		if info.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", dstAbs)
		}
		if err := inv.FS.Remove(ctx, dstAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := inv.FS.Symlink(ctx, linkTarget, dstAbs); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.TraceRecorder(), "copy", dstAbs, linkTarget, dstAbs)
	return nil
}

func cpCreateHardLink(ctx context.Context, inv *Invocation, source, srcAbs, dstAbs string) error {
	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	if info, _, exists, err := lstatMaybe(ctx, inv, dstAbs); err != nil {
		return err
	} else if exists {
		if info.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", dstAbs)
		}
		if err := inv.FS.Remove(ctx, dstAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := inv.FS.Link(ctx, srcAbs, dstAbs); err != nil {
		return exitf(inv, 1, "cp: cannot create hard link %s to %s: %s", quoteGNUOperand(path.Base(dstAbs)), quoteGNUOperand(source), lnErrText(err))
	}
	recordFileMutation(inv.TraceRecorder(), "copy", dstAbs, srcAbs, dstAbs)
	return nil
}

var _ Command = (*CP)(nil)
var _ SpecProvider = (*CP)(nil)
var _ ParsedRunner = (*CP)(nil)
