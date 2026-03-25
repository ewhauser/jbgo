package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"path/filepath"
)

type LN struct{}

type lnOptions struct {
	backupMode      backupMode
	backupSuffix    string
	symbolic        bool
	force           bool
	verbose         bool
	noDereference   bool
	logical         bool
	physical        bool
	noTargetDir     bool
	targetDirectory string
	relative        bool
}

func NewLN() *LN {
	return &LN{}
}

func (c *LN) Name() string {
	return "ln"
}

func (c *LN) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *LN) Spec() CommandSpec {
	return CommandSpec{
		Name:  "ln",
		About: "Make links between files.",
		Usage: "ln [OPTION]... [-T] TARGET LINK_NAME\n" +
			"       ln [OPTION]... TARGET\n" +
			"       ln [OPTION]... TARGET... DIRECTORY\n" +
			"       ln [OPTION]... -t DIRECTORY TARGET...",
		AfterHelp: "In the 1st form, create a link to TARGET with the name LINK_NAME.\n" +
			"  In the 2nd form, create a link to TARGET in the current directory.\n" +
			"  In the 3rd and 4th forms, create links to each TARGET in DIRECTORY.\n" +
			"  Create hard links by default, symbolic links with --symbolic.\n" +
			"  By default, each destination (name of new link) should not already exist.\n" +
			"  When creating hard links, each TARGET must exist. Symbolic links\n" +
			"  can hold arbitrary text; if later resolved, a relative link is\n" +
			"  interpreted in relation to its parent directory.",
		Options: []OptionSpec{
			{Name: "backup-short", Short: 'b', Help: "like --backup but does not accept an argument"},
			{Name: "backup", Long: "backup", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "CONTROL", Help: "make a backup of each existing destination file"},
			{Name: "force", Short: 'f', Long: "force", Help: "remove existing destination files"},
			{Name: "no-dereference", Short: 'n', Long: "no-dereference", Help: "treat LINK_NAME as a normal file if it is a\n                          symbolic link to a directory"},
			{Name: "logical", Short: 'L', Long: "logical", Help: "follow TARGETs that are symbolic links"},
			{Name: "physical", Short: 'P', Long: "physical", Help: "make hard links directly to symbolic links"},
			{Name: "symbolic", Short: 's', Long: "symbolic", Help: "make symbolic links instead of hard links"},
			{Name: "target-directory", Short: 't', Long: "target-directory", Arity: OptionRequiredValue, ValueName: "DIRECTORY", Help: "specify the DIRECTORY in which to create the links"},
			{Name: "no-target-directory", Short: 'T', Long: "no-target-directory", Help: "treat LINK_NAME as a normal file always"},
			{Name: "relative", Short: 'r', Long: "relative", Help: "create symbolic links relative to link location"},
			{Name: "suffix", Short: 'S', Long: "suffix", Arity: OptionRequiredValue, ValueName: "SUFFIX", Help: "override the usual backup suffix"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "print name of each linked file"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
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

func (c *LN) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts := lnOptions{
		symbolic:      matches.Has("symbolic"),
		force:         matches.Has("force"),
		verbose:       matches.Has("verbose"),
		noDereference: matches.Has("no-dereference"),
		logical:       matches.Has("logical"),
		physical:      matches.Has("physical"),
		noTargetDir:   matches.Has("no-target-directory"),
		relative:      matches.Has("relative"),
		backupSuffix:  determineBackupSuffix(inv, matches),
	}
	backupMode, err := determineBackupMode(inv, matches, "ln")
	if err != nil {
		return err
	}
	opts.backupMode = backupMode
	if matches.Has("target-directory") {
		opts.targetDirectory = matches.Value("target-directory")
	}
	if opts.relative && !opts.symbolic {
		return commandUsageError(inv, "ln", "--relative works only with symbolic links")
	}
	if opts.logical && opts.symbolic {
		opts.logical = false
	}
	if opts.physical {
		opts.logical = false
	}

	files := matches.Args("file")
	return runLN(ctx, inv, &opts, files)
}

func runLN(ctx context.Context, inv *Invocation, opts *lnOptions, files []string) error {
	if opts.targetDirectory != "" {
		if opts.noTargetDir {
			return commandUsageError(inv, "ln", "cannot combine --target-directory and --no-target-directory")
		}
		if len(files) == 0 {
			return commandUsageError(inv, "ln", "missing file operand")
		}
		return lnLinkIntoDirectory(ctx, inv, opts, files, opts.targetDirectory)
	}

	switch len(files) {
	case 0:
		return commandUsageError(inv, "ln", "missing file operand")
	case 1:
		if opts.noTargetDir {
			return commandUsageError(inv, "ln", "missing destination file operand after %s", quoteGNUOperand(files[0]))
		}
		return lnLinkIntoDirectory(ctx, inv, opts, files, ".")
	case 2:
		if opts.noTargetDir {
			if hasTrailingSlash(files[1]) {
				return exitf(inv, 1, "ln: failed to access %s: Not a directory", quoteGNUOperand(files[1]))
			}
		} else {
			statFn := statMaybe
			if opts.noDereference {
				statFn = lstatMaybe
			}
			if info, _, exists, err := statFn(ctx, inv, files[1]); err != nil {
				return err
			} else if exists && info.IsDir() {
				return lnLinkIntoDirectory(ctx, inv, opts, files[:1], files[1])
			}
		}
		return lnCreateOne(ctx, inv, opts, files[0], files[1])
	default:
		if opts.noTargetDir {
			return commandUsageError(inv, "ln", "extra operand %s", quoteGNUOperand(files[2]))
		}
		return lnLinkIntoDirectory(ctx, inv, opts, files[:len(files)-1], files[len(files)-1])
	}
}

func lnLinkIntoDirectory(ctx context.Context, inv *Invocation, opts *lnOptions, sources []string, dir string) error {
	info, dirAbs, err := statPath(ctx, inv, dir)
	if err != nil {
		return exitf(inv, 1, "ln: target %s is not a directory", quoteGNUOperand(dir))
	}
	if !info.IsDir() {
		return exitf(inv, 1, "ln: target %s is not a directory", quoteGNUOperand(dir))
	}

	hadErr := false
	for _, src := range sources {
		base := path.Base(src)
		if base == "." || base == ".." || base == "/" || base == "" {
			base = src
		}
		dest := path.Join(dirAbs, base)
		if err := lnCreateOne(ctx, inv, opts, src, dest); err != nil {
			if exitCode, ok := ExitCode(err); ok && exitCode != 0 {
				hadErr = true
				continue
			}
			return err
		}
	}
	if hadErr {
		return &ExitError{Code: 1}
	}
	return nil
}

func lnCreateOne(ctx context.Context, inv *Invocation, opts *lnOptions, target, linkName string) error {
	linkAbs := allowPath(inv, linkName)
	if err := ensureParentDirExists(ctx, inv, linkAbs); err != nil {
		return err
	}

	if err := lnPrepareDestination(ctx, inv, opts, target, linkName, linkAbs); err != nil {
		return err
	}

	linkTarget := target
	if opts.symbolic && opts.relative {
		linkTarget = lnRelativeTarget(inv, target, linkAbs)
	}

	if opts.symbolic {
		if err := inv.FS.Symlink(ctx, linkTarget, linkAbs); err != nil {
			return exitf(inv, 1, "ln: failed to create symbolic link %s: %s", quoteGNUOperand(linkName), lnErrText(err))
		}
	} else {
		sourceInfo, sourceAbs, err := lstatPath(ctx, inv, target)
		if err != nil {
			return exitf(inv, 1, "ln: failed to access %s: %s", quoteGNUOperand(target), lnErrText(err))
		}
		if sourceInfo.IsDir() {
			return exitf(inv, 1, "ln: %s: hard link not allowed for directory", quoteGNUOperand(target))
		}
		linkSource := sourceAbs
		if opts.logical {
			if resolved, err := inv.FS.Realpath(ctx, sourceAbs); err == nil {
				linkSource = resolved
			}
		}
		if err := inv.FS.Link(ctx, linkSource, linkAbs); err != nil {
			return exitf(inv, 1, "ln: failed to create hard link %s => %s: %s", quoteGNUOperand(target), quoteGNUOperand(linkName), lnErrText(err))
		}
	}

	if opts.verbose {
		if _, err := fmt.Fprintf(inv.Stdout, "%s -> %s\n", quoteGNUOperand(linkName), quoteGNUOperand(linkTarget)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func lnPrepareDestination(ctx context.Context, inv *Invocation, opts *lnOptions, target, linkName, linkAbs string) error {
	info, _, exists, err := lstatMaybe(ctx, inv, linkName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	replacing := opts.force || opts.backupMode != backupNone
	if !replacing {
		return exitf(inv, 1, "ln: failed to create link %s: File exists", quoteGNUOperand(linkName))
	}
	if err := lnCheckSameFile(ctx, inv, opts, target, linkName); err != nil {
		return err
	}

	if info.IsDir() && !info.Mode().Type().IsRegular() && !opts.noDereference {
		return exitf(inv, 1, "ln: failed to create link %s: File exists", quoteGNUOperand(linkName))
	}
	if opts.backupMode != backupNone {
		backupAbs, err := backupPath(ctx, inv, opts.backupMode, linkAbs, opts.backupSuffix)
		if err != nil {
			return err
		}
		if _, _, backupExists, err := lstatMaybe(ctx, inv, backupAbs); err != nil {
			return err
		} else if backupExists {
			if err := inv.FS.Remove(ctx, backupAbs, false); err != nil {
				return exitf(inv, 1, "ln: failed to remove %s: %s", quoteGNUOperand(backupAbs), lnErrText(err))
			}
		}
		if err := inv.FS.Rename(ctx, linkAbs, backupAbs); err != nil {
			return exitf(inv, 1, "ln: failed to create backup %s: %s", quoteGNUOperand(backupAbs), lnErrText(err))
		}
		return nil
	}
	if err := inv.FS.Remove(ctx, linkAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return exitf(inv, 1, "ln: failed to remove %s: %s", quoteGNUOperand(linkName), lnErrText(err))
	}
	return nil
}

func lnCheckSameFile(ctx context.Context, inv *Invocation, opts *lnOptions, target, linkName string) error {
	if allowPath(inv, target) == allowPath(inv, linkName) {
		return exitf(inv, 1, "ln: %s and %s are the same file", quoteGNUOperand(target), quoteGNUOperand(linkName))
	}
	if opts.symbolic {
		return nil
	}
	if targetAbs, err := inv.FS.Realpath(ctx, target); err == nil {
		if destAbs, err := inv.FS.Realpath(ctx, linkName); err == nil && targetAbs == destAbs {
			return exitf(inv, 1, "ln: %s and %s are the same file", quoteGNUOperand(target), quoteGNUOperand(linkName))
		}
	}
	return nil
}

func lnRelativeTarget(inv *Invocation, target, linkAbs string) string {
	targetAbs := inv.FS.Resolve(target)
	baseDir := path.Dir(linkAbs)
	if rel, err := filepath.Rel(baseDir, targetAbs); err == nil {
		return filepath.ToSlash(rel)
	}
	return target
}

func lnErrText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrExist):
		return "File exists"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, stdfs.ErrInvalid):
		return "Invalid argument"
	default:
		return err.Error()
	}
}

var _ Command = (*LN)(nil)
var _ SpecProvider = (*LN)(nil)
var _ ParsedRunner = (*LN)(nil)
