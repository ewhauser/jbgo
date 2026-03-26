package builtins

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
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

type cpInteractiveMode uint8

const (
	cpInteractiveNever cpInteractiveMode = iota
	cpInteractiveAlways
)

type cpPreserveAttr uint32

const (
	cpPreserveMode cpPreserveAttr = 1 << iota
	cpPreserveOwnership
	cpPreserveTimestamps
	cpPreserveLinks
)

const (
	cpDefaultPreserveMask = cpPreserveMode | cpPreserveOwnership | cpPreserveTimestamps
	cpArchivePreserveMask = cpDefaultPreserveMask | cpPreserveLinks
)

type cpPromptInput struct {
	reader *bufio.Reader
	closer io.Closer
}

type cpRunState struct {
	promptInput        *cpPromptInput
	justCreatedSymlink map[string]string
	preservedLinks     map[string]string
	interactiveSkipped bool
}

type cpParsedUpdate struct {
	value            string
	hasExplicitValue bool
}

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
			{Name: "backup-short", Short: 'b', Help: "like --backup but does not accept an argument"},
			{Name: "backup", Long: "backup", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "CONTROL", Help: "make a backup of each existing destination file"},
			{Name: "attributes-only", Long: "attributes-only", Help: "don't copy the file data, just the attributes"},
			{Name: "copy-contents", Long: "copy-contents", Help: "copy contents of special files when recursive"},
			{Name: "debug", Long: "debug", Help: "explain how a file is copied"},
			{Name: "no-dereference", Short: 'd', Long: "no-dereference", Help: "copy symbolic links as symbolic links"},
			{Name: "force", Short: 'f', Long: "force", Help: "overwrite an existing destination file"},
			{Name: "dereference-command-line", Short: 'H', Help: "follow command line symbolic links in SOURCE"},
			{Name: "dereference", Short: 'L', Help: "always follow symbolic links in SOURCE"},
			{Name: "hard-link", Short: 'l', Long: "link", Help: "hard link files instead of copying"},
			{Name: "interactive", Short: 'i', Long: "interactive", Help: "prompt before overwrite"},
			{Name: "recursive", Short: 'r', ShortAliases: []rune{'R'}, Long: "recursive", Help: "copy directories recursively"},
			{Name: "no-clobber", Short: 'n', Long: "no-clobber", Help: "do not overwrite an existing file"},
			{Name: "no-preserve", Long: "no-preserve", Arity: OptionRequiredValue, ValueName: "ATTR_LIST", Help: "don't preserve the specified attributes"},
			{Name: "parents", Long: "parents", Help: "use full source file name under DIRECTORY"},
			{Name: "physical", Short: 'P', Help: "never follow symbolic links in SOURCE"},
			{Name: "preserve-short", Short: 'p', Help: "same as --preserve=mode,ownership,timestamps"},
			{Name: "preserve", Long: "preserve", ValueName: "ATTR_LIST", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "preserve the specified attributes"},
			{Name: "reflink", Long: "reflink", ValueName: "WHEN", Arity: OptionRequiredValue, Help: "control clone/CoW copies"},
			{Name: "remove-destination", Long: "remove-destination", Help: "remove each existing destination file before opening it"},
			{Name: "symbolic-link", Short: 's', Long: "symbolic-link", Help: "make symbolic links instead of copying"},
			{Name: "suffix", Short: 'S', Long: "suffix", Arity: OptionRequiredValue, ValueName: "SUFFIX", Help: "override the usual backup suffix"},
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
	opts, err := parseCPMatches(inv, matches)
	if err != nil {
		return err
	}
	if err := validateCPOptions(inv, &opts); err != nil {
		return err
	}
	args := matches.Positionals()
	sources, destArg, err := cpOperands(inv, &opts, args)
	if err != nil {
		return err
	}
	state := cpRunState{
		promptInput:        &cpPromptInput{},
		justCreatedSymlink: make(map[string]string),
		preservedLinks:     make(map[string]string),
	}
	defer func() { _ = cpClosePromptInput(state.promptInput) }()
	multipleSources := len(sources) > 1

	for _, source := range sources {
		srcInfo, srcAbs, srcLinkInfo, err := resolveCPSource(ctx, inv, source, &opts, true)
		if err != nil {
			return exitf(inv, 1, "cp: cannot stat %q: %s", source, cpErrorText(err))
		}

		destAbs, destDisplay, _, _, err := resolveCPDestination(ctx, inv, &opts, source, destArg, multipleSources)
		if err != nil {
			return err
		}
		destInfo, _, destExists, err := lstatMaybe(ctx, inv, destAbs)
		if err != nil {
			return err
		}
		if err := cpCopyResolvedSource(ctx, inv, source, srcAbs, srcInfo, srcLinkInfo, destAbs, destDisplay, destInfo, destExists, &opts, &state); err != nil {
			return err
		}
	}
	if state.interactiveSkipped {
		return &ExitError{Code: 1}
	}

	return nil
}

type cpOptions struct {
	recursive         bool
	noClobber         bool
	preserve          cpPreserveAttr
	verbose           bool
	force             bool
	interactive       cpInteractiveMode
	dereference       cpDereferenceMode
	noTargetDirectory bool
	targetDirectory   string
	removeDestination bool
	copyMode          cpCopyMode
	hardLinkMode      bool
	symbolicLinkMode  bool
	updateMode        cpUpdateMode
	updateArg         string
	backupMode        backupMode
	backupSuffix      string
	copyContents      bool
	attributesOnly    bool
	debug             bool
	parents           bool
}

