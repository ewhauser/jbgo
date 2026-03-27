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

type MV struct{}

type mvOverwriteMode uint8

const (
	mvOverwriteDefault mvOverwriteMode = iota
	mvOverwriteForce
	mvOverwriteInteractive
	mvOverwriteNoClobber
)

type mvDuplicateDirectoryState uint8

const (
	mvDuplicateDirectoryNone mvDuplicateDirectoryState = iota
	mvDuplicateDirectoryMoved
	mvDuplicateDirectoryBlocked
)

type mvMoveOutcome uint8

const (
	mvMoveOutcomeMoved mvMoveOutcome = iota
	mvMoveOutcomeSkipped
	mvMoveOutcomeBlockedSelf
)

type mvResolvedDestination struct {
	abs     string
	display string
}

type mvOptions struct {
	overwriteMode mvOverwriteMode
	verbose       bool
	targetDir     string
	noTarget      bool
	backupMode    backupMode
	backupSuffix  string
	updateMode    cpUpdateMode
	updateArg     string
	promptInput   *rmPromptInput
}

type mvRunState struct {
	justCreatedFiles map[string]string
}

type mvParsedUpdate struct {
	value            string
	hasExplicitValue bool
}

func NewMV() *MV {
	return &MV{}
}

func (c *MV) Name() string {
	return "mv"
}

