package builtins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Install struct{}

const (
	installDefaultMode         = stdfs.FileMode(0o755)
	installDefaultStripProgram = "strip"
	installSpecialModeBits     = stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky
	installAllModeBits         = stdfs.ModePerm | installSpecialModeBits
)

type installOptions struct {
	backupMode         backupMode
	backupSuffix       string
	mode               stdfs.FileMode
	modeSpecified      bool
	compare            bool
	directoryOnly      bool
	createLeading      bool
	ownerValue         string
	groupValue         string
	ownerID            *uint32
	groupID            *uint32
	preserveTimestamps bool
	strip              bool
	stripProgram       string
	targetDirectory    string
	targetDirectorySet bool
	noTargetDirectory  bool
	verbose            bool
	unprivileged       bool
	files              []string
}

func NewInstall() *Install {
	return &Install{}
}

func (c *Install) Name() string {
	return "install"
}

func (c *Install) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Install) Spec() CommandSpec {
	return CommandSpec{
		Name:      "install",
		About:     "Copy SOURCE to DEST while setting mode and ownership attributes.",
		Usage:     "install [OPTION]... SOURCE DEST\n  or:  install [OPTION]... SOURCE... DIRECTORY\n  or:  install [OPTION]... -t DIRECTORY SOURCE...\n  or:  install [OPTION]... -d DIRECTORY...",
		AfterHelp: "Backup CONTROL values:\n  none, off       never make backups\n  numbered, t     make numbered backups\n  existing, nil   numbered if numbered backups exist, simple otherwise\n  simple, never   always make simple backups",
		Options: []OptionSpec{
			{Name: "backup-short", Short: 'b', Help: "like --backup but does not accept an argument"},
			{Name: "backup", Long: "backup", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "CONTROL", Help: "make a backup of each existing destination file"},
			{Name: "ignored", Short: 'c', Help: "ignored"},
			{Name: "compare", Short: 'C', Long: "compare", Help: "compare source and destination and skip unchanged installs"},
			{Name: "directory", Short: 'd', Long: "directory", Help: "treat all arguments as directory names; create all components"},
			{Name: "create-leading", Short: 'D', Help: "create all leading components of DEST except the last"},
			{Name: "group", Short: 'g', Long: "group", Arity: OptionRequiredValue, ValueName: "GROUP", Help: "set group ownership instead of the process's current group"},
			{Name: "mode", Short: 'm', Long: "mode", Arity: OptionRequiredValue, ValueName: "MODE", Help: "set permission mode instead of 0755"},
			{Name: "owner", Short: 'o', Long: "owner", Arity: OptionRequiredValue, ValueName: "OWNER", Help: "set ownership instead of the process's current owner"},
			{Name: "preserve-timestamps", Short: 'p', Long: "preserve-timestamps", Help: "apply access/modification times of SOURCE files to destination files"},
			{Name: "strip", Short: 's', Long: "strip", Help: "strip symbol tables using a sandbox-visible strip program"},
			{Name: "strip-program", Long: "strip-program", Arity: OptionRequiredValue, ValueName: "PROGRAM", Help: "program used to strip binaries"},
			{Name: "suffix", Short: 'S', Long: "suffix", Arity: OptionRequiredValue, ValueName: "SUFFIX", Help: "override the usual backup suffix"},
			{Name: "target-directory", Short: 't', Long: "target-directory", Arity: OptionRequiredValue, ValueName: "DIRECTORY", Help: "copy all SOURCE arguments into DIRECTORY"},
			{Name: "no-target-directory", Short: 'T', Long: "no-target-directory", Help: "treat DEST as a normal file"},
			{Name: "unprivileged", Short: 'U', Long: "unprivileged", Help: "do not attempt to change ownership when installing"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "print the name of each directory as it is created"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true, Help: "source and destination paths"},
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

func (c *Install) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseInstallMatches(inv, matches)
	if err != nil {
		return err
	}

	db := loadPermissionIdentityDB(ctx, inv)
	if opts.ownerValue != "" {
		uid, err := parsePermissionUserForCommand(inv, db, opts.ownerValue, c.Name())
		if err != nil {
			return err
		}
		opts.ownerID = &uid
	}
	if opts.groupValue != "" {
		gid, err := parsePermissionGroupForCommand(inv, db, opts.groupValue, c.Name())
		if err != nil {
			return err
		}
		opts.groupID = &gid
	}

	if opts.compare && opts.modeSpecified && opts.mode&installSpecialModeBits != 0 {
		if _, err := fmt.Fprintln(inv.Stderr, "install: the --compare (-C) option is ignored when you specify a mode with non-permission bits"); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if opts.directoryOnly {
		return runInstallDirectories(ctx, inv, db, &opts)
	}
	return runInstallFiles(ctx, inv, db, &opts)
}

func parseInstallMatches(inv *Invocation, matches *ParsedCommand) (installOptions, error) {
	opts := installOptions{
		compare:            matches.Has("compare"),
		directoryOnly:      matches.Has("directory"),
		createLeading:      matches.Has("create-leading"),
		ownerValue:         strings.TrimSpace(matches.Value("owner")),
		groupValue:         strings.TrimSpace(matches.Value("group")),
		preserveTimestamps: matches.Has("preserve-timestamps"),
		strip:              matches.Has("strip"),
		stripProgram:       installDefaultStripProgram,
		targetDirectory:    matches.Value("target-directory"),
		targetDirectorySet: matches.Has("target-directory"),
		noTargetDirectory:  matches.Has("no-target-directory"),
		verbose:            matches.Has("verbose"),
		unprivileged:       matches.Has("unprivileged"),
		files:              matches.Positionals(),
	}
	if matches.Has("strip-program") {
		opts.stripProgram = matches.Value("strip-program")
	}
	if matches.Has("owner") && opts.ownerValue == "" {
		return installOptions{}, exitf(inv, 1, "install: invalid user %s", quoteGNUOperand(matches.Value("owner")))
	}
	if matches.Has("group") && opts.groupValue == "" {
		return installOptions{}, exitf(inv, 1, "install: invalid group %s", quoteGNUOperand(matches.Value("group")))
	}
	if opts.targetDirectorySet && opts.noTargetDirectory {
		return installOptions{}, exitf(inv, 1, "install: options --target-directory and --no-target-directory are mutually exclusive")
	}
	if opts.directoryOnly && opts.targetDirectorySet {
		return installOptions{}, exitf(inv, 1, "install: options --directory and --target-directory are mutually exclusive")
	}
	if opts.targetDirectorySet && opts.targetDirectory == "" {
		return installOptions{}, exitf(inv, 1, "install: failed to access %s: No such file or directory", quoteGNUOperand(opts.targetDirectory))
	}
	if opts.compare && opts.preserveTimestamps {
		return installOptions{}, exitf(inv, 1, "install: options --compare and --preserve-timestamps are mutually exclusive")
	}
	if opts.compare && opts.strip {
		return installOptions{}, exitf(inv, 1, "install: options --compare and --strip are mutually exclusive")
	}

	mode, modeSpecified, err := parseInstallMode(matches.Value("mode"), matches.Has("mode"), opts.directoryOnly)
	if err != nil {
		return installOptions{}, exitf(inv, 1, "install: invalid mode %s", quoteGNUOperand(matches.Value("mode")))
	}
	opts.mode = mode
	opts.modeSpecified = modeSpecified

	opts.backupSuffix = determineBackupSuffix(inv, matches)
	opts.backupMode, err = determineBackupMode(inv, matches, "install")
	if err != nil {
		return installOptions{}, err
	}

	return opts, nil
}

func parseInstallMode(spec string, set, directory bool) (stdfs.FileMode, bool, error) {
	if !set {
		return installDefaultMode, false, nil
	}
	spec = strings.TrimSpace(spec)
	if installIsPlainOctal(spec) {
		value, err := strconv.ParseUint(spec, 8, 32)
		if err != nil {
			return 0, false, err
		}
		if value > 0o7777 {
			return 0, false, fmt.Errorf("mode out of range")
		}
		return installModeFromOctal(uint32(value)), true, nil
	}
	mode, err := computeChmodModeWithUmask(0, spec, 0, directory)
	if err != nil {
		return 0, false, err
	}
	return mode & installAllModeBits, true, nil
}

func runInstallDirectories(ctx context.Context, inv *Invocation, db *permissionIdentityDB, opts *installOptions) error {
	if len(opts.files) == 0 {
		return exitf(inv, 1, "install: missing file operand")
	}

	for _, raw := range opts.files {
		info, abs, exists, err := statMaybe(ctx, inv, raw)
		if err != nil {
			return exitf(inv, 1, "install: cannot create directory %s: %s", quoteGNUOperand(raw), installErrorText(err))
		}
		if exists && !info.IsDir() { //nolint:nilaway // info is non-nil when exists is true
			return exitf(inv, 1, "install: cannot create directory %s: File exists", quoteGNUOperand(raw))
		}
		if !exists {
			if err := inv.FS.MkdirAll(ctx, abs, installDefaultMode); err != nil {
				return exitf(inv, 1, "install: cannot create directory %s: %s", quoteGNUOperand(raw), installErrorText(err))
			}
			if opts.verbose {
				if _, err := fmt.Fprintf(inv.Stdout, "install: creating directory %s\n", quoteGNUOperand(raw)); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
		}
		if err := inv.FS.Chmod(ctx, abs, opts.mode); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if err := installApplyOwnership(ctx, inv, db, opts, abs); err != nil {
			return err
		}
	}

	return nil
}

func runInstallFiles(ctx context.Context, inv *Invocation, db *permissionIdentityDB, opts *installOptions) error {
	sources, targetArg, err := installOperands(inv, opts)
	if err != nil {
		return err
	}
	targetPathForOps, err := installResolveTargetOperand(ctx, inv, targetArg, opts.targetDirectorySet || len(sources) > 1)
	if err != nil {
		return err
	}

	if opts.createLeading {
		switch {
		case opts.targetDirectorySet:
			if err := installEnsureRealDirectoryPath(ctx, inv, allowPath(inv, opts.targetDirectory), opts.verbose); err != nil {
				return err
			}
		case len(sources) == 1 && !installRawPathLooksDirectory(targetArg):
			targetInfo, _, targetExists, err := installStatTarget(ctx, inv, targetPathForOps)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if !targetExists || targetInfo == nil || !targetInfo.IsDir() {
				if err := installEnsureRealDirectoryPath(ctx, inv, path.Dir(allowPath(inv, targetArg)), opts.verbose); err != nil {
					return err
				}
			}
		}
	}

	targetInfo, targetAbs, targetExists, err := installStatTarget(ctx, inv, targetPathForOps)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	targetIsDir := targetExists && targetInfo != nil && targetInfo.IsDir()
	targetLstat, _, targetLExists, err := lstatMaybe(ctx, inv, targetPathForOps)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	targetIsSymlink := targetLExists && targetLstat != nil && targetLstat.Mode()&stdfs.ModeSymlink != 0

	if opts.targetDirectorySet || len(sources) > 1 {
		if !targetIsDir {
			return exitf(inv, 1, "install: target %s is not a directory", quoteGNUOperand(targetArg))
		}
		for _, source := range sources {
			destAbs := path.Join(targetAbs, path.Base(allowPath(inv, source)))
			if err := installOne(ctx, inv, db, opts, source, destAbs); err != nil {
				return err
			}
		}
		return nil
	}

	if opts.noTargetDirectory {
		if installRawPathLooksDirectory(targetArg) || (targetIsDir && !targetIsSymlink) {
			return exitf(inv, 1, "install: cannot overwrite directory %s with non-directory", quoteGNUOperand(targetArg))
		}
		return installOne(ctx, inv, db, opts, sources[0], targetAbs)
	}

	if targetIsDir {
		return installOne(ctx, inv, db, opts, sources[0], path.Join(targetAbs, path.Base(allowPath(inv, sources[0]))))
	}
	if installRawPathLooksDirectory(targetArg) {
		return exitf(inv, 1, "install: target %s is not a directory", quoteGNUOperand(targetArg))
	}
	return installOne(ctx, inv, db, opts, sources[0], targetAbs)
}

func installOperands(inv *Invocation, opts *installOptions) ([]string, string, error) {
	args := append([]string(nil), opts.files...)
	if len(args) == 0 {
		return nil, "", exitf(inv, 1, "install: missing file operand")
	}
	if opts.targetDirectorySet {
		return args, opts.targetDirectory, nil
	}
	if opts.noTargetDirectory && len(args) > 2 {
		return nil, "", commandUsageError(inv, "install", "extra operand %s", quoteGNUOperand(args[2]))
	}
	if len(args) == 1 {
		return nil, "", exitf(inv, 1, "install: missing destination file operand after %s", quoteGNUOperand(args[0]))
	}
	return args[:len(args)-1], args[len(args)-1], nil
}

func installOne(ctx context.Context, inv *Invocation, db *permissionIdentityDB, opts *installOptions, sourceArg, destAbs string) error {
	sourceInfo, sourceAbs, err := statPath(ctx, inv, sourceArg)
	if err != nil {
		return exitf(inv, 1, "install: cannot stat %s: %s", quoteGNUOperand(sourceArg), installErrorText(err))
	}
	if sourceInfo.IsDir() {
		return exitf(inv, 1, "install: omitting directory %s", quoteGNUOperand(sourceArg))
	}

	destLstat, _, destExists, err := lstatMaybe(ctx, inv, destAbs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	var (
		destInfo       stdfs.FileInfo
		statDestExists bool
	)
	if destLstat == nil || destLstat.Mode()&stdfs.ModeSymlink == 0 {
		destInfo, _, statDestExists, err = statMaybe(ctx, inv, destAbs)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if destInfo != nil && statDestExists && destInfo.IsDir() { //nolint:nilaway // destInfo is non-nil when statDestExists is true
		return exitf(inv, 1, "install: cannot overwrite directory %s with non-directory", quoteGNUOperand(destAbs))
	}
	if (destLstat == nil || destLstat.Mode()&stdfs.ModeSymlink == 0) && cpSameFile(ctx, inv, sourceAbs, sourceInfo, destAbs, destInfo, destExists) {
		return exitf(inv, 1, "install: %s and %s are the same file", quoteGNUOperand(sourceArg), quoteGNUOperand(destAbs))
	}
	if opts.compare {
		needCopy, err := installNeedsCopy(ctx, inv, opts, sourceAbs, sourceInfo, destAbs, destLstat, destInfo, destExists)
		if err != nil {
			return err
		}
		if !needCopy {
			return nil
		}
	}

	backupAbs, err := installPrepareDestination(ctx, inv, opts, destAbs, destLstat, destExists)
	if err != nil {
		return err
	}
	if err := installCopyFile(ctx, inv, sourceAbs, destAbs); err != nil {
		return err
	}
	if err := installFinalizeCopy(ctx, inv, db, opts, sourceArg, sourceInfo, destAbs, backupAbs); err != nil {
		return err
	}
	return nil
}

func installNeedsCopy(ctx context.Context, inv *Invocation, opts *installOptions, sourceAbs string, sourceInfo stdfs.FileInfo, destAbs string, destLstat, destInfo stdfs.FileInfo, destExists bool) (bool, error) {
	if !destExists || destInfo == nil {
		return true, nil
	}
	if destLstat != nil && destLstat.Mode()&stdfs.ModeSymlink != 0 {
		return true, nil
	}
	if sourceInfo.Mode()&installSpecialModeBits != 0 || destInfo.Mode()&installSpecialModeBits != 0 || opts.mode&installSpecialModeBits != 0 {
		return true, nil
	}
	if destInfo.Mode()&installAllModeBits != opts.mode {
		return true, nil
	}
	if !sourceInfo.Mode().IsRegular() || !destInfo.Mode().IsRegular() {
		return true, nil
	}
	if sourceInfo.Size() != destInfo.Size() {
		return true, nil
	}
	if !opts.unprivileged {
		destOwnership := installOwnershipForInfo(destInfo)
		expectedUID := idUintEnv(inv.Env, "EUID", idUintEnv(inv.Env, "UID", idDefaultUID))
		expectedGID := idUintEnv(inv.Env, "EGID", idUintEnv(inv.Env, "GID", idDefaultGID))
		if opts.ownerID != nil {
			expectedUID = *opts.ownerID
		}
		if opts.groupID != nil {
			expectedGID = *opts.groupID
		}
		if destOwnership.UID != expectedUID || destOwnership.GID != expectedGID {
			return true, nil
		}
	}

	sourceData, _, err := readAllFile(ctx, inv, sourceAbs)
	if err != nil {
		return false, &ExitError{Code: 1, Err: err}
	}
	destData, _, err := readAllFile(ctx, inv, destAbs)
	if err != nil {
		return false, &ExitError{Code: 1, Err: err}
	}
	return !bytes.Equal(sourceData, destData), nil
}

func installPrepareDestination(ctx context.Context, inv *Invocation, opts *installOptions, destAbs string, destLstat stdfs.FileInfo, destExists bool) (string, error) {
	if !destExists {
		return "", nil
	}
	if opts.verbose {
		if _, err := fmt.Fprintf(inv.Stdout, "removed %s\n", quoteGNUOperand(destAbs)); err != nil {
			return "", &ExitError{Code: 1, Err: err}
		}
	}
	if opts.backupMode != backupNone {
		backupAbs, err := backupPath(ctx, inv, opts.backupMode, destAbs, opts.backupSuffix)
		if err != nil {
			return "", err
		}
		if _, _, exists, err := lstatMaybe(ctx, inv, backupAbs); err != nil {
			return "", err
		} else if exists {
			if err := inv.FS.Remove(ctx, backupAbs, false); err != nil {
				return "", &ExitError{Code: 1, Err: err}
			}
		}
		if err := inv.FS.Rename(ctx, destAbs, backupAbs); err != nil {
			return "", &ExitError{Code: 1, Err: err}
		}
		return backupAbs, nil
	}
	if err := inv.FS.Remove(ctx, destAbs, destLstat != nil && destLstat.IsDir()); err != nil {
		return "", &ExitError{Code: 1, Err: err}
	}
	return "", nil
}

func installCopyFile(ctx context.Context, inv *Invocation, sourceAbs, destAbs string) error {
	if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
		return err
	}

	src, err := inv.FS.Open(ctx, sourceAbs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer func() { _ = src.Close() }()

	dst, err := inv.FS.OpenFile(ctx, destAbs, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, src); err != nil {
		_ = inv.FS.Remove(ctx, destAbs, false)
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.TraceRecorder(), "copy", destAbs, sourceAbs, destAbs)
	return nil
}

func installFinalizeCopy(ctx context.Context, inv *Invocation, db *permissionIdentityDB, opts *installOptions, sourceArg string, sourceInfo stdfs.FileInfo, destAbs, backupAbs string) error {
	if opts.strip {
		if err := installRunStripProgram(ctx, inv, opts.stripProgram, destAbs); err != nil {
			_ = inv.FS.Remove(ctx, destAbs, false)
			return err
		}
	}
	if err := inv.FS.Chmod(ctx, destAbs, opts.mode); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if err := installApplyOwnership(ctx, inv, db, opts, destAbs); err != nil {
		return err
	}
	if opts.preserveTimestamps {
		atime, ok := statAccessTime(sourceInfo)
		if !ok {
			atime = sourceInfo.ModTime()
		}
		if err := inv.FS.Chtimes(ctx, destAbs, atime.UTC(), sourceInfo.ModTime().UTC()); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if opts.verbose {
		if backupAbs != "" {
			if _, err := fmt.Fprintf(inv.Stdout, "%s -> %s (backup: %s)\n", quoteGNUOperand(sourceArg), quoteGNUOperand(destAbs), quoteGNUOperand(backupAbs)); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		} else if _, err := fmt.Fprintf(inv.Stdout, "%s -> %s\n", quoteGNUOperand(sourceArg), quoteGNUOperand(destAbs)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func installApplyOwnership(ctx context.Context, inv *Invocation, db *permissionIdentityDB, opts *installOptions, destAbs string) error {
	if opts.unprivileged || (opts.ownerID == nil && opts.groupID == nil) {
		return nil
	}
	info, _, err := statPath(ctx, inv, destAbs)
	if err != nil {
		return err
	}
	current := permissionLookupOwnership(db, info)
	uid := current.uid
	gid := current.gid
	if opts.ownerID != nil {
		uid = *opts.ownerID
	}
	if opts.groupID != nil {
		gid = *opts.groupID
	}
	if err := inv.FS.Chown(ctx, destAbs, uid, gid, true); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

// install's strip hook runs another gbash command inside the same sandbox;
// it never shells out to the host.
func installRunStripProgram(ctx context.Context, inv *Invocation, program, destAbs string) error {
	if inv == nil || inv.Exec == nil {
		return exitf(inv, 1, "install: strip program failed: nested execution is unavailable")
	}

	var stdout, stderr bytes.Buffer
	result, err := inv.Exec(ctx, &ExecutionRequest{
		Command:    []string{program, destAbs},
		Env:        cloneEnv(inv.Env),
		WorkDir:    inv.Cwd,
		ReplaceEnv: true,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})
	if err != nil {
		return exitf(inv, 1, "install: strip program failed: %s", installErrorText(err))
	}
	if result != nil && result.ExitCode == 0 {
		return nil
	}

	message := strings.TrimSpace(stderr.String())
	if message == "" && result != nil {
		message = strings.TrimSpace(result.Stderr)
	}
	if message == "" && result != nil {
		message = strings.TrimSpace(result.ControlStderr)
	}
	if message == "" && result != nil {
		message = fmt.Sprintf("exit status %d", result.ExitCode)
	}
	if message == "" {
		message = "strip failed"
	}
	return exitf(inv, 1, "install: strip program failed: %s", message)
}

func installEnsureRealDirectoryPath(ctx context.Context, inv *Invocation, abs string, verbose bool) error {
	abs = gbfs.Clean(abs)
	if abs == "/" {
		return nil
	}

	currentRaw := "/"
	currentResolved := "/"
	for part := range strings.SplitSeq(strings.TrimPrefix(abs, "/"), "/") {
		if part == "" {
			continue
		}
		rawPath := path.Join(currentRaw, part)
		resolvedPath := path.Join(currentResolved, part)
		info, _, exists, err := lstatMaybe(ctx, inv, resolvedPath)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if exists {
			switch {
			case info.Mode()&stdfs.ModeSymlink != 0:
				target, err := inv.FS.Readlink(ctx, resolvedPath)
				if err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				nextResolved, err := canonicalizeReadlink(ctx, inv, path.Join(path.Dir(resolvedPath), target), readlinkModeCanonicalizeMissing)
				if err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				nextInfo, err := inv.FS.Stat(ctx, nextResolved)
				if err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				if !nextInfo.IsDir() {
					return exitf(inv, 1, "install: failed to access %s: Not a directory", quoteGNUOperand(rawPath))
				}
				currentRaw = rawPath
				currentResolved = nextResolved
				continue
			case info.IsDir():
				currentRaw = rawPath
				currentResolved = resolvedPath
				continue
			default:
				return exitf(inv, 1, "install: failed to access %s: Not a directory", quoteGNUOperand(rawPath))
			}
		}
		if err := inv.FS.MkdirAll(ctx, resolvedPath, installDefaultMode); err != nil {
			return exitf(inv, 1, "install: cannot create directory %s: %s", quoteGNUOperand(rawPath), installErrorText(err))
		}
		if verbose {
			if _, err := fmt.Fprintf(inv.Stdout, "install: creating directory %s\n", quoteGNUOperand(rawPath)); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		currentRaw = rawPath
		currentResolved = resolvedPath
	}
	return nil
}

func installRawPathLooksDirectory(raw string) bool {
	return strings.HasSuffix(raw, "/")
}

func installIsPlainOctal(spec string) bool {
	if spec == "" {
		return false
	}
	for _, ch := range spec {
		if ch < '0' || ch > '7' {
			return false
		}
	}
	return true
}

func installModeFromOctal(value uint32) stdfs.FileMode {
	mode := stdfs.FileMode(value & 0o777)
	if value&0o4000 != 0 {
		mode |= stdfs.ModeSetuid
	}
	if value&0o2000 != 0 {
		mode |= stdfs.ModeSetgid
	}
	if value&0o1000 != 0 {
		mode |= stdfs.ModeSticky
	}
	return mode
}

func installStatTarget(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, bool, error) {
	info, abs, exists, err := statMaybe(ctx, inv, name)
	if err == nil {
		return info, abs, exists, nil
	}
	linfo, labs, lexists, lerr := lstatMaybe(ctx, inv, name)
	if lerr == nil && lexists && linfo.Mode()&stdfs.ModeSymlink != 0 {
		return nil, labs, true, nil
	}
	return nil, "", false, err
}

func installResolveTargetOperand(ctx context.Context, inv *Invocation, raw string, directoryOperand bool) (string, error) {
	abs := allowPath(inv, raw)
	if directoryOperand {
		resolved, err := canonicalizeReadlink(ctx, inv, abs, readlinkModeCanonicalizeMissing)
		if err != nil {
			return "", &ExitError{Code: 1, Err: err}
		}
		return resolved, nil
	}

	resolvedParent, err := canonicalizeReadlink(ctx, inv, path.Dir(abs), readlinkModeCanonicalizeMissing)
	if err != nil {
		return "", &ExitError{Code: 1, Err: err}
	}
	if resolvedParent == "/" {
		return "/" + path.Base(abs), nil
	}
	return path.Join(resolvedParent, path.Base(abs)), nil
}

func installOwnershipForInfo(info stdfs.FileInfo) gbfs.FileOwnership {
	if ownership, ok := gbfs.OwnershipFromFileInfo(info); ok {
		return ownership
	}
	return gbfs.DefaultOwnership()
}

func installErrorText(err error) string {
	switch {
	case errorsIsNotExist(err):
		return "No such file or directory"
	case errorsIsDirectory(err):
		return "Is a directory"
	case os.IsPermission(err):
		return "Permission denied"
	default:
		return err.Error()
	}
}

var _ Command = (*Install)(nil)
var _ SpecProvider = (*Install)(nil)
var _ ParsedRunner = (*Install)(nil)