func parseCPMatches(inv *Invocation, matches *ParsedCommand) (cpOptions, error) {
	opts := cpOptions{
		updateMode:   cpUpdateAll,
		backupSuffix: determineBackupSuffix(inv, matches),
	}
	if matches == nil {
		return opts, nil
	}
	for _, occurrence := range matches.OptionOccurrences() {
		switch occurrence.Name {
		case "archive":
			opts.recursive = true
			opts.dereference = cpDerefNever
			cpAddPreserve(&opts, cpArchivePreserveMask)
		case "recursive":
			opts.recursive = true
		case "no-clobber":
			opts.noClobber = true
			opts.force = false
			opts.interactive = cpInteractiveNever
		case "preserve-short":
			cpAddPreserve(&opts, cpDefaultPreserveMask)
		case "preserve":
			mask, err := cpParsePreserveMask(inv, occurrence.Value, occurrence.HasValue, false)
			if err != nil {
				return cpOptions{}, err
			}
			cpAddPreserve(&opts, mask)
		case "verbose":
			opts.verbose = true
		case "force":
			opts.force = true
			opts.interactive = cpInteractiveNever
		case "interactive":
			opts.interactive = cpInteractiveAlways
			opts.noClobber = false
			opts.force = false
		case "no-dereference", "physical":
			opts.dereference = cpDerefNever
			if occurrence.Name == "no-dereference" {
				cpAddPreserve(&opts, cpPreserveLinks)
			}
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
			opts.hardLinkMode = true
		case "symbolic-link":
			opts.copyMode = cpCopySymbolicLink
			opts.symbolicLinkMode = true
		case "attributes-only":
			opts.attributesOnly = true
		case "debug":
			opts.debug = true
		case "parents":
			opts.parents = true
		case "no-preserve":
			mask, err := cpParsePreserveMask(inv, occurrence.Value, occurrence.HasValue, true)
			if err != nil {
				return cpOptions{}, err
			}
			opts.preserve &^= mask
		case "backup-short", "backup":
			mode, err := cpBackupModeForOccurrence(inv, occurrence)
			if err != nil {
				return cpOptions{}, err
			}
			opts.backupMode = mode
		case "suffix":
			opts.backupSuffix = determineBackupSuffix(inv, matches)
			if opts.backupMode == backupNone {
				opts.backupMode = backupExisting
			}
		case "update":
			updateMode := cpParseUpdateModeValue(occurrence.Value, occurrence.HasValue)
			if updateMode == cpUpdateInvalid {
				if opts.updateMode != cpUpdateInvalid {
					opts.updateArg = occurrence.Value
					opts.updateMode = cpUpdateInvalid
				}
				continue
			}
			if opts.updateMode != cpUpdateInvalid {
				opts.updateArg = occurrence.Value
				opts.updateMode = updateMode
			}
		case "copy-contents":
			opts.copyContents = true
		case "reflink":
			// Accepted for forward GNU compatibility; semantics will be filled in incrementally.
		}
	}
	return opts, nil
}

func cpAddPreserve(opts *cpOptions, mask cpPreserveAttr) {
	if opts == nil {
		return
	}
	opts.preserve |= mask
}

func cpParsePreserveMask(inv *Invocation, value string, hasValue, noPreserve bool) (cpPreserveAttr, error) {
	raw := strings.TrimSpace(value)
	if !hasValue || raw == "" {
		if noPreserve {
			return 0, commandUsageError(inv, "cp", "option '--no-preserve' requires an argument")
		}
		return cpDefaultPreserveMask, nil
	}
	var mask cpPreserveAttr
	for item := range strings.SplitSeq(raw, ",") {
		switch strings.TrimSpace(item) {
		case "mode":
			mask |= cpPreserveMode
		case "ownership":
			mask |= cpPreserveOwnership
		case "timestamps":
			mask |= cpPreserveTimestamps
		case "links":
			mask |= cpPreserveLinks
		case "all":
			mask |= cpArchivePreserveMask
		default:
			flag := "--preserve"
			if noPreserve {
				flag = "--no-preserve"
			}
			return 0, commandUsageError(inv, "cp", "invalid argument %q for '%s'", strings.TrimSpace(item), flag)
		}
	}
	return mask, nil
}

func cpBackupModeForOccurrence(inv *Invocation, occurrence ParsedOptionOccurrence) (backupMode, error) {
	if occurrence.Name == "backup-short" {
		if value := strings.TrimSpace(backupEnvValue(inv, "VERSION_CONTROL")); value != "" {
			return matchBackupMode("cp", value, "$VERSION_CONTROL")
		}
		return backupExisting, nil
	}
	if occurrence.HasValue {
		return matchBackupMode("cp", strings.TrimSpace(occurrence.Value), "backup type")
	}
	if value := strings.TrimSpace(backupEnvValue(inv, "VERSION_CONTROL")); value != "" {
		return matchBackupMode("cp", value, "$VERSION_CONTROL")
	}
	return backupExisting, nil
}

func cpParseUpdateMode(update cpParsedUpdate) cpUpdateMode {
	return cpParseUpdateModeValue(update.value, update.hasExplicitValue)
}

func cpParseUpdateModeValue(value string, hasExplicitValue bool) cpUpdateMode {
	if !hasExplicitValue {
		return cpUpdateOlder
	}
	switch value {
	case "older":
		return cpUpdateOlder
	case "all":
		return cpUpdateAll
	case "none":
		return cpUpdateNone
	case "none-fail":
		return cpUpdateNoneFail
	default:
		return cpUpdateInvalid
	}
}

