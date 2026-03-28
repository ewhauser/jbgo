package builtins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"strings"
)

const rgVersionText = "ripgrep 15.1.0 (gbash)\n"

type RG struct{}

type rgOptions struct {
	grep         grepOptions
	globs        []rgGlobRule
	ignoreFiles  []string
	manualIgnore []rgIgnoreRule

	hidden      bool
	noIgnore    bool
	noIgnoreVCS bool
	followLinks bool
	filesMode   bool
}

type rgTarget struct {
	path        string
	displayBase string
}

type rgFileRecord struct {
	abs        string
	display    string
	showName   bool
	explicit   bool
	skipBinary bool
}

func NewRG() *RG {
	return &RG{}
}

func (c *RG) Name() string {
	return "rg"
}

func (c *RG) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *RG) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Recursively search files for a pattern.",
		Usage: "rg [OPTION]... PATTERN [PATH]...",
		Options: []OptionSpec{
			{Name: "regexp", Short: 'e', Long: "regexp", Arity: OptionRequiredValue, ValueName: "PATTERN", Help: "use PATTERN for matching"},
			{Name: "file", Short: 'f', Long: "file", Arity: OptionRequiredValue, ValueName: "FILE", Help: "read patterns from FILE"},
			{Name: "fixed-strings", Short: 'F', Long: "fixed-strings", Help: "treat patterns as literal strings"},
			{Name: "ignore-case", Short: 'i', Long: "ignore-case", Help: "search case-insensitively"},
			{Name: "case-sensitive", Short: 's', Long: "case-sensitive", Help: "search case-sensitively"},
			{Name: "smart-case", Short: 'S', Long: "smart-case", Help: "smart case search"},
			{Name: "invert-match", Short: 'v', Long: "invert-match", Help: "invert matching"},
			{Name: "word-regexp", Short: 'w', Long: "word-regexp", Help: "match whole words"},
			{Name: "line-regexp", Short: 'x', Long: "line-regexp", Help: "match whole lines"},
			{Name: "line-number", Short: 'n', Long: "line-number", Help: "show line numbers"},
			{Name: "no-line-number", Short: 'N', Long: "no-line-number", Help: "suppress line numbers"},
			{Name: "with-filename", Short: 'H', Long: "with-filename", Help: "show file names"},
			{Name: "no-filename", Short: 'I', Long: "no-filename", Help: "suppress file names"},
			{Name: "only-matching", Short: 'o', Long: "only-matching", Help: "show only matching parts"},
			{Name: "quiet", Short: 'q', Long: "quiet", Aliases: []string{"silent"}, HelpAliases: []string{"silent"}, Help: "suppress normal output"},
			{Name: "count", Short: 'c', Long: "count", Help: "show match counts"},
			{Name: "files-with-matches", Short: 'l', Long: "files-with-matches", Help: "show only matching file names"},
			{Name: "files-without-match", Long: "files-without-match", Help: "show only file names without matches"},
			{Name: "after-context", Short: 'A', Long: "after-context", Arity: OptionRequiredValue, ValueName: "NUM", Help: "show NUM lines after each match"},
			{Name: "before-context", Short: 'B', Long: "before-context", Arity: OptionRequiredValue, ValueName: "NUM", Help: "show NUM lines before each match"},
			{Name: "context", Short: 'C', Long: "context", Arity: OptionRequiredValue, ValueName: "NUM", Help: "show NUM lines before and after each match"},
			{Name: "glob", Short: 'g', Long: "glob", Arity: OptionRequiredValue, ValueName: "GLOB", Help: "include or exclude paths matching GLOB"},
			{Name: "iglob", Long: "iglob", Arity: OptionRequiredValue, ValueName: "GLOB", Help: "case-insensitive glob include or exclude"},
			{Name: "hidden", Short: '.', Long: "hidden", Help: "search hidden files and directories"},
			{Name: "no-ignore", Long: "no-ignore", Help: "do not respect .gitignore, .ignore, or .rgignore"},
			{Name: "no-ignore-vcs", Long: "no-ignore-vcs", Help: "do not respect VCS ignore files"},
			{Name: "ignore-file", Long: "ignore-file", Arity: OptionRequiredValue, ValueName: "PATH", Help: "read ignore rules from PATH"},
			{Name: "follow", Short: 'L', Long: "follow", Help: "follow symbolic links"},
			{Name: "files", Long: "files", Help: "print each file that would be searched"},
			{Name: "pcre2", Short: 'P', Long: "pcre2", Hidden: true, Help: "accepted for ripgrep compatibility"},
			{Name: "multiline", Short: 'U', Long: "multiline", Hidden: true, Help: "accepted for ripgrep compatibility"},
			{Name: "json", Long: "json", Hidden: true, Help: "accepted for ripgrep compatibility"},
			{Name: "type", Long: "type", Arity: OptionRequiredValue, ValueName: "TYPE", Hidden: true, Help: "accepted for ripgrep compatibility"},
			{Name: "type-not", Long: "type-not", Arity: OptionRequiredValue, ValueName: "TYPE", Hidden: true, Help: "accepted for ripgrep compatibility"},
			{Name: "type-add", Long: "type-add", Arity: OptionRequiredValue, ValueName: "SPEC", Hidden: true, Help: "accepted for ripgrep compatibility"},
			{Name: "type-clear", Long: "type-clear", Arity: OptionRequiredValue, ValueName: "TYPE", Hidden: true, Help: "accepted for ripgrep compatibility"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		VersionRenderer: renderStaticVersion(rgVersionText),
	}
}

