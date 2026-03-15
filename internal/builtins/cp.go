package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type CP struct{}

type cpDereferenceMode int

const (
	cpDerefDefault cpDereferenceMode = iota
	cpDerefCommandLine
	cpDerefAlways
	cpDerefNever
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
			{Name: "update", Long: "update", ValueName: "WHEN", Arity: OptionRequiredValue, Help: "control which existing files are replaced"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "explain what is being done"},
		},
		Args: []ArgSpec{
			{Name: "source", ValueName: "SOURCE", Repeatable: true, Help: "source paths followed by destination"},
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

func (c *CP) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts := parseCPMatches(matches)
	if err := validateCPOptions(inv, opts); err != nil {
		return err
	}
	args := matches.Positionals()
	sources, destArg, err := cpOperands(inv, opts, args)
	if err != nil {
		return err
	}
	multipleSources := len(sources) > 1

	for i, source := range sources {
		srcInfo, srcAbs, srcLinkInfo, err := resolveCPSource(ctx, inv, source, opts, i == 0)
		if err != nil {
			return exitf(inv, 1, "cp: cannot stat %q: No such file or directory", source)
		}

		destAbs, _, _, err := resolveCPDestination(ctx, inv, opts, source, destArg, multipleSources)
		if err != nil {
			return err
		}
		destInfo, _, destExists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, destAbs)
		if err != nil {
			return err
		}
		if opts.noClobber && destExists {
			continue
		}
		destInfo, destExists, err = prepareCPDestination(ctx, inv, source, srcAbs, srcInfo, srcLinkInfo != nil, destAbs, destInfo, destExists, opts)
		if err != nil {
			return err
		}
		if same, err := cpSameFile(ctx, inv, srcAbs, srcInfo, destAbs, destInfo, destExists); err != nil {
			return err
		} else if same {
			return exitf(inv, 1, "cp: %s and %s are the same file", quoteGNUOperand(source), quoteGNUOperand(path.Base(destAbs)))
		}

		if srcLinkInfo != nil {
			if err := copySymlink(ctx, inv, srcAbs, destAbs, opts); err != nil {
				return err
			}
			continue
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
	hardLink          bool
	symbolicLink      bool
}

func parseCPMatches(matches *ParsedCommand) cpOptions {
	opts := cpOptions{}
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
			opts.hardLink = true
		case "symbolic-link":
			opts.symbolicLink = true
		case "backup", "attributes-only", "debug", "reflink", "update":
			// Accepted for forward GNU compatibility; semantics will be filled in incrementally.
		}
	}
	return opts
}

func validateCPOptions(inv *Invocation, opts cpOptions) error {
	switch {
	case opts.hardLink:
		return exitf(inv, 1, "cp: --link is not yet supported")
	case opts.symbolicLink:
		return exitf(inv, 1, "cp: --symbolic-link is not yet supported")
	default:
		return nil
	}
}

func cpOperands(inv *Invocation, opts cpOptions, args []string) (sources []string, destArg string, err error) {
	if opts.targetDirectory != "" {
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

func resolveCPDestination(ctx context.Context, inv *Invocation, opts cpOptions, sourceArg, destArg string, multipleSources bool) (destAbs string, destInfo stdfs.FileInfo, destExists bool, err error) {
	if opts.noTargetDirectory {
		info, abs, exists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, destArg)
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
	destInfo, destAbs, destExists, err = lstatMaybe(ctx, inv, policy.FileActionLstat, destArg)
	if err != nil {
		return "", nil, false, err
	}
	destIsDir := false
	if destExists {
		if destInfo.IsDir() {
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
			return "", nil, false, exitf(inv, 1, "target %q is not a directory", destArg)
		}
		return path.Join(destAbs, cpSourceBase(sourceArg)), destInfo, true, nil
	}
	if destExists && destIsDir {
		return path.Join(destAbs, cpSourceBase(sourceArg)), destInfo, true, nil
	}
	if strings.HasSuffix(destArg, "/") {
		return "", nil, false, exitf(inv, 1, "target %q is not a directory", destArg)
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

func resolveCPSource(ctx context.Context, inv *Invocation, source string, opts cpOptions, commandLine bool) (info stdfs.FileInfo, abs string, linkInfo stdfs.FileInfo, err error) {
	linkInfo, abs, err = lstatPath(ctx, inv, source)
	if err != nil {
		return nil, "", nil, err
	}
	if linkInfo.Mode()&stdfs.ModeSymlink == 0 {
		return linkInfo, abs, nil, nil
	}
	deref := !opts.recursive
	if commandLine && strings.HasSuffix(source, "/") {
		deref = true
	}
	switch opts.dereference {
	case cpDerefAlways:
		deref = true
	case cpDerefNever:
		deref = false
	case cpDerefCommandLine:
		deref = commandLine
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

func cpSameFile(ctx context.Context, inv *Invocation, srcAbs string, srcInfo stdfs.FileInfo, destAbs string, destInfo stdfs.FileInfo, destExists bool) (bool, error) {
	if !destExists {
		return false, nil
	}
	if srcAbs == destAbs {
		return true, nil
	}
	if realSrc, err := inv.FS.Realpath(ctx, srcAbs); err == nil {
		if realDst, err := inv.FS.Realpath(ctx, destAbs); err == nil && realSrc == realDst {
			return true, nil
		}
	}
	if srcInfo != nil && destInfo != nil && testSameFile(srcInfo, destInfo) {
		return true, nil
	}
	return false, nil
}

func prepareCPDestination(ctx context.Context, inv *Invocation, source, srcAbs string, srcInfo stdfs.FileInfo, copyingSymlink bool, destAbs string, destInfo stdfs.FileInfo, destExists bool, opts cpOptions) (stdfs.FileInfo, bool, error) {
	if !destExists {
		return destInfo, false, nil
	}
	if destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0 {
		if opts.removeDestination {
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
					if opts.force {
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

func cpIsSymlinkLoop(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "too many link")
}

func copySymlink(ctx context.Context, inv *Invocation, srcAbs, dstAbs string, opts cpOptions) error {
	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	target, err := inv.FS.Readlink(ctx, srcAbs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if info, _, exists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, dstAbs); err != nil {
		return err
	} else if exists {
		if info.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", dstAbs)
		}
		if opts.removeDestination || opts.force {
			if err := inv.FS.Remove(ctx, dstAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}
	if err := inv.FS.Symlink(ctx, target, dstAbs); err != nil {
		if errors.Is(err, stdfs.ErrExist) && opts.force {
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

var _ Command = (*CP)(nil)
var _ SpecProvider = (*CP)(nil)
var _ ParsedRunner = (*CP)(nil)