func validateCPOptions(inv *Invocation, opts *cpOptions) error {
	if opts == nil {
		return nil
	}
	if opts.updateMode == cpUpdateInvalid {
		return commandUsageError(inv, "cp", "invalid argument %q for '--update'", opts.updateArg)
	}
	if opts.hardLinkMode && opts.symbolicLinkMode {
		return commandUsageError(inv, "cp", "cannot make both hard and symbolic links")
	}
	if opts.backupMode != backupNone {
		switch {
		case opts.noClobber:
			return exitf(inv, 1, "cp: options --backup and --no-clobber are mutually exclusive")
		case opts.updateMode == cpUpdateNone:
			return exitf(inv, 1, "cp: options --backup and --update=none are mutually exclusive")
		case opts.updateMode == cpUpdateNoneFail:
			return exitf(inv, 1, "cp: options --backup and --update=none-fail are mutually exclusive")
		}
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

func resolveCPDestination(ctx context.Context, inv *Invocation, opts *cpOptions, sourceArg, destArg string, multipleSources bool) (destAbs, destDisplay string, destInfo stdfs.FileInfo, destExists bool, err error) {
	destSuffix := cpDestinationSuffix(sourceArg, opts)
	if opts != nil && opts.noTargetDirectory {
		if opts.parents {
			return "", "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		info, abs, exists, err := lstatMaybe(ctx, inv, destArg)
		if err != nil {
			return "", "", nil, false, err
		}
		if multipleSources {
			return "", "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		if strings.HasSuffix(destArg, "/") {
			return "", "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		return abs, destArg, info, exists, nil
	}

	destInfo, destAbs, destExists, err = lstatMaybe(ctx, inv, destArg)
	if err != nil {
		return "", "", nil, false, err
	}
	destIsDir := false
	if destExists {
		if destInfo.IsDir() { //nolint:nilaway // destInfo is non-nil when destExists is true
			destIsDir = true
		} else if destInfo.Mode()&stdfs.ModeSymlink != 0 {
			if resolvedInfo, statErr := cpStatQuiet(ctx, inv, destArg); statErr == nil {
				destIsDir = resolvedInfo.IsDir()
			} else if errors.Is(statErr, stdfs.ErrPermission) {
				if multipleSources || (opts != nil && (opts.targetDirectory != "" || opts.parents)) {
					return "", "", nil, false, exitf(inv, 1, "cp: target directory %s: Permission denied", quoteGNUOperand(destArg))
				}
			}
		}
	}

	if opts != nil && opts.parents {
		if !destExists || !destIsDir {
			return "", "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		return path.Join(destAbs, destSuffix), cpJoinDisplayPath(destArg, destSuffix), destInfo, true, nil
	}
	if multipleSources {
		if !destExists || !destIsDir {
			return "", "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
		}
		return path.Join(destAbs, destSuffix), cpJoinDisplayPath(destArg, destSuffix), destInfo, true, nil
	}
	if destExists && destIsDir {
		return path.Join(destAbs, destSuffix), cpJoinDisplayPath(destArg, destSuffix), destInfo, true, nil
	}
	if strings.HasSuffix(destArg, "/") {
		return "", "", nil, false, exitf(inv, 1, "cp: target %q is not a directory", destArg)
	}
	return destAbs, destArg, destInfo, destExists, nil
}

func cpSourceBase(source string) string {
	trimmed := strings.TrimRight(source, "/")
	if trimmed == "" {
		return path.Base(source)
	}
	return path.Base(trimmed)
}

func cpDestinationSuffix(source string, opts *cpOptions) string {
	if opts != nil && opts.parents {
		return cpParentsPath(source)
	}
	return cpSourceBase(source)
}

func cpParentsPath(source string) string {
	trimmed := strings.TrimRight(source, "/")
	if trimmed == "" {
		trimmed = source
	}
	cleaned := path.Clean(trimmed)
	cleaned = strings.TrimPrefix(cleaned, "/")
	for strings.HasPrefix(cleaned, "../") {
		cleaned = strings.TrimPrefix(cleaned, "../")
	}
	if cleaned == "" || cleaned == "." {
		return cpSourceBase(source)
	}
	return cleaned
}

func cpJoinDisplayPath(root, child string) string {
	if child == "" || child == "." {
		return root
	}
	if root == "" {
		return child
	}
	if root == "/" {
		return "/" + child
	}
	return path.Join(root, child)
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
		deref = !opts.recursive || opts.copyMode == cpCopyHardLink
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

func cpCopyResolvedSource(ctx context.Context, inv *Invocation, source, srcAbs string, srcInfo, srcLinkInfo stdfs.FileInfo, destAbs, destDisplay string, destInfo stdfs.FileInfo, destExists bool, opts *cpOptions, state *cpRunState) (err error) {
	createdParents, err := cpEnsureParentHierarchy(ctx, inv, source, destAbs, opts)
	if err != nil {
		return err
	}
	defer func() {
		finalizeErr := cpFinalizeCreatedParentHierarchy(ctx, inv, createdParents)
		if err == nil {
			err = finalizeErr
		}
	}()

	if srcInfo.IsDir() {
		return cpCopyDirectory(ctx, inv, source, srcAbs, srcInfo, destAbs, destDisplay, destInfo, destExists, opts, state)
	}

	destIsSymlink := destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0
	sameFile := cpSameFile(ctx, inv, srcAbs, srcInfo, destAbs, destInfo, destExists)
	_, preservedLinkCandidate := cpPreservedLinkDestination(state, opts, srcAbs, srcInfo, srcLinkInfo, destAbs)

	skip, err := cpShouldSkipExisting(ctx, inv, srcInfo, srcLinkInfo != nil, destAbs, destDisplay, destInfo, destExists, sameFile, preservedLinkCandidate, opts)
	if err != nil {
		return err
	}
	if skip {
		if opts != nil && opts.debug {
			return cpWriteCopyResult(inv, source, destDisplay, opts, "skipped")
		}
		return nil
	}

	if sameFile && (opts == nil || opts.backupMode == backupNone) {
		bypassSameFile := false
		if opts != nil {
			switch opts.copyMode {
			case cpCopyHardLink:
				if !destIsSymlink {
					return nil
				}
				bypassSameFile = true
			case cpCopySymbolicLink:
				if destIsSymlink {
					bypassSameFile = true
				}
			default:
				return exitf(inv, 1, "cp: %s and %s are the same file", quoteGNUOperand(source), quoteGNUOperand(path.Base(destDisplay)))
			}
		}
		if !bypassSameFile {
			return exitf(inv, 1, "cp: %s and %s are the same file", quoteGNUOperand(source), quoteGNUOperand(path.Base(destDisplay)))
		}
	}

	if opts != nil && opts.interactive == cpInteractiveAlways && destExists {
		ok, err := cpPromptOverwrite(ctx, inv, destDisplay, state)
		if err != nil {
			return err
		}
		if !ok {
			if state != nil {
				state.interactiveSkipped = true
			}
			return nil
		}
	}

	if linked, linkErr := cpTryPreservedLink(ctx, inv, source, srcAbs, srcInfo, srcLinkInfo, destAbs, destDisplay, destInfo, destExists, opts, state); linkErr != nil {
		return linkErr
	} else if linked {
		return cpWriteCopyResult(inv, source, destDisplay, opts, "")
	}

	copySourceAbs, destInfo, destExists, err := prepareCPDestination(ctx, inv, source, srcAbs, srcInfo, srcLinkInfo != nil, destAbs, destDisplay, destInfo, destExists, sameFile, opts, state)
	if err != nil {
		return err
	}
	writeTargetAbs := destAbs
	if srcLinkInfo == nil {
		if redirected, ok, err := cpPOSIXWriteTarget(ctx, inv, destAbs, destInfo, opts); err != nil {
			return err
		} else if ok {
			writeTargetAbs = redirected
			destExists = false
			destInfo = nil
		}
	}

	switch opts.copyMode {
	case cpCopySymbolicLink:
		if err := cpCreateSymbolicLink(ctx, inv, source, destAbs, destDisplay, opts); err != nil {
			return err
		}
	case cpCopyHardLink:
		linkSource := copySourceAbs
		if srcLinkInfo != nil {
			linkSource = srcAbs
		}
		if err := cpCreateHardLink(ctx, inv, source, linkSource, destAbs, destDisplay, opts); err != nil {
			return err
		}
	default:
		switch {
		case srcLinkInfo != nil:
			if opts != nil && opts.attributesOnly && destExists && !opts.removeDestination {
				return exitf(inv, 1, "cp: cannot create symbolic link %s: File exists", quoteGNUOperand(path.Base(destDisplay)))
			}
			if err := copySymlink(ctx, inv, copySourceAbs, destAbs, destDisplay, state); err != nil {
				return err
			}
		case srcInfo.Mode()&stdfs.ModeNamedPipe != 0:
			if opts != nil && opts.attributesOnly {
				if err := cpEnsureSpecialDestination(inv, destDisplay, destInfo, destExists); err != nil {
					return err
				}
			} else if cpShouldPreserveNamedPipe(opts) {
				if err := cpCopyNamedPipe(ctx, inv, destAbs, destDisplay, srcInfo, destInfo, destExists); err != nil {
					return err
				}
			} else if err := copyFileContents(ctx, inv, copySourceAbs, writeTargetAbs, cpCreateFileMode(inv, srcInfo)); err != nil {
				return err
			}
		default:
			if opts != nil && opts.attributesOnly {
				if err := cpEnsureRegularDestination(ctx, inv, destAbs, destDisplay, srcInfo, destInfo, destExists); err != nil {
					return err
				}
			} else if err := copyFileContents(ctx, inv, copySourceAbs, writeTargetAbs, cpCreateFileMode(inv, srcInfo)); err != nil {
				return err
			}
		}
	}

	metadataDestAbs := destAbs
	if writeTargetAbs != destAbs {
		metadataDestAbs = writeTargetAbs
	}
	if err := cpApplyMetadata(ctx, inv, srcInfo, srcLinkInfo, metadataDestAbs, false, !destExists, opts); err != nil {
		return err
	}
	cpRememberPreservedLink(state, opts, srcAbs, srcInfo, srcLinkInfo, destAbs, true)

	debugKind := ""
	if opts != nil && opts.attributesOnly {
		debugKind = "attributes-only"
	}
	return cpWriteCopyResult(inv, source, destDisplay, opts, debugKind)
}

func cpCopyDirectory(ctx context.Context, inv *Invocation, source, srcAbs string, srcInfo stdfs.FileInfo, destAbs, destDisplay string, destInfo stdfs.FileInfo, destExists bool, opts *cpOptions, state *cpRunState) (err error) {
	if opts == nil || !opts.recursive {
		return exitf(inv, 1, "cp: omitting directory %q", source)
	}
	if destAbs == srcAbs || strings.HasPrefix(destAbs, srcAbs+"/") {
		return exitf(inv, 1, "cp: cannot copy a directory, %s, into itself, %s", quoteGNUOperand(source), quoteGNUOperand(destDisplay))
	}

	created, err := cpPrepareDirectoryDestination(ctx, inv, source, destAbs, destDisplay, destInfo, destExists)
	if err != nil {
		return err
	}
	defer func() {
		metaErr := cpApplyMetadata(ctx, inv, srcInfo, nil, destAbs, true, created, opts)
		if err == nil {
			err = metaErr
		}
	}()

	entries, err := readDir(ctx, inv, srcAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childSource := joinChildPath(source, entry.Name())
		childInfo, childAbs, childLinkInfo, err := resolveCPSource(ctx, inv, childSource, opts, false)
		if err != nil {
			return exitf(inv, 1, "cp: cannot stat %q: %s", childSource, cpErrorText(err))
		}
		childDestAbs := joinChildPath(destAbs, entry.Name())
		childDestDisplay := joinChildPath(destDisplay, entry.Name())
		childDestInfo, _, childDestExists, err := lstatMaybe(ctx, inv, childDestAbs)
		if err != nil {
			return err
		}
		if err := cpCopyResolvedSource(ctx, inv, childSource, childAbs, childInfo, childLinkInfo, childDestAbs, childDestDisplay, childDestInfo, childDestExists, opts, state); err != nil {
			return err
		}
	}
	return nil
}

func cpPrepareDirectoryDestination(ctx context.Context, inv *Invocation, source, destAbs, destDisplay string, destInfo stdfs.FileInfo, destExists bool) (bool, error) {
	if destExists {
		effectiveDestInfo := destInfo
		if destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0 {
			resolvedInfo, err := cpStatQuiet(ctx, inv, destAbs)
			if err != nil {
				return false, exitf(inv, 1, "cp: cannot overwrite non-directory %q with directory %q", destDisplay, source)
			}
			effectiveDestInfo = resolvedInfo
		}
		if effectiveDestInfo != nil && !effectiveDestInfo.IsDir() {
			return false, exitf(inv, 1, "cp: cannot overwrite non-directory %q with directory %q", destDisplay, source)
		}
		return false, nil
	}

	if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
		return false, err
	}
	if err := inv.FS.MkdirAll(ctx, destAbs, cpTemporaryDirMode()); err != nil {
		return false, &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.TraceRecorder(), "copy", destAbs, source, destAbs)
	return true, nil
}

func cpSameFile(ctx context.Context, inv *Invocation, srcAbs string, srcInfo stdfs.FileInfo, destAbs string, destInfo stdfs.FileInfo, destExists bool) bool {
	if !destExists {
		return false
	}
	if srcAbs == destAbs {
		return true
	}
	if srcInfo != nil && destInfo != nil && testSameFile(srcInfo, destInfo) {
		return true
	}
	if srcInfo != nil && destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0 {
		if effectiveDestInfo, err := cpStatQuiet(ctx, inv, destAbs); err == nil && testSameFile(srcInfo, effectiveDestInfo) {
			return true
		}
	}
	return false
}

func prepareCPDestination(ctx context.Context, inv *Invocation, source, srcAbs string, srcInfo stdfs.FileInfo, copyingSymlink bool, destAbs, destDisplay string, destInfo stdfs.FileInfo, destExists, sameFile bool, opts *cpOptions, state *cpRunState) (copySourceAbs string, updatedDestInfo stdfs.FileInfo, updatedDestExists bool, err error) {
	copySourceAbs = srcAbs
	if !destExists {
		return copySourceAbs, destInfo, false, nil
	}
	if !copyingSymlink && state != nil {
		if symlinkDisplay, ok := state.justCreatedSymlink[destAbs]; ok {
			return "", nil, false, exitf(inv, 1, "cp: will not copy %s through just-created symlink %s", quoteGNUOperand(source), quoteGNUOperand(symlinkDisplay))
		}
	}
	if opts != nil && opts.backupMode != backupNone {
		backupAbs, err := backupPath(ctx, inv, opts.backupMode, destAbs, opts.backupSuffix)
		if err != nil {
			return "", nil, false, err
		}
		if backupAbs == srcAbs {
			return "", nil, false, exitf(inv, 1, "cp: backing up %s might destroy source;  %s not copied", quoteGNUOperand(destDisplay), quoteGNUOperand(source))
		}
		if backupInfo, _, backupExists, err := lstatMaybe(ctx, inv, backupAbs); err != nil {
			return "", nil, false, err
		} else if backupExists {
			if err := inv.FS.Remove(ctx, backupAbs, backupInfo.IsDir()); err != nil {
				return "", nil, false, &ExitError{Code: 1, Err: err}
			}
		}
		if err := inv.FS.Rename(ctx, destAbs, backupAbs); err != nil {
			return "", nil, false, &ExitError{Code: 1, Err: err}
		}
		delete(state.justCreatedSymlink, destAbs)
		if sameFile {
			copySourceAbs = backupAbs
		}
		return copySourceAbs, nil, false, nil
	}
	if opts != nil && opts.removeDestination && (destInfo == nil || !destInfo.IsDir()) {
		if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return "", nil, false, &ExitError{Code: 1, Err: err}
		}
		delete(state.justCreatedSymlink, destAbs)
		return copySourceAbs, nil, false, nil
	}
	if destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0 {
		if !copyingSymlink {
			if _, err := cpStatQuiet(ctx, inv, destAbs); err != nil {
				switch {
				case errors.Is(err, stdfs.ErrPermission):
					return "", nil, false, exitf(inv, 1, "cp: cannot stat %s: Permission denied", quoteGNUOperand(path.Base(destDisplay)))
				case cpIsSymlinkLoop(err):
					if opts != nil && opts.force {
						if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
							return "", nil, false, &ExitError{Code: 1, Err: err}
						}
						delete(state.justCreatedSymlink, destAbs)
						return copySourceAbs, nil, false, nil
					}
				case cpPosixlyCorrect(inv):
					return copySourceAbs, destInfo, true, nil
				}
				return "", nil, false, exitf(inv, 1, "cp: not writing through dangling symlink %s", quoteGNUOperand(path.Base(destDisplay)))
			}
		}
	}
	_ = srcInfo
	return copySourceAbs, destInfo, true, nil
}

func cpPosixlyCorrect(inv *Invocation) bool {
	if inv == nil || inv.Env == nil {
		return false
	}
	_, ok := inv.Env["POSIXLY_CORRECT"]
	return ok
}

func cpValidateSkippedDestination(ctx context.Context, inv *Invocation, copyingSymlink bool, destAbs, destDisplay string, destInfo stdfs.FileInfo) error {
	if copyingSymlink || destInfo == nil || destInfo.Mode()&stdfs.ModeSymlink == 0 {
		return nil
	}
	if _, err := cpStatQuiet(ctx, inv, destAbs); err != nil {
		switch {
		case errors.Is(err, stdfs.ErrPermission):
			return exitf(inv, 1, "cp: cannot stat %s: Permission denied", quoteGNUOperand(path.Base(destDisplay)))
		case cpPosixlyCorrect(inv):
			return nil
		}
		return exitf(inv, 1, "cp: not writing through dangling symlink %s", quoteGNUOperand(path.Base(destDisplay)))
	}
	return nil
}

func cpShouldSkipExisting(ctx context.Context, inv *Invocation, srcInfo stdfs.FileInfo, copyingSymlink bool, destAbs, destDisplay string, destInfo stdfs.FileInfo, destExists, sameFile, preservedLinkCandidate bool, opts *cpOptions) (bool, error) {
	if !destExists {
		return false, nil
	}
	if opts == nil {
		return false, nil
	}
	if opts.noClobber {
		if err := cpValidateSkippedDestination(ctx, inv, copyingSymlink, destAbs, destDisplay, destInfo); err != nil {
			return false, err
		}
		return true, nil
	}

	switch opts.updateMode {
	case cpUpdateAll:
		return false, nil
	case cpUpdateNone:
		if err := cpValidateSkippedDestination(ctx, inv, copyingSymlink, destAbs, destDisplay, destInfo); err != nil {
			return false, err
		}
		return true, nil
	case cpUpdateNoneFail:
		return false, exitf(inv, 1, "cp: not replacing %s", quoteGNUOperand(destDisplay))
	case cpUpdateOlder:
		if preservedLinkCandidate {
			return false, nil
		}
		if sameFile {
			return false, nil
		}
		effectiveDestInfo := destInfo
		if !copyingSymlink && destInfo != nil && destInfo.Mode()&stdfs.ModeSymlink != 0 {
			if resolvedInfo, err := cpStatQuiet(ctx, inv, destAbs); err == nil {
				effectiveDestInfo = resolvedInfo
			} else {
				return false, nil
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

func copySymlink(ctx context.Context, inv *Invocation, srcAbs, dstAbs, dstDisplay string, state *cpRunState) error {
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
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", dstDisplay)
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
	if state != nil {
		state.justCreatedSymlink[dstAbs] = dstDisplay
	}
	return nil
}

func cpCreateSymbolicLink(ctx context.Context, inv *Invocation, target, dstAbs, dstDisplay string, opts *cpOptions) error {
	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	linkTarget := target
	if !path.IsAbs(target) {
		linkPath := dstAbs
		if resolvedParent, err := inv.FS.Realpath(ctx, path.Dir(dstAbs)); err == nil {
			linkPath = path.Join(resolvedParent, path.Base(dstAbs))
		}
		linkTarget = lnRelativeTarget(inv, target, linkPath)
	}
	if info, _, exists, err := lstatMaybe(ctx, inv, dstAbs); err != nil {
		return err
	} else if exists {
		if info.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", dstDisplay)
		}
		if opts == nil || (!opts.force && !opts.removeDestination) {
			return exitf(inv, 1, "cp: cannot create symbolic link %s: File exists", quoteGNUOperand(path.Base(dstDisplay)))
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

func cpCreateHardLink(ctx context.Context, inv *Invocation, source, srcAbs, dstAbs, dstDisplay string, opts *cpOptions) error {
	if err := ensureParentDirExists(ctx, inv, dstAbs); err != nil {
		return err
	}
	if info, _, exists, err := lstatMaybe(ctx, inv, dstAbs); err != nil {
		return err
	} else if exists {
		if info.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", dstDisplay)
		}
		if opts == nil || (!opts.force && !opts.removeDestination) {
			return exitf(inv, 1, "cp: cannot create hard link %s to %s: File exists", quoteGNUOperand(path.Base(dstDisplay)), quoteGNUOperand(source))
		}
		if err := inv.FS.Remove(ctx, dstAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := inv.FS.Link(ctx, srcAbs, dstAbs); err != nil {
		return exitf(inv, 1, "cp: cannot create hard link %s to %s: %s", quoteGNUOperand(path.Base(dstDisplay)), quoteGNUOperand(source), lnErrText(err))
	}
	recordFileMutation(inv.TraceRecorder(), "copy", dstAbs, srcAbs, dstAbs)
	return nil
}

type cpCreatedParentDir struct {
	abs  string
	mode stdfs.FileMode
}

type cpParentHierarchyEntry struct {
	sourcePrefix string
	destPrefix   string
}

func cpEnsureParentHierarchy(ctx context.Context, inv *Invocation, source, destAbs string, opts *cpOptions) ([]cpCreatedParentDir, error) {
	if opts == nil || !opts.parents {
		return nil, nil
	}
	hierarchy := cpParentHierarchy(source)
	if len(hierarchy) == 0 {
		return nil, nil
	}
	suffix := cpParentsPath(source)
	rootDest := strings.TrimSuffix(destAbs, "/"+suffix)
	if rootDest == "" {
		rootDest = "/"
	}
	if rootDest == destAbs {
		return nil, nil
	}

	created := make([]cpCreatedParentDir, 0, len(hierarchy))
	for _, entry := range hierarchy {
		currentDest := joinChildPath(rootDest, entry.destPrefix)
		info, _, exists, err := lstatMaybe(ctx, inv, currentDest)
		if err != nil {
			return nil, err
		}
		if exists {
			if info == nil || !info.IsDir() {
				return nil, exitf(inv, 1, "cp: cannot create directory %q: File exists", currentDest)
			}
			continue
		}
		sourceDirInfo, _, err := statPath(ctx, inv, entry.sourcePrefix)
		if err != nil {
			return nil, exitf(inv, 1, "cp: cannot stat %q: %s", entry.sourcePrefix, cpErrorText(err))
		}
		if err := inv.FS.MkdirAll(ctx, currentDest, cpTemporaryDirMode()); err != nil {
			return nil, &ExitError{Code: 1, Err: err}
		}
		recordFileMutation(inv.TraceRecorder(), "copy", currentDest, entry.sourcePrefix, currentDest)
		created = append(created, cpCreatedParentDir{
			abs:  currentDest,
			mode: cpDesiredDirectoryMode(inv, sourceDirInfo, opts),
		})
	}
	return created, nil
}

func cpFinalizeCreatedParentHierarchy(ctx context.Context, inv *Invocation, created []cpCreatedParentDir) error {
	for i := len(created) - 1; i >= 0; i-- {
		if err := inv.FS.Chmod(ctx, created[i].abs, created[i].mode); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func cpParentSourcePrefixes(source string) []string {
	trimmed := strings.TrimRight(source, "/")
	if trimmed == "" {
		return nil
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "/" {
		return nil
	}
	absolute := strings.HasPrefix(cleaned, "/")
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	if len(parts) <= 1 {
		return nil
	}
	prefixes := make([]string, 0, len(parts)-1)
	current := ""
	if absolute {
		current = "/"
	}
	for i := 0; i < len(parts)-1; i++ {
		switch current {
		case "/":
			current = "/" + parts[i]
		case "":
			current = parts[i]
		default:
			current = current + "/" + parts[i]
		}
		prefixes = append(prefixes, current)
	}
	return prefixes
}

func cpParentHierarchy(source string) []cpParentHierarchyEntry {
	sourcePrefixes := cpParentSourcePrefixes(source)
	if len(sourcePrefixes) == 0 {
		return nil
	}
	sanitized := cpParentsPath(source)
	parts := strings.Split(sanitized, "/")
	if len(parts) <= 1 {
		return nil
	}
	entries := make([]cpParentHierarchyEntry, 0, len(parts)-1)
	for i := 0; i < len(parts)-1 && i < len(sourcePrefixes); i++ {
		destPrefix := path.Join(parts[:i+1]...)
		entries = append(entries, cpParentHierarchyEntry{
			sourcePrefix: sourcePrefixes[len(sourcePrefixes)-len(parts)+1+i],
			destPrefix:   destPrefix,
		})
	}
	return entries
}

func cpTemporaryDirMode() stdfs.FileMode {
	return 0o700
}

func cpCreateFileMode(inv *Invocation, srcInfo stdfs.FileInfo) stdfs.FileMode {
	if srcInfo != nil {
		return srcInfo.Mode().Perm() &^ stdfs.FileMode(umaskValue(inv))
	}
	return stdfs.FileMode(uint32(0o666) &^ umaskValue(inv))
}

func cpCreateDirMode(inv *Invocation) stdfs.FileMode {
	return stdfs.FileMode(uint32(0o777) &^ umaskValue(inv))
}

func cpDesiredDirectoryMode(inv *Invocation, srcInfo stdfs.FileInfo, opts *cpOptions) stdfs.FileMode {
	if opts != nil && opts.preserve&cpPreserveMode != 0 && srcInfo != nil {
		return srcInfo.Mode()
	}
	return cpCreateDirMode(inv)
}

func cpEnsureRegularDestination(ctx context.Context, inv *Invocation, destAbs, destDisplay string, srcInfo, destInfo stdfs.FileInfo, destExists bool) error {
	if destExists {
		if destInfo != nil && destInfo.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", destDisplay)
		}
		return nil
	}
	if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
		return err
	}
	file, err := inv.FS.OpenFile(ctx, destAbs, os.O_CREATE|os.O_WRONLY|os.O_EXCL, cpCreateFileMode(inv, srcInfo))
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if closeErr := file.Close(); closeErr != nil {
		return &ExitError{Code: 1, Err: closeErr}
	}
	recordFileMutation(inv.TraceRecorder(), "copy", destAbs, destAbs, destAbs)
	return nil
}

func cpEnsureSpecialDestination(inv *Invocation, destDisplay string, destInfo stdfs.FileInfo, destExists bool) error {
	if destExists && destInfo != nil && destInfo.IsDir() {
		return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", destDisplay)
	}
	return nil
}

func cpCopyNamedPipe(ctx context.Context, inv *Invocation, destAbs, destDisplay string, srcInfo, destInfo stdfs.FileInfo, destExists bool) error {
	if destExists {
		if destInfo != nil && destInfo.IsDir() {
			return exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", destDisplay)
		}
		if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
		return err
	}
	if err := inv.FS.Mkfifo(ctx, destAbs, cpCreateFileMode(inv, srcInfo)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.TraceRecorder(), "copy", destAbs, destAbs, destAbs)
	return nil
}

func cpShouldPreserveNamedPipe(opts *cpOptions) bool {
	if opts == nil {
		return false
	}
	return opts.recursive && !opts.copyContents
}

func cpApplyMetadata(ctx context.Context, inv *Invocation, srcInfo, srcLinkInfo stdfs.FileInfo, destAbs string, isDir, created bool, opts *cpOptions) error {
	if opts == nil {
		return nil
	}
	if srcLinkInfo != nil {
		if opts.preserve&cpPreserveOwnership != 0 {
			if ownership, ok := gbfs.OwnershipFromFileInfo(srcInfo); ok {
				if err := inv.FS.Chown(ctx, destAbs, ownership.UID, ownership.GID, false); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
		}
		if opts.preserve&cpPreserveTimestamps != 0 {
			atime, ok := statAccessTime(srcInfo)
			if !ok {
				atime = srcInfo.ModTime()
			}
			if err := inv.FS.Lchtimes(ctx, destAbs, atime.UTC(), srcInfo.ModTime().UTC()); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}
	if opts.preserve&cpPreserveOwnership != 0 {
		if ownership, ok := gbfs.OwnershipFromFileInfo(srcInfo); ok {
			if err := inv.FS.Chown(ctx, destAbs, ownership.UID, ownership.GID, true); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}
	if opts.preserve&cpPreserveMode != 0 {
		if err := inv.FS.Chmod(ctx, destAbs, srcInfo.Mode()); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	} else if isDir && created {
		dirMode := cpCreateDirMode(inv)
		if srcInfo != nil {
			dirMode = cpCreateFileMode(inv, srcInfo)
		}
		if err := inv.FS.Chmod(ctx, destAbs, dirMode); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if opts.preserve&cpPreserveTimestamps != 0 {
		atime, ok := statAccessTime(srcInfo)
		if !ok {
			atime = srcInfo.ModTime()
		}
		if err := inv.FS.Chtimes(ctx, destAbs, atime.UTC(), srcInfo.ModTime().UTC()); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func cpPOSIXWriteTarget(ctx context.Context, inv *Invocation, destAbs string, destInfo stdfs.FileInfo, opts *cpOptions) (string, bool, error) {
	if opts == nil || !cpPosixlyCorrect(inv) || destInfo == nil || destInfo.Mode()&stdfs.ModeSymlink == 0 {
		return "", false, nil
	}
	if _, err := cpStatQuiet(ctx, inv, destAbs); err == nil || errors.Is(err, stdfs.ErrPermission) {
		return "", false, nil
	}
	target, err := inv.FS.Readlink(ctx, destAbs)
	if err != nil {
		return "", false, &ExitError{Code: 1, Err: err}
	}
	if path.IsAbs(target) {
		return target, true, nil
	}
	return path.Clean(path.Join(path.Dir(destAbs), target)), true, nil
}

func cpTryPreservedLink(ctx context.Context, inv *Invocation, source, srcAbs string, srcInfo, srcLinkInfo stdfs.FileInfo, destAbs, destDisplay string, destInfo stdfs.FileInfo, destExists bool, opts *cpOptions, state *cpRunState) (bool, error) {
	linkedDest, ok := cpPreservedLinkDestination(state, opts, srcAbs, srcInfo, srcLinkInfo, destAbs)
	if !ok {
		return false, nil
	}
	linkedInfo, _, linkedExists, err := lstatMaybe(ctx, inv, linkedDest)
	if err != nil {
		return false, err
	}
	if !linkedExists {
		return false, nil
	}
	if destExists && cpSameFile(ctx, inv, linkedDest, linkedInfo, destAbs, destInfo, true) {
		return true, nil
	}
	if destExists {
		if destInfo != nil && destInfo.IsDir() {
			return false, exitf(inv, 1, "cp: cannot overwrite directory %q with non-directory", destDisplay)
		}
		if err := inv.FS.Remove(ctx, destAbs, true); err != nil && !errors.Is(err, stdfs.ErrNotExist) {
			return false, &ExitError{Code: 1, Err: err}
		}
	}
	if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
		return false, err
	}
	if err := inv.FS.Link(ctx, linkedDest, destAbs); err != nil {
		return false, exitf(inv, 1, "cp: cannot create hard link %s to %s: %s", quoteGNUOperand(path.Base(destDisplay)), quoteGNUOperand(source), lnErrText(err))
	}
	recordFileMutation(inv.TraceRecorder(), "copy", destAbs, linkedDest, destAbs)
	return true, nil
}

func cpRememberPreservedLink(state *cpRunState, opts *cpOptions, srcAbs string, srcInfo, srcLinkInfo stdfs.FileInfo, destAbs string, destExists bool) {
	if state == nil || opts == nil || opts.preserve&cpPreserveLinks == 0 || !destExists || srcInfo == nil || srcInfo.IsDir() || srcLinkInfo != nil {
		return
	}
	state.preservedLinks[cpPreservedLinkKey(srcAbs, srcInfo)] = destAbs
}

func cpPreservedLinkDestination(state *cpRunState, opts *cpOptions, srcAbs string, srcInfo, srcLinkInfo stdfs.FileInfo, destAbs string) (string, bool) {
	if state == nil || opts == nil || opts.preserve&cpPreserveLinks == 0 || srcInfo == nil || srcInfo.IsDir() || srcLinkInfo != nil {
		return "", false
	}
	linkedDest, ok := state.preservedLinks[cpPreservedLinkKey(srcAbs, srcInfo)]
	if !ok || linkedDest == "" || linkedDest == destAbs {
		return "", false
	}
	return linkedDest, true
}

func cpPreservedLinkKey(srcAbs string, srcInfo stdfs.FileInfo) string {
	if identity, ok := fileInfoIdentity(srcInfo); ok {
		return fmt.Sprintf("%d:%d", identity.device, identity.inode)
	}
	return srcAbs
}

func cpPromptOverwrite(ctx context.Context, inv *Invocation, destDisplay string, state *cpRunState) (bool, error) {
	reader, err := cpPromptReader(ctx, inv, state)
	if err != nil {
		return false, err
	}
	if inv != nil && inv.Stderr != nil {
		if _, err := fmt.Fprintf(inv.Stderr, "cp: overwrite %s? ", quoteGNUOperand(destDisplay)); err != nil {
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
		return false, exitf(inv, 1, "cp: Failed to read from standard input")
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

func cpPromptReader(ctx context.Context, inv *Invocation, state *cpRunState) (*bufio.Reader, error) {
	if state != nil && state.promptInput != nil && state.promptInput.reader != nil {
		return state.promptInput.reader, nil
	}
	if inv != nil && inv.Stdin != nil {
		reader := bufio.NewReader(inv.Stdin)
		if state != nil && state.promptInput != nil {
			state.promptInput.reader = reader
		}
		return reader, nil
	}
	file, _, err := openRead(ctx, inv, "/dev/tty")
	if err == nil {
		reader := bufio.NewReader(file)
		if state != nil && state.promptInput != nil {
			state.promptInput.reader = reader
			state.promptInput.closer = file
		}
		return reader, nil
	}
	return nil, exitf(inv, 1, "cp: failed to open standard input")
}

func cpClosePromptInput(input *cpPromptInput) error {
	if input == nil || input.closer == nil {
		return nil
	}
	err := input.closer.Close()
	input.closer = nil
	input.reader = nil
	return err
}

func cpWriteCopyResult(inv *Invocation, source, destDisplay string, opts *cpOptions, debugKind string) error {
	if opts == nil || inv == nil || inv.Stdout == nil {
		return nil
	}
	if opts.debug {
		var line string
		switch debugKind {
		case "skipped":
			line = fmt.Sprintf("'%s' -> '%s' (skipped)\n", source, destDisplay)
		case "attributes-only":
			line = fmt.Sprintf("'%s' -> '%s' (attributes only)\n", source, destDisplay)
		default:
			line = fmt.Sprintf("'%s' -> '%s' (copy offload: unavailable; reflink: no; sparse detection: no)\n", source, destDisplay)
		}
		if _, err := io.WriteString(inv.Stdout, line); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}
	if !opts.verbose {
		return nil
	}
	if _, err := fmt.Fprintf(inv.Stdout, "'%s' -> '%s'\n", source, destDisplay); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func cpStatQuiet(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, error) {
	abs := allowPath(inv, name)
	info, err := inv.FS.StatQuiet(ctx, abs)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func cpErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, stdfs.ErrInvalid):
		return "Invalid argument"
	case cpIsSymlinkLoop(err):
		return "Too many levels of symbolic links"
	}
	text := err.Error()
	if idx := strings.LastIndex(text, ": "); idx >= 0 && idx+2 < len(text) {
		return text[idx+2:]
	}
	return text
}

var _ Command = (*CP)(nil)
var _ SpecProvider = (*CP)(nil)
var _ ParsedRunner = (*CP)(nil)