func (c *RG) NormalizeParseError(inv *Invocation, err error) error {
	if err == nil {
		return nil
	}

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		return err
	}

	if inv != nil && inv.Stderr != nil {
		message := strings.TrimSpace(err.Error())
		if message != "" {
			_, _ = io.WriteString(inv.Stderr, message+"\n")
		}
	}
	return &ExitError{Code: 2}
}

func (c *RG) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, paths, err := parseRGMatches(inv, matches)
	if err != nil {
		return err
	}

	if err := rgLoadManualIgnoreFiles(ctx, inv, &opts); err != nil {
		return err
	}

	if opts.filesMode {
		return c.runFiles(ctx, inv, &opts, paths)
	}

	matcher, err := compileGrepPattern(ctx, inv, &opts.grep)
	if err != nil {
		return err
	}

	state := &grepRunState{}
	if len(paths) == 0 && !invInputLooksLikeTTY(inv) {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return err
		}
		if err := writeGrepResult(inv, matcher, data, "", false, &opts.grep, state); err != nil {
			return err
		}
	} else {
		targets := rgBuildTargets(paths, len(paths) == 0)
		defaultShowExplicit := len(paths) > 1
		for _, target := range targets {
			records := make([]rgFileRecord, 0, 16)
			visitedDirs := make(map[string]string)
			if err := c.enumerateTarget(ctx, inv, &opts, state, target, defaultShowExplicit, visitedDirs, &records); err != nil {
				return err
			}
			for _, record := range records {
				if state.quietMatched {
					return nil
				}

				data, _, err := readAllFile(ctx, inv, record.abs)
				if err != nil {
					if grepShouldPropagateError(err) {
						return err
					}
					grepNoteFileError(inv, &opts.grep, state, record.displayOrAbs(), err)
					continue
				}
				if record.skipBinary && rgIsBinaryData(data) {
					continue
				}
				if err := writeGrepResult(inv, matcher, data, record.displayOrAbs(), record.showName, &opts.grep, state); err != nil {
					return err
				}
			}
		}
	}

	if state.quietMatched {
		return nil
	}
	if state.hadError {
		return &ExitError{Code: 2}
	}
	if opts.grep.filesWithoutMatch {
		if state.filesWithoutMatchAny {
			return nil
		}
		return &ExitError{Code: 1}
	}
	if state.matchedAny {
		return nil
	}
	return &ExitError{Code: 1}
}