func (c *MV) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *MV) Spec() CommandSpec {
	return CommandSpec{
		Name:  "mv",
		About: "Rename SOURCE to DEST, or move SOURCE(s) to DIRECTORY.",
		Usage: "mv [OPTION]... SOURCE DEST\n" +
			"       mv [OPTION]... SOURCE... DIRECTORY\n" +
			"       mv [OPTION]... -t DIRECTORY SOURCE...",
		Options: []OptionSpec{
			{Name: "backup-short", Short: 'b', Help: "like --backup but does not accept an argument"},
			{Name: "backup", Long: "backup", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "CONTROL", Help: "make a backup of each existing destination file"},
			{Name: "force", Short: 'f', Long: "force", Help: "do not prompt before overwriting"},
			{Name: "interactive", Short: 'i', Help: "prompt before overwrite"},
			{Name: "no-clobber", Short: 'n', Long: "no-clobber", Help: "do not overwrite an existing file"},
			{Name: "suffix", Short: 'S', Long: "suffix", Arity: OptionRequiredValue, ValueName: "SUFFIX", Help: "override the usual backup suffix"},
			{Name: "update", Short: 'u', Long: "update", ValueName: "WHEN", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "control which existing files are replaced"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "explain what is being done"},
			{Name: "target-directory", Short: 't', Long: "target-directory", Arity: OptionRequiredValue, ValueName: "DIRECTORY", Help: "move all SOURCE arguments into DIRECTORY"},
			{Name: "no-target-directory", Short: 'T', Long: "no-target-directory", Help: "treat DEST as a normal file"},
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

func (c *MV) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, args, err := parseMVMatches(inv, matches)
	if err != nil {
		return err
	}
	defer func() { _ = rmClosePromptInput(opts.promptInput) }()

	sources, destArg, err := mvOperands(inv, &opts, args)
	if err != nil {
		return err
	}
	if opts.targetDir != "" {
		if _, err := mvRequireDestinationDirectory(ctx, inv, destArg, true); err != nil {
			return err
		}
	} else if len(sources) > 1 && !opts.noTarget {
		if _, err := mvRequireDestinationDirectory(ctx, inv, destArg, false); err != nil {
			return err
		}
	}
	multipleSources := len(sources) > 1 || opts.targetDir != ""
	duplicateDirectoryKeys := mvDuplicateDirectoryKeys(ctx, inv, sources)
	duplicateDirectoryState := make(map[string]mvDuplicateDirectoryState, len(duplicateDirectoryKeys))
	state := mvRunState{
		justCreatedFiles: make(map[string]string),
	}

	hadErr := false
	for _, source := range sources {
		if key := duplicateDirectoryKeys[source]; key != "" && duplicateDirectoryState[key] == mvDuplicateDirectoryBlocked {
			if err := mvWriteWarning(inv, source); err != nil {
				return err
			}
			continue
		}

		outcome, err := mvMoveOne(ctx, inv, source, destArg, multipleSources, &opts, &state)
		if key := duplicateDirectoryKeys[source]; key != "" {
			switch {
			case outcome == mvMoveOutcomeBlockedSelf:
				duplicateDirectoryState[key] = mvDuplicateDirectoryBlocked
			case err == nil:
				duplicateDirectoryState[key] = mvDuplicateDirectoryMoved
			}
		}
		if err != nil {
			if code, ok := ExitCode(err); ok && code != 0 {
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

func parseMVMatches(inv *Invocation, matches *ParsedCommand) (mvOptions, []string, error) {
	opts := mvOptions{
		updateMode:  cpUpdateAll,
		promptInput: &rmPromptInput{},
	}
	if matches == nil {
		return opts, nil, nil
	}
	opts.backupSuffix = determineBackupSuffix(inv, matches)
	backupMode, err := determineBackupMode(inv, matches, "mv")
	if err != nil {
		return mvOptions{}, nil, err
	}
	opts.backupMode = backupMode

	updateValues := mvParseUpdateOccurrences(inv)
	updateIndex := 0
	for _, name := range matches.OptionOrder() {
		switch name {
		case "force":
			opts.overwriteMode = mvOverwriteForce
		case "interactive":
			opts.overwriteMode = mvOverwriteInteractive
		case "no-clobber":
			opts.overwriteMode = mvOverwriteNoClobber
		case "verbose":
			opts.verbose = true
		case "target-directory":
			opts.targetDir = matches.Value("target-directory")
		case "no-target-directory":
			opts.noTarget = true
		case "update":
			update := mvParsedUpdate{}
			if updateIndex < len(updateValues) {
				update = updateValues[updateIndex]
			}
			updateIndex++
			updateMode := cpParseUpdateMode(cpParsedUpdate(update))
			if updateMode == cpUpdateInvalid {
				if opts.updateMode != cpUpdateInvalid {
					opts.updateMode = cpUpdateInvalid
					opts.updateArg = update.value
				}
				continue
			}
			if opts.updateMode != cpUpdateInvalid {
				opts.updateMode = updateMode
				opts.updateArg = update.value
			}
		}
	}

	if err := mvValidateOptions(inv, &opts); err != nil {
		return mvOptions{}, nil, err
	}
	return opts, matches.Args("file"), nil
}

func mvParseUpdateOccurrences(inv *Invocation) []mvParsedUpdate {
	if inv == nil {
		return nil
	}
	var updates []mvParsedUpdate
	for _, arg := range inv.Args {
		if arg == "--" {
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			continue
		}
		if name, ok := strings.CutPrefix(arg, "--"); ok {
			opt, value, hasValue := strings.Cut(name, "=")
			if opt != "" && len(opt) <= len("update") && "update"[:len(opt)] == opt {
				updates = append(updates, mvParsedUpdate{value: value, hasExplicitValue: hasValue})
			}
			continue
		}
		shorts := strings.TrimPrefix(arg, "-")
	shortLoop:
		for i, ch := range shorts {
			remaining := shorts[i+1:]
			switch ch {
			case 'u':
				update := mvParsedUpdate{}
				if strings.HasPrefix(remaining, "=") {
					update.value = remaining[1:]
					update.hasExplicitValue = true
					updates = append(updates, update)
					break shortLoop
				}
				updates = append(updates, update)
			case 't', 'S':
				if remaining != "" {
					break shortLoop
				}
			}
		}
	}
	return updates
}

func mvValidateOptions(inv *Invocation, opts *mvOptions) error {
	if opts == nil {
		return nil
	}
	if opts.updateMode == cpUpdateInvalid {
		return commandUsageError(inv, "mv", "invalid argument %q for '--update'", opts.updateArg)
	}
	if opts.targetDir != "" && opts.noTarget {
		return commandUsageError(inv, "mv", "cannot combine --target-directory and --no-target-directory")
	}
	if opts.backupMode != backupNone {
		if opts.overwriteMode == mvOverwriteNoClobber {
			return commandUsageError(inv, "mv", "options --backup and --no-clobber are mutually exclusive")
		}
		if opts.updateMode == cpUpdateNone || opts.updateMode == cpUpdateNoneFail {
			return commandUsageError(inv, "mv", "options --backup and --update=none or --update=none-fail are mutually exclusive")
		}
	}
	return nil
}

func mvOperands(inv *Invocation, opts *mvOptions, args []string) (sources []string, destArg string, err error) {
	if opts != nil && opts.targetDir != "" {
		if len(args) == 0 {
			return nil, "", commandUsageError(inv, "mv", "missing file operand")
		}
		return args, opts.targetDir, nil
	}
	switch len(args) {
	case 0:
		return nil, "", commandUsageError(inv, "mv", "missing file operand")
	case 1:
		return nil, "", commandUsageError(inv, "mv", "missing destination file operand after %s", quoteGNUOperand(args[0]))
	case 2:
		return args[:1], args[1], nil
	default:
		if opts != nil && opts.noTarget {
			return nil, "", commandUsageError(inv, "mv", "extra operand %s", quoteGNUOperand(args[2]))
		}
		return args[:len(args)-1], args[len(args)-1], nil
	}
}

func mvMoveOne(ctx context.Context, inv *Invocation, sourceArg, destArg string, multipleSources bool, opts *mvOptions, state *mvRunState) (mvMoveOutcome, error) {
	srcLInfo, srcAbs, err := lstatPath(ctx, inv, sourceArg)
	if err != nil {
		return mvMoveOutcomeSkipped, exitf(inv, 1, "mv: cannot stat %s: No such file or directory", quoteGNUOperand(sourceArg))
	}
	sourceIsDir, srcInfo, err := mvSourceInfo(ctx, inv, sourceArg, srcAbs, srcLInfo, opts)
	if err != nil {
		return mvMoveOutcomeSkipped, err
	}

	dest, err := mvResolveDestination(ctx, inv, sourceArg, sourceIsDir, destArg, multipleSources, opts)
	if err != nil {
		return mvMoveOutcomeSkipped, err
	}

	destLInfo, _, destExists, err := lstatMaybe(ctx, inv, dest.abs)
	if err != nil {
		return mvMoveOutcomeSkipped, mvMoveError(inv, sourceArg, dest.display, err)
	}
	if sourceIsDir && isWithinMovedTree(srcAbs, dest.abs) {
		return mvMoveOutcomeBlockedSelf, exitf(inv, 1, "mv: cannot move %s to a subdirectory of itself, %s", quoteGNUOperand(sourceArg), quoteGNUOperand(dest.display))
	}

	sameFile := mvSameFile(ctx, inv, sourceArg, srcAbs, srcLInfo, dest.abs, destLInfo, destExists)
	if sameFile {
		switch opts.updateMode {
		case cpUpdateNone:
			return mvMoveOutcomeSkipped, nil
		case cpUpdateNoneFail:
			return mvMoveOutcomeSkipped, exitf(inv, 1, "mv: not replacing %s", quoteGNUOperand(dest.display))
		}
	}
	if sameFile && srcAbs == dest.abs {
		return mvMoveOutcomeSkipped, exitf(inv, 1, "mv: %s and %s are the same file", quoteGNUOperand(sourceArg), quoteGNUOperand(dest.display))
	}

	if sameFile && opts.backupMode == backupNone {
		return mvMoveOutcomeSkipped, exitf(inv, 1, "mv: %s and %s are the same file", quoteGNUOperand(sourceArg), quoteGNUOperand(dest.display))
	}

	destInfo := mvDestinationInfo(ctx, inv, dest.abs, destLInfo, destExists)
	if !sameFile {
		skip, err := mvShouldSkipExisting(ctx, inv, srcInfo, dest.display, dest.abs, destLInfo, destInfo, destExists, opts)
		if err != nil {
			return mvMoveOutcomeSkipped, err
		}
		if skip {
			return mvMoveOutcomeSkipped, nil
		}
	}
	if !sourceIsDir && opts.backupMode != backupNumbered {
		if createdDisplay, ok := mvJustCreatedDestination(state, dest.abs); ok {
			return mvMoveOutcomeSkipped, exitf(inv, 1, "mv: will not overwrite just-created %s with %s", quoteGNUOperand(createdDisplay), quoteGNUOperand(sourceArg))
		}
	}
	if destExists && opts.backupMode == backupNone {
		if err := mvValidateDestination(ctx, inv, sourceArg, sourceIsDir, dest.abs, dest.display, destLInfo); err != nil {
			return mvMoveOutcomeSkipped, err
		}
	}

	if destExists && opts.overwriteMode == mvOverwriteInteractive {
		replace, err := mvPromptOverwrite(ctx, inv, dest.display, opts)
		if err != nil {
			return mvMoveOutcomeSkipped, err
		}
		if !replace {
			return mvMoveOutcomeSkipped, &ExitError{Code: 1}
		}
	}
	if err := mvCheckRenamePermissions(ctx, inv, sourceArg, dest.display, srcAbs, dest.abs); err != nil {
		return mvMoveOutcomeSkipped, err
	}

	backupDisplay := ""
	if destExists && opts.backupMode != backupNone {
		backupAbs, err := backupPath(ctx, inv, opts.backupMode, dest.abs, opts.backupSuffix)
		if err != nil {
			return mvMoveOutcomeSkipped, err
		}
		if err := mvRemoveIfExists(ctx, inv, backupAbs, dest.display); err != nil {
			return mvMoveOutcomeSkipped, err
		}
		if err := inv.FS.Rename(ctx, dest.abs, backupAbs); err != nil {
			return mvMoveOutcomeSkipped, mvMoveError(inv, sourceArg, mvDisplaySiblingPath(dest.display, backupAbs), err)
		}
		backupDisplay = mvDisplaySiblingPath(dest.display, backupAbs)
		destExists = false
		destLInfo = nil
	}

	if destExists {
		if err := mvRemoveDestination(ctx, inv, sourceArg, dest.abs, dest.display, destLInfo); err != nil {
			return mvMoveOutcomeSkipped, err
		}
	}
	if err := ensureParentDirExists(ctx, inv, dest.abs); err != nil {
		return mvMoveOutcomeSkipped, err
	}
	if err := inv.FS.Rename(ctx, srcAbs, dest.abs); err != nil {
		return mvMoveOutcomeSkipped, mvMoveError(inv, sourceArg, dest.display, err)
	}
	mvRememberJustCreatedDestination(state, sourceIsDir, dest.abs, dest.display)
	if opts.verbose {
		if err := mvWriteVerbose(inv, sourceArg, dest.display, backupDisplay); err != nil {
			return mvMoveOutcomeSkipped, err
		}
	}
	return mvMoveOutcomeMoved, nil
}

func mvSourceInfo(ctx context.Context, inv *Invocation, sourceArg, srcAbs string, srcLInfo stdfs.FileInfo, opts *mvOptions) (bool, stdfs.FileInfo, error) {
	sourceIsDir := srcLInfo != nil && srcLInfo.IsDir()
	sourceInfo := srcLInfo
	if hasTrailingSlash(sourceArg) || (srcLInfo != nil && srcLInfo.Mode()&stdfs.ModeSymlink != 0 && opts != nil && opts.updateMode == cpUpdateOlder) {
		info, _, err := statPath(ctx, inv, srcAbs)
		if err != nil {
			return false, nil, exitf(inv, 1, "mv: cannot stat %s: No such file or directory", quoteGNUOperand(sourceArg))
		}
		sourceInfo = info
		if info.IsDir() {
			sourceIsDir = true
		}
	}
	return sourceIsDir, sourceInfo, nil
}

func mvResolveDestination(ctx context.Context, inv *Invocation, sourceArg string, sourceIsDir bool, destArg string, multipleSources bool, opts *mvOptions) (mvResolvedDestination, error) {
	if opts != nil && opts.noTarget {
		info, _, exists, err := lstatMaybe(ctx, inv, destArg)
		if err != nil {
			return mvResolvedDestination{}, err
		}
		if hasTrailingSlash(destArg) && !sourceIsDir && (!exists || info == nil || !info.IsDir()) {
			return mvResolvedDestination{}, exitf(inv, 1, "mv: cannot move %s to %s: Not a directory", quoteGNUOperand(sourceArg), quoteGNUOperand(destArg))
		}
		return mvResolvedDestination{
			abs:     allowPath(inv, destArg),
			display: mvTrimDisplayPath(destArg),
		}, nil
	}

	if opts != nil && opts.targetDir != "" {
		dirAbs, err := mvRequireDestinationDirectory(ctx, inv, destArg, true)
		if err != nil {
			return mvResolvedDestination{}, err
		}
		base := mvSourceBase(sourceArg)
		return mvResolvedDestination{
			abs:     path.Join(dirAbs, base),
			display: mvJoinDisplayPath(destArg, base),
		}, nil
	}

	if multipleSources {
		dirAbs, err := mvRequireDestinationDirectory(ctx, inv, destArg, false)
		if err != nil {
			return mvResolvedDestination{}, err
		}
		base := mvSourceBase(sourceArg)
		return mvResolvedDestination{
			abs:     path.Join(dirAbs, base),
			display: mvJoinDisplayPath(destArg, base),
		}, nil
	}

	info, abs, exists, err := statMaybe(ctx, inv, destArg)
	if err != nil {
		return mvResolvedDestination{}, err
	}
	if exists && info != nil && info.IsDir() {
		base := mvSourceBase(sourceArg)
		return mvResolvedDestination{
			abs:     path.Join(abs, base),
			display: mvJoinDisplayPath(destArg, base),
		}, nil
	}
	trimmed := mvTrimDisplayPath(destArg)
	if trimmed == "" {
		trimmed = "/"
	}
	if hasTrailingSlash(destArg) && !sourceIsDir && (!exists || info == nil || !info.IsDir()) {
		return mvResolvedDestination{}, exitf(inv, 1, "mv: cannot move %s to %s: Not a directory", quoteGNUOperand(sourceArg), quoteGNUOperand(destArg))
	}
	return mvResolvedDestination{
		abs:     allowPath(inv, destArg),
		display: trimmed,
	}, nil
}

func mvRequireDestinationDirectory(ctx context.Context, inv *Invocation, destArg string, explicit bool) (string, error) {
	info, abs, exists, err := statMaybe(ctx, inv, destArg)
	if err != nil {
		return "", err
	}
	if !exists || info == nil {
		if explicit {
			return "", exitf(inv, 1, "mv: target directory %s: No such file or directory", quoteGNUOperand(destArg))
		}
		return "", exitf(inv, 1, "mv: target %s: No such file or directory", quoteGNUOperand(destArg))
	}
	if !info.IsDir() {
		if explicit {
			return "", exitf(inv, 1, "mv: target directory %s: Not a directory", quoteGNUOperand(destArg))
		}
		return "", exitf(inv, 1, "mv: target %s: Not a directory", quoteGNUOperand(destArg))
	}
	return abs, nil
}

func mvDestinationInfo(ctx context.Context, inv *Invocation, destAbs string, destLInfo stdfs.FileInfo, destExists bool) stdfs.FileInfo {
	if !destExists || destLInfo == nil {
		return nil
	}
	if destLInfo.Mode()&stdfs.ModeSymlink == 0 {
		return destLInfo
	}
	info, _, err := statPath(ctx, inv, destAbs)
	if err != nil {
		return destLInfo
	}
	return info
}

func mvSameFile(ctx context.Context, inv *Invocation, sourceArg, srcAbs string, srcLInfo stdfs.FileInfo, destAbs string, destLInfo stdfs.FileInfo, destExists bool) bool {
	if !destExists {
		return false
	}
	if srcAbs == destAbs {
		return true
	}
	if srcKey, ok := fileInfoIdentity(srcLInfo); ok {
		if destKey, ok := fileInfoIdentity(destLInfo); ok && srcKey == destKey {
			return true
		}
	}
	if srcLInfo != nil && destLInfo != nil && testSameFile(srcLInfo, destLInfo) {
		return true
	}
	if srcLInfo != nil && srcLInfo.Mode()&stdfs.ModeSymlink != 0 && !hasTrailingSlash(sourceArg) {
		target, err := inv.FS.Readlink(ctx, srcAbs)
		if err != nil {
			return false
		}
		resolved := path.Clean(path.Join(path.Dir(srcAbs), target))
		if realDst, err := inv.FS.Realpath(ctx, destAbs); err == nil {
			if realResolved, err := inv.FS.Realpath(ctx, resolved); err == nil {
				return realResolved == realDst
			}
			return resolved == realDst
		}
		return false
	}
	if realSrc, err := inv.FS.Realpath(ctx, srcAbs); err == nil {
		if realDst, err := inv.FS.Realpath(ctx, destAbs); err == nil && realSrc == realDst {
			return true
		}
	}
	return false
}

func mvShouldSkipExisting(ctx context.Context, inv *Invocation, srcInfo stdfs.FileInfo, destDisplay, destAbs string, destLInfo, destInfo stdfs.FileInfo, destExists bool, opts *mvOptions) (bool, error) {
	if !destExists || opts == nil {
		return false, nil
	}
	if opts.overwriteMode == mvOverwriteNoClobber {
		return true, nil
	}
	switch opts.updateMode {
	case cpUpdateAll:
		return false, nil
	case cpUpdateNone:
		return true, nil
	case cpUpdateNoneFail:
		return false, exitf(inv, 1, "mv: not replacing %s", quoteGNUOperand(destDisplay))
	case cpUpdateOlder:
		if srcInfo == nil || destInfo == nil {
			return false, nil
		}
		if destLInfo != nil && destLInfo.Mode()&stdfs.ModeSymlink != 0 {
			if resolvedInfo, _, err := statPath(ctx, inv, destAbs); err == nil {
				destInfo = resolvedInfo
			}
		}
		if destInfo == nil {
			return false, nil
		}
		if srcInfo.IsDir() || destInfo.IsDir() {
			return false, nil
		}
		return !srcInfo.ModTime().After(destInfo.ModTime()), nil
	default:
		return false, nil
	}
}

func mvPromptOverwrite(ctx context.Context, inv *Invocation, destDisplay string, opts *mvOptions) (bool, error) {
	reader, err := mvPromptReader(ctx, inv, opts)
	if err != nil {
		return false, err
	}
	if inv != nil && inv.Stderr != nil {
		if _, err := fmt.Fprintf(inv.Stderr, "mv: overwrite %s? ", quoteGNUOperand(destDisplay)); err != nil {
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
		return false, exitf(inv, 1, "mv: Failed to read from standard input")
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

func mvPromptReader(ctx context.Context, inv *Invocation, opts *mvOptions) (*bufio.Reader, error) {
	if opts != nil && opts.promptInput != nil && opts.promptInput.reader != nil {
		return opts.promptInput.reader, nil
	}
	if inv != nil && inv.Stdin != nil {
		reader := bufio.NewReader(inv.Stdin)
		if opts != nil && opts.promptInput != nil {
			opts.promptInput.reader = reader
		}
		return reader, nil
	}
	file, _, err := openRead(ctx, inv, "/dev/tty")
	if err == nil {
		reader := bufio.NewReader(file)
		if opts != nil && opts.promptInput != nil {
			opts.promptInput.reader = reader
			opts.promptInput.closer = file
		}
		return reader, nil
	}
	return nil, exitf(inv, 1, "mv: failed to open standard input")
}

func mvCheckRenamePermissions(ctx context.Context, inv *Invocation, sourceArg, destDisplay, srcAbs, destAbs string) error {
	parentPaths := []string{path.Dir(srcAbs)}
	if path.Dir(destAbs) != parentPaths[0] {
		parentPaths = append(parentPaths, path.Dir(destAbs))
	}
	for _, parent := range parentPaths {
		info, _, err := statPath(ctx, inv, parent)
		if err != nil {
			continue
		}
		if info == nil || !info.IsDir() {
			continue
		}
		if !mvHasDirectoryRenamePermission(inv, info) {
			return exitf(inv, 1, "mv: cannot move %s to %s: Permission denied", quoteGNUOperand(sourceArg), quoteGNUOperand(destDisplay))
		}
	}
	return nil
}

func mvHasDirectoryRenamePermission(inv *Invocation, info stdfs.FileInfo) bool {
	if testCurrentID(inv, "EUID") == 0 {
		return true
	}
	ownerUID, ownerGID, ok := testOwnerIDs(info)
	if !ok {
		return true
	}
	currentUID := testCurrentID(inv, "EUID")
	currentGID := testCurrentID(inv, "EGID")
	if currentUID != ownerUID && currentGID != ownerGID {
		return true
	}
	return testHasPermission(inv, info, 0o1) && testHasPermission(inv, info, 0o2)
}

func mvValidateDestination(ctx context.Context, inv *Invocation, sourceArg string, sourceIsDir bool, destAbs, destDisplay string, destLInfo stdfs.FileInfo) error {
	destIsDir := destLInfo != nil && destLInfo.IsDir()
	switch {
	case sourceIsDir && !destIsDir:
		return exitf(inv, 1, "mv: cannot overwrite non-directory %s with directory %s", quoteGNUOperand(destDisplay), quoteGNUOperand(sourceArg))
	case !sourceIsDir && destIsDir:
		return exitf(inv, 1, "mv: cannot overwrite directory %s with non-directory", quoteGNUOperand(destDisplay))
	case sourceIsDir && destIsDir:
		entries, err := readDir(ctx, inv, destAbs)
		if err != nil {
			return mvMoveError(inv, sourceArg, destDisplay, err)
		}
		if len(entries) != 0 {
			return exitf(inv, 1, "mv: cannot overwrite %s: Directory not empty", quoteGNUOperand(destDisplay))
		}
	}
	return nil
}

func mvRemoveDestination(ctx context.Context, inv *Invocation, sourceArg, destAbs, destDisplay string, destLInfo stdfs.FileInfo) error {
	if destLInfo != nil && destLInfo.IsDir() {
		if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return mvMoveError(inv, sourceArg, destDisplay, err)
		}
		return nil
	}
	if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return mvMoveError(inv, sourceArg, destDisplay, err)
	}
	return nil
}

func mvRemoveIfExists(ctx context.Context, inv *Invocation, targetAbs, destDisplay string) error {
	info, _, exists, err := lstatMaybe(ctx, inv, targetAbs)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if info != nil && info.IsDir() {
		return exitf(inv, 1, "mv: cannot backup %s: Is a directory", quoteGNUOperand(destDisplay))
	}
	return inv.FS.Remove(ctx, targetAbs, true)
}

func mvMoveError(inv *Invocation, sourceArg, destDisplay string, err error) error {
	return exitf(inv, 1, "mv: cannot move %s to %s: %s", quoteGNUOperand(sourceArg), quoteGNUOperand(destDisplay), mvErrText(err))
}

func mvErrText(err error) string {
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
		message := err.Error()
		if strings.Contains(strings.ToLower(message), "directory not empty") {
			return "Directory not empty"
		}
		if strings.Contains(strings.ToLower(message), "permission denied") {
			return "Permission denied"
		}
		return message
	}
}

func mvWriteVerbose(inv *Invocation, sourceArg, destDisplay, backupDisplay string) error {
	if inv == nil || inv.Stdout == nil {
		return nil
	}
	var err error
	if backupDisplay != "" {
		_, err = fmt.Fprintf(inv.Stdout, "renamed %s -> %s (backup: %s)\n", quoteGNUOperand(sourceArg), quoteGNUOperand(destDisplay), quoteGNUOperand(backupDisplay))
	} else {
		_, err = fmt.Fprintf(inv.Stdout, "renamed %s -> %s\n", quoteGNUOperand(sourceArg), quoteGNUOperand(destDisplay))
	}
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func mvWriteWarning(inv *Invocation, sourceArg string) error {
	if inv == nil || inv.Stderr == nil {
		return nil
	}
	if _, err := fmt.Fprintf(inv.Stderr, "mv: warning: source directory %s specified more than once\n", quoteGNUOperand(sourceArg)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func mvJustCreatedDestination(state *mvRunState, destAbs string) (string, bool) {
	if state == nil || state.justCreatedFiles == nil {
		return "", false
	}
	display, ok := state.justCreatedFiles[destAbs]
	if !ok || display == "" {
		return "", false
	}
	return display, true
}

func mvRememberJustCreatedDestination(state *mvRunState, sourceIsDir bool, destAbs, destDisplay string) {
	if state == nil || state.justCreatedFiles == nil || sourceIsDir {
		return
	}
	state.justCreatedFiles[destAbs] = destDisplay
}

func mvDuplicateDirectoryKeys(ctx context.Context, inv *Invocation, sources []string) map[string]string {
	keys := make(map[string]string, len(sources))
	counts := make(map[string]int)
	for _, source := range sources {
		linfo, abs, err := lstatPath(ctx, inv, source)
		if err != nil || linfo == nil {
			continue
		}
		info := linfo
		if !linfo.IsDir() {
			if linfo.Mode()&stdfs.ModeSymlink == 0 || !hasTrailingSlash(source) {
				continue
			}
			info, _, err = statPath(ctx, inv, source)
			if err != nil || info == nil || !info.IsDir() {
				continue
			}
		}
		key := abs
		if realPath, err := inv.FS.Realpath(ctx, abs); err == nil {
			key = realPath
		}
		keys[source] = key
		counts[key]++
	}
	for source, key := range keys {
		if counts[key] < 2 {
			delete(keys, source)
		}
	}
	return keys
}

func mvSourceBase(source string) string {
	trimmed := strings.TrimRight(source, "/")
	if trimmed == "" {
		return path.Base(source)
	}
	return path.Base(trimmed)
}

func mvJoinDisplayPath(dir, base string) string {
	trimmed := mvTrimDisplayPath(dir)
	switch trimmed {
	case "", ".":
		return "./" + base
	case "/":
		return "/" + base
	default:
		return trimmed + "/" + base
	}
}

func mvTrimDisplayPath(name string) string {
	if name == "/" {
		return "/"
	}
	trimmed := strings.TrimRight(name, "/")
	if trimmed == "" {
		if strings.HasPrefix(name, "/") {
			return "/"
		}
		return "."
	}
	return trimmed
}

func mvDisplaySiblingPath(referenceDisplay, siblingAbs string) string {
	if path.IsAbs(referenceDisplay) {
		return siblingAbs
	}
	dir := path.Dir(referenceDisplay)
	base := path.Base(siblingAbs)
	switch dir {
	case ".":
		return base
	case "/":
		return "/" + base
	default:
		return dir + "/" + base
	}
}

func isWithinMovedTree(srcAbs, destAbs string) bool {
	return destAbs == srcAbs || len(destAbs) > len(srcAbs) && destAbs[:len(srcAbs)] == srcAbs && destAbs[len(srcAbs)] == '/'
}

var _ Command = (*MV)(nil)
var _ SpecProvider = (*MV)(nil)
var _ ParsedRunner = (*MV)(nil)