func parseRGMatches(inv *Invocation, matches *ParsedCommand) (rgOptions, []string, error) {
	opts := rgOptions{
		grep: grepOptions{
			commandName: "rg",
			matchMode:   grepPatternModeERE,
		},
	}

	var caseMode string
	for _, occurrence := range matches.OptionOccurrences() {
		switch occurrence.Name {
		case "regexp":
			opts.grep.patternInputs = append(opts.grep.patternInputs, grepPatternInput{
				kind:  grepPatternInputText,
				value: occurrence.Value,
			})
		case "file":
			opts.grep.patternInputs = append(opts.grep.patternInputs, grepPatternInput{
				kind:  grepPatternInputFile,
				value: occurrence.Value,
			})
		case "fixed-strings":
			opts.grep.matchMode = grepPatternModeFixed
		case "ignore-case":
			caseMode = "ignore"
		case "case-sensitive":
			caseMode = "sensitive"
		case "smart-case":
			caseMode = "smart"
		case "invert-match":
			opts.grep.invert = true
		case "word-regexp":
			opts.grep.wordRegexp = true
		case "line-regexp":
			opts.grep.lineRegexp = true
		case "line-number":
			opts.grep.lineNumber = true
		case "no-line-number":
			opts.grep.lineNumber = false
		case "with-filename":
			opts.grep.filenameMode = grepFilenameWith
		case "no-filename":
			opts.grep.filenameMode = grepFilenameWithout
		case "only-matching":
			opts.grep.onlyMatching = true
		case "quiet":
			opts.grep.quiet = true
		case "count":
			opts.grep.count = true
		case "files-with-matches":
			opts.grep.listFiles = true
		case "files-without-match":
			opts.grep.filesWithoutMatch = true
		case "after-context":
			number, err := parseGrepFlagInt(occurrence.Value)
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid context length %q", occurrence.Value)
			}
			setGrepContext(&opts.grep, "-A", number)
		case "before-context":
			number, err := parseGrepFlagInt(occurrence.Value)
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid context length %q", occurrence.Value)
			}
			setGrepContext(&opts.grep, "-B", number)
		case "context":
			number, err := parseGrepFlagInt(occurrence.Value)
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid context length %q", occurrence.Value)
			}
			setGrepContext(&opts.grep, "-C", number)
		case "glob":
			opts.globs = append(opts.globs, rgParseGlobRule(occurrence.Value, false))
		case "iglob":
			opts.globs = append(opts.globs, rgParseGlobRule(occurrence.Value, true))
		case "hidden":
			opts.hidden = true
		case "no-ignore":
			opts.noIgnore = true
		case "no-ignore-vcs":
			opts.noIgnoreVCS = true
		case "ignore-file":
			opts.ignoreFiles = append(opts.ignoreFiles, occurrence.Value)
		case "follow":
			opts.followLinks = true
		case "files":
			opts.filesMode = true
		case "pcre2":
			return rgOptions{}, nil, exitf(inv, 2, "rg: PCRE2 matching is not supported in this build")
		case "multiline":
			return rgOptions{}, nil, exitf(inv, 2, "rg: multiline mode is not supported in this build")
		case "json":
			return rgOptions{}, nil, exitf(inv, 2, "rg: JSON output is not supported in this build")
		case "type", "type-not", "type-add", "type-clear":
			return rgOptions{}, nil, exitf(inv, 2, "rg: file type filters are not supported in this build")
		}
	}

	switch caseMode {
	case "ignore":
		opts.grep.ignoreCase = true
	case "smart":
		opts.grep.smartCase = true
	}

	args := matches.Args("arg")
	if !opts.filesMode && len(opts.grep.patternInputs) == 0 {
		if len(args) == 0 {
			return rgOptions{}, nil, exitf(inv, 2, "rg: missing pattern")
		}
		opts.grep.patternInputs = append(opts.grep.patternInputs, grepPatternInput{
			kind:  grepPatternInputText,
			value: args[0],
		})
		args = args[1:]
	}

	return opts, args, nil
}

func rgLoadManualIgnoreFiles(ctx context.Context, inv *Invocation, opts *rgOptions) error {
	if opts == nil || len(opts.ignoreFiles) == 0 {
		return nil
	}

	baseDir := inv.Cwd
	if baseDir == "" {
		baseDir = "/"
	}
	for _, name := range opts.ignoreFiles {
		rules, err := rgLoadIgnoreRules(ctx, inv, name, baseDir)
		if err != nil {
			if grepShouldPropagateError(err) {
				return err
			}
			grepPrintFileError(inv, &opts.grep, name, err)
			return &ExitError{Code: 2}
		}
		opts.manualIgnore = append(opts.manualIgnore, rules...)
	}
	return nil
}

func rgBuildTargets(paths []string, implicitCurrentDir bool) []rgTarget {
	if implicitCurrentDir {
		return []rgTarget{{path: ".", displayBase: ""}}
	}

	targets := make([]rgTarget, 0, len(paths))
	for _, raw := range paths {
		display := path.Clean(raw)
		if display == "/" {
			display = "/"
		}
		targets = append(targets, rgTarget{
			path:        raw,
			displayBase: display,
		})
	}
	return targets
}

func (c *RG) runFiles(ctx context.Context, inv *Invocation, opts *rgOptions, paths []string) error {
	targets := rgBuildTargets(paths, len(paths) == 0)
	defaultShowExplicit := len(paths) > 1
	hadAny := false
	state := &grepRunState{}

	for _, target := range targets {
		records := make([]rgFileRecord, 0, 16)
		visitedDirs := make(map[string]string)
		if err := c.enumerateTarget(ctx, inv, opts, state, target, defaultShowExplicit, visitedDirs, &records); err != nil {
			return err
		}
		for _, record := range records {
			hadAny = true
			if opts.grep.quiet {
				return nil
			}
			if _, err := fmt.Fprintln(inv.Stdout, record.displayOrAbs()); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	if state.hadError {
		return &ExitError{Code: 2}
	}
	if hadAny {
		return nil
	}
	return &ExitError{Code: 1}
}

func (c *RG) enumerateTarget(ctx context.Context, inv *Invocation, opts *rgOptions, state *grepRunState, target rgTarget, defaultShowExplicit bool, visitedDirs map[string]string, records *[]rgFileRecord) error {
	linfo, abs, exists, err := lstatMaybe(ctx, inv, target.path)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, &opts.grep, state, target.displayBase, err)
		return nil
	}
	if !exists {
		grepNoteFileError(inv, &opts.grep, state, target.displayBase, stdfs.ErrNotExist)
		return nil
	}

	info := linfo
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, abs, err = rgFollowSymlink(ctx, inv, abs)
		if err != nil {
			if grepShouldPropagateError(err) {
				return err
			}
			grepNoteFileError(inv, &opts.grep, state, target.displayBase, err)
			return nil
		}
	}

	if info.IsDir() {
		return c.walkRecursive(ctx, inv, opts, state, abs, target.displayBase, true, visitedDirs, nil, records)
	}

	*records = append(*records, rgFileRecord{
		abs:        abs,
		display:    target.displayBase,
		showName:   defaultShowExplicit,
		explicit:   true,
		skipBinary: false,
	})
	return nil
}

func (c *RG) walkRecursive(ctx context.Context, inv *Invocation, opts *rgOptions, state *grepRunState, currentAbs, currentDisplay string, explicitRoot bool, visitedDirs map[string]string, parentRules []rgIgnoreRule, records *[]rgFileRecord) error {
	linfo, _, err := lstatPath(ctx, inv, currentAbs)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, &opts.grep, state, currentDisplayOrAbs(currentDisplay, currentAbs), err)
		return nil
	}

	info := linfo
	isSymlink := linfo.Mode()&stdfs.ModeSymlink != 0
	if isSymlink {
		if !explicitRoot && !opts.followLinks {
			return nil
		}
		info, currentAbs, err = rgFollowSymlink(ctx, inv, currentAbs)
		if err != nil {
			if grepShouldPropagateError(err) {
				return err
			}
			grepNoteFileError(inv, &opts.grep, state, currentDisplayOrAbs(currentDisplay, currentAbs), err)
			return nil
		}
	}

	if !info.IsDir() {
		*records = append(*records, rgFileRecord{
			abs:        currentAbs,
			display:    currentDisplay,
			showName:   true,
			explicit:   false,
			skipBinary: !opts.filesMode,
		})
		return nil
	}

	resolvedDir, err := inv.FS.Realpath(ctx, currentAbs)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, &opts.grep, state, currentDisplayOrAbs(currentDisplay, currentAbs), err)
		return nil
	}
	if seenDisplay, seen := visitedDirs[resolvedDir]; seen {
		if !explicitRoot {
			rgNoteLoop(inv, &opts.grep, state, currentDisplayOrAbs(currentDisplay, currentAbs), seenDisplay)
		}
		return nil
	}
	visitedDirs[resolvedDir] = currentDisplayOrAbs(currentDisplay, currentAbs)

	currentRules := parentRules
	if !opts.noIgnore {
		nextRules, err := c.loadAutoIgnoreRules(ctx, inv, opts, currentAbs, currentRules)
		if err != nil {
			if grepShouldPropagateError(err) {
				return err
			}
			grepNoteFileError(inv, &opts.grep, state, currentDisplayOrAbs(currentDisplay, currentAbs), err)
			return nil
		}
		currentRules = nextRules
	}

	entries, err := readDir(ctx, inv, currentAbs)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, &opts.grep, state, currentDisplayOrAbs(currentDisplay, currentAbs), err)
		return nil
	}
	for _, entry := range entries {
		childName := entry.Name()
		if !opts.hidden && strings.HasPrefix(childName, ".") {
			continue
		}

		childAbs := path.Join(currentAbs, childName)
		childDisplay := rgJoinDisplay(currentDisplay, childName)

		childLinfo, _, err := lstatPath(ctx, inv, childAbs)
		if err != nil {
			if grepShouldPropagateError(err) {
				return err
			}
			grepNoteFileError(inv, &opts.grep, state, childDisplay, err)
			continue
		}
		childIsDir := childLinfo.IsDir()
		childIsSymlink := childLinfo.Mode()&stdfs.ModeSymlink != 0
		childResolvedAbs := childAbs
		if childIsSymlink && opts.followLinks {
			info, resolvedAbs, err := rgFollowSymlink(ctx, inv, childAbs)
			if err != nil {
				if grepShouldPropagateError(err) {
					return err
				}
				grepNoteFileError(inv, &opts.grep, state, childDisplay, err)
				continue
			}
			childIsDir = info.IsDir()
			childResolvedAbs = resolvedAbs
		}

		if !opts.noIgnore {
			ignored, err := rgShouldIgnorePath(append(currentRules, opts.manualIgnore...), childAbs, childIsDir)
			if err != nil {
				return exitf(inv, 2, "rg: invalid ignore pattern: %v", err)
			}
			if ignored {
				continue
			}
		} else {
			ignored, err := rgShouldIgnorePath(opts.manualIgnore, childAbs, childIsDir)
			if err != nil {
				return exitf(inv, 2, "rg: invalid ignore pattern: %v", err)
			}
			if ignored {
				continue
			}
		}

		allowed, err := rgGlobAllows(opts.globs, childDisplay)
		if err != nil {
			return exitf(inv, 2, "rg: invalid glob: %v", err)
		}
		if !allowed {
			continue
		}

		if childIsDir {
			if err := c.walkRecursive(ctx, inv, opts, state, childResolvedAbs, childDisplay, false, visitedDirs, currentRules, records); err != nil {
				return err
			}
			continue
		}

		if childIsSymlink && !opts.followLinks {
			continue
		}
		*records = append(*records, rgFileRecord{
			abs:        childResolvedAbs,
			display:    childDisplay,
			showName:   true,
			explicit:   false,
			skipBinary: !opts.filesMode,
		})
	}
	return nil
}

func (c *RG) loadAutoIgnoreRules(ctx context.Context, inv *Invocation, opts *rgOptions, currentAbs string, inherited []rgIgnoreRule) ([]rgIgnoreRule, error) {
	rules := append([]rgIgnoreRule(nil), inherited...)
	baseDir := currentAbs

	inGitRepo := rgHasGitBoundary(ctx, inv, currentAbs)
	for _, candidate := range []struct {
		name  string
		allow bool
	}{
		{name: ".gitignore", allow: !opts.noIgnoreVCS && inGitRepo},
		{name: ".ignore", allow: true},
		{name: ".rgignore", allow: true},
	} {
		if !candidate.allow {
			continue
		}
		info, _, exists, err := lstatMaybe(ctx, inv, path.Join(currentAbs, candidate.name))
		if err != nil {
			return nil, err
		}
		if !exists || info.IsDir() {
			continue
		}
		loaded, err := rgLoadIgnoreRules(ctx, inv, path.Join(currentAbs, candidate.name), baseDir)
		if err != nil {
			return nil, err
		}
		rules = append(rules, loaded...)
	}
	return rules, nil
}

func rgHasGitBoundary(ctx context.Context, inv *Invocation, dir string) bool {
	info, _, exists, err := lstatMaybe(ctx, inv, path.Join(dir, ".git"))
	if err != nil || !exists {
		return false
	}
	if info.IsDir() {
		return true
	}
	if info.Mode()&stdfs.ModeSymlink != 0 {
		statInfo, _, err := statPath(ctx, inv, path.Join(dir, ".git"))
		return err == nil && statInfo != nil && statInfo.IsDir()
	}
	return false
}

func rgFollowSymlink(ctx context.Context, inv *Invocation, abs string) (stdfs.FileInfo, string, error) {
	resolvedAbs, err := canonicalizeReadlink(ctx, inv, abs, readlinkModeCanonicalizeExisting)
	if err != nil {
		return nil, "", err
	}
	info, _, err := lstatPath(ctx, inv, resolvedAbs)
	if err != nil {
		return nil, "", err
	}
	return info, resolvedAbs, nil
}

func rgJoinDisplay(base, child string) string {
	switch base {
	case "":
		return child
	case ".":
		return "./" + child
	case "/":
		return "/" + child
	default:
		return base + "/" + child
	}
}

func currentDisplayOrAbs(display, abs string) string {
	if display != "" {
		return display
	}
	return abs
}

func rgNoteLoop(inv *Invocation, opts *grepOptions, state *grepRunState, loopDisplay, ancestorDisplay string) {
	if inv != nil && inv.Stderr != nil {
		_, _ = fmt.Fprintf(inv.Stderr, "%s: File system loop found: %s points to an ancestor %s\n", grepCommandName(opts), loopDisplay, ancestorDisplay)
	}
	state.hadError = true
}

func rgIsBinaryData(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0
}

func (r rgFileRecord) displayOrAbs() string {
	if r.display != "" {
		return r.display
	}
	return r.abs
}

var _ Command = (*RG)(nil)
var _ SpecProvider = (*RG)(nil)
var _ ParsedRunner = (*RG)(nil)
var _ ParseErrorNormalizer = (*RG)(nil)
