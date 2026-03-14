package builtins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ewhauser/gbash/policy"
)

type RG struct{}

type rgCaseMode int

const (
	rgCaseModeSmart rgCaseMode = iota
	rgCaseModeIgnore
	rgCaseModeSensitive
)

type rgOptions struct {
	caseMode            rgCaseMode
	patterns            []string
	patternFiles        []string
	types               []string
	typesNot            []string
	typeAdd             []string
	typeClear           []string
	globs               []string
	iglobs              []string
	fixedStrings        bool
	wordRegexp          bool
	lineRegexp          bool
	invert              bool
	count               bool
	listFiles           bool
	filesWithoutMatch   bool
	listOnly            bool
	typeList            bool
	hidden              bool
	noIgnore            bool
	noIgnoreDot         bool
	noIgnoreVcs         bool
	followSymlinks      bool
	searchBinary        bool
	onlyMatching        bool
	quiet               bool
	noFilename          bool
	withFilename        bool
	lineNumber          bool
	explicitLineNumbers bool
	globCaseInsensitive bool
	sortPath            bool
	maxCount            int
	beforeContext       int
	afterContext        int
	maxDepth            int
	unrestrictedCount   int
}

type rgCollectedFile struct {
	abs     string
	display string
}

type rgCollectResult struct {
	files              []rgCollectedFile
	hadError           bool
	singleExplicitFile bool
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
		Name:  "rg",
		About: "ripgrep-style recursive search",
		Usage: "rg [OPTIONS] PATTERN [PATH ...]\n       rg [OPTIONS] --files [PATH ...]\n       rg --type-list",
		Options: []OptionSpec{
			{Name: "line-number", Short: 'n', Long: "line-number", Help: "show line numbers"},
			{Name: "no-line-number", Short: 'N', Long: "no-line-number", Help: "hide line numbers"},
			{Name: "ignore-case", Short: 'i', Long: "ignore-case", Help: "case-insensitive search"},
			{Name: "case-sensitive", Short: 's', Long: "case-sensitive", Help: "case-sensitive search"},
			{Name: "smart-case", Short: 'S', Long: "smart-case", Help: "smart case search (default)"},
			{Name: "fixed-strings", Short: 'F', Long: "fixed-strings", Help: "treat patterns as literal strings"},
			{Name: "word-regexp", Short: 'w', Long: "word-regexp", Help: "show matches surrounded by word boundaries"},
			{Name: "line-regexp", Short: 'x', Long: "line-regexp", Help: "show matches surrounded by line boundaries"},
			{Name: "invert-match", Short: 'v', Long: "invert-match", Help: "invert matching"},
			{Name: "regexp", Short: 'e', Long: "regexp", Arity: OptionRequiredValue, ValueName: "PATTERN", Repeatable: true, Help: "use PATTERN for matching"},
			{Name: "file", Short: 'f', Long: "file", Arity: OptionRequiredValue, ValueName: "FILE", Repeatable: true, Help: "read patterns from FILE"},
			{Name: "count", Short: 'c', Long: "count", Help: "print only a count of matching lines per file"},
			{Name: "files-with-matches", Short: 'l', Long: "files-with-matches", Help: "print only paths with matches"},
			{Name: "files-without-match", Long: "files-without-match", Help: "print only paths without matches"},
			{Name: "files", Long: "files", Help: "print files that would be searched"},
			{Name: "type-list", Long: "type-list", Help: "show all supported file types"},
			{Name: "only-matching", Short: 'o', Long: "only-matching", Help: "show only the matching text"},
			{Name: "quiet", Short: 'q', Long: "quiet", Help: "suppress output and stop after the first match"},
			{Name: "no-filename", Long: "no-filename", Help: "never print the path with matches"},
			{Name: "with-filename", Short: 'H', Long: "with-filename", Help: "always print the path with matches"},
			{Name: "hidden", Long: "hidden", Help: "search hidden files and directories"},
			{Name: "no-ignore", Long: "no-ignore", Help: "do not respect ignore files"},
			{Name: "no-ignore-dot", Long: "no-ignore-dot", Help: "do not respect .ignore or .rgignore files"},
			{Name: "no-ignore-vcs", Long: "no-ignore-vcs", Help: "do not respect .gitignore files"},
			{Name: "glob", Short: 'g', Long: "glob", Arity: OptionRequiredValue, ValueName: "GLOB", Repeatable: true, Help: "include or exclude files matching GLOB"},
			{Name: "iglob", Long: "iglob", Arity: OptionRequiredValue, ValueName: "GLOB", Repeatable: true, Help: "like --glob but case-insensitive"},
			{Name: "glob-case-insensitive", Long: "glob-case-insensitive", Help: "make --glob matching case-insensitive"},
			{Name: "max-count", Short: 'm', Long: "max-count", Arity: OptionRequiredValue, ValueName: "NUM", Help: "stop after NUM matching lines per file"},
			{Name: "after-context", Short: 'A', Long: "after-context", Arity: OptionRequiredValue, ValueName: "NUM", Help: "show NUM lines after each match"},
			{Name: "before-context", Short: 'B', Long: "before-context", Arity: OptionRequiredValue, ValueName: "NUM", Help: "show NUM lines before each match"},
			{Name: "context", Short: 'C', Long: "context", Arity: OptionRequiredValue, ValueName: "NUM", Help: "show NUM lines before and after each match"},
			{Name: "max-depth", Short: 'd', Long: "max-depth", Arity: OptionRequiredValue, ValueName: "NUM", Help: "descend at most NUM directories"},
			{Name: "unrestricted", Short: 'u', Long: "unrestricted", Help: "reduce or disable smart filtering"},
			{Name: "follow", Short: 'L', Long: "follow", Help: "follow symbolic links"},
			{Name: "text", Short: 'a', Long: "text", Help: "search binary files as if they were text"},
			{Name: "type", Short: 't', Long: "type", Arity: OptionRequiredValue, ValueName: "TYPE", Repeatable: true, Help: "only search files matching TYPE"},
			{Name: "type-not", Short: 'T', Long: "type-not", Arity: OptionRequiredValue, ValueName: "TYPE", Repeatable: true, Help: "do not search files matching TYPE"},
			{Name: "type-add", Long: "type-add", Arity: OptionRequiredValue, ValueName: "SPEC", Repeatable: true, Help: "add a file type definition"},
			{Name: "type-clear", Long: "type-clear", Arity: OptionRequiredValue, ValueName: "TYPE", Repeatable: true, Help: "clear a file type definition"},
			{Name: "sort", Long: "sort", Arity: OptionRequiredValue, ValueName: "SORTBY", Help: "sort paths by 'path' or leave them as 'none'"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
		},
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
	message := strings.TrimSuffix(err.Error(), "\nTry 'rg --help' for more information.")
	if message == err.Error() {
		return err
	}
	trimmed := strings.TrimSuffix(message, "\n")
	if inv != nil && inv.Stderr != nil {
		_, _ = io.WriteString(inv.Stderr, trimmed+"\n")
	}
	return &ExitError{Code: exitErr.Code}
}

func (c *RG) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, roots, err := parseRGMatches(inv, matches)
	if err != nil {
		return err
	}

	if opts.typeList {
		return rgWriteTypeList(inv)
	}

	patterns, err := rgLoadPatterns(ctx, inv, &opts)
	if err != nil {
		return err
	}
	if !opts.listOnly && len(patterns) == 0 {
		if len(opts.patternFiles) > 0 {
			return &ExitError{Code: 1}
		}
		return exitf(inv, 2, "rg: no pattern given")
	}

	re, err := rgCompilePattern(patterns, &opts)
	if err != nil {
		return exitf(inv, 2, "rg: invalid regex: %v", err)
	}

	typeRegistry := newRGTypeRegistry()
	for _, name := range opts.typeClear {
		typeRegistry.ClearType(name)
	}
	for _, spec := range opts.typeAdd {
		typeRegistry.AddType(spec)
	}

	if len(roots) == 0 {
		roots = []string{"."}
	}

	var ignoreMatcher *rgIgnoreMatcher
	if !opts.noIgnore {
		ignoreMatcher = newRGIgnoreMatcher(&opts)
	}

	collectResult, err := c.collectFiles(ctx, inv, roots, &opts, ignoreMatcher, typeRegistry)
	if err != nil {
		return err
	}

	if opts.listOnly {
		for _, file := range collectResult.files {
			if _, err := fmt.Fprintln(inv.Stdout, file.display); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if collectResult.hadError {
			return &ExitError{Code: 2}
		}
		if len(collectResult.files) == 0 {
			return &ExitError{Code: 1}
		}
		return nil
	}

	grepOpts := grepOptions{
		pattern:           strings.Join(patterns, "\n"),
		ignoreCase:        rgDetermineIgnoreCase(&opts, patterns),
		lineNumber:        rgEffectiveLineNumbers(&opts, collectResult.singleExplicitFile, len(collectResult.files)),
		invert:            opts.invert,
		count:             opts.count,
		listFiles:         opts.listFiles,
		filesWithoutMatch: opts.filesWithoutMatch,
		wordRegexp:        opts.wordRegexp,
		lineRegexp:        opts.lineRegexp,
		fixedStrings:      opts.fixedStrings,
		onlyMatching:      opts.onlyMatching,
		quiet:             opts.quiet,
		maxCount:          opts.maxCount,
		beforeContext:     opts.beforeContext,
		afterContext:      opts.afterContext,
	}

	showNames := !opts.noFilename && (opts.withFilename || !collectResult.singleExplicitFile || len(collectResult.files) > 1)
	state := &grepRunState{}
	for _, file := range collectResult.files {
		data, _, err := readAllFile(ctx, inv, file.abs)
		if err != nil {
			return err
		}
		if !opts.searchBinary && rgLooksBinary(data) {
			continue
		}
		if err := writeGrepResult(inv, re, data, file.display, showNames, grepOpts, state); err != nil {
			return err
		}
		if state.quietMatched {
			return nil
		}
	}

	if state.quietMatched {
		return nil
	}
	if collectResult.hadError {
		return &ExitError{Code: 2}
	}
	if opts.filesWithoutMatch {
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
		caseMode: rgCaseModeSmart,
		maxDepth: 256,
		sortPath: true,
	}

	valueIndex := map[string]int{}
	valueAt := func(name string) string {
		values := matches.Values(name)
		idx := valueIndex[name]
		if idx >= len(values) {
			return ""
		}
		valueIndex[name] = idx + 1
		return values[idx]
	}

	explicitA := -1
	explicitB := -1
	explicitC := -1

	for _, name := range matches.OptionOrder() {
		switch name {
		case "line-number":
			opts.lineNumber = true
			opts.explicitLineNumbers = true
		case "no-line-number":
			opts.lineNumber = false
			opts.explicitLineNumbers = true
		case "ignore-case":
			opts.caseMode = rgCaseModeIgnore
		case "case-sensitive":
			opts.caseMode = rgCaseModeSensitive
		case "smart-case":
			opts.caseMode = rgCaseModeSmart
		case "fixed-strings":
			opts.fixedStrings = true
		case "word-regexp":
			opts.wordRegexp = true
		case "line-regexp":
			opts.lineRegexp = true
		case "invert-match":
			opts.invert = true
		case "regexp":
			opts.patterns = append(opts.patterns, valueAt("regexp"))
		case "file":
			opts.patternFiles = append(opts.patternFiles, valueAt("file"))
		case "count":
			opts.count = true
		case "files-with-matches":
			opts.listFiles = true
		case "files-without-match":
			opts.filesWithoutMatch = true
		case "files":
			opts.listOnly = true
		case "type-list":
			opts.typeList = true
		case "only-matching":
			opts.onlyMatching = true
		case "quiet":
			opts.quiet = true
		case "no-filename":
			opts.noFilename = true
			opts.withFilename = false
		case "with-filename":
			opts.withFilename = true
			opts.noFilename = false
		case "hidden":
			opts.hidden = true
		case "no-ignore":
			opts.noIgnore = true
		case "no-ignore-dot":
			opts.noIgnoreDot = true
		case "no-ignore-vcs":
			opts.noIgnoreVcs = true
		case "glob":
			opts.globs = append(opts.globs, valueAt("glob"))
		case "iglob":
			opts.iglobs = append(opts.iglobs, valueAt("iglob"))
		case "glob-case-insensitive":
			opts.globCaseInsensitive = true
		case "max-count":
			value, err := parseRGFlagInt(valueAt("max-count"))
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid max count %q", matches.Value("max-count"))
			}
			opts.maxCount = value
		case "after-context":
			value, err := parseRGFlagInt(valueAt("after-context"))
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid context length %q", matches.Value("after-context"))
			}
			if value > explicitA {
				explicitA = value
			}
		case "before-context":
			value, err := parseRGFlagInt(valueAt("before-context"))
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid context length %q", matches.Value("before-context"))
			}
			if value > explicitB {
				explicitB = value
			}
		case "context":
			value, err := parseRGFlagInt(valueAt("context"))
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid context length %q", matches.Value("context"))
			}
			explicitC = value
		case "max-depth":
			value, err := parseRGFlagInt(valueAt("max-depth"))
			if err != nil {
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid maximum depth %q", matches.Value("max-depth"))
			}
			opts.maxDepth = value
		case "unrestricted":
			opts.unrestrictedCount++
		case "follow":
			opts.followSymlinks = true
		case "text":
			opts.searchBinary = true
		case "type":
			opts.types = append(opts.types, valueAt("type"))
		case "type-not":
			opts.typesNot = append(opts.typesNot, valueAt("type-not"))
		case "type-add":
			opts.typeAdd = append(opts.typeAdd, valueAt("type-add"))
		case "type-clear":
			opts.typeClear = append(opts.typeClear, valueAt("type-clear"))
		case "sort":
			switch valueAt("sort") {
			case "path", "":
				opts.sortPath = true
			case "none":
				opts.sortPath = false
			default:
				return rgOptions{}, nil, exitf(inv, 2, "rg: invalid sort mode %q", matches.Value("sort"))
			}
		}
	}

	if explicitA >= 0 || explicitC >= 0 {
		opts.afterContext = max(explicitA, explicitC)
	}
	if explicitB >= 0 || explicitC >= 0 {
		opts.beforeContext = max(explicitB, explicitC)
	}

	if opts.unrestrictedCount >= 1 {
		opts.noIgnore = true
	}
	if opts.unrestrictedCount >= 2 {
		opts.hidden = true
	}
	if opts.unrestrictedCount >= 3 {
		opts.searchBinary = true
	}

	args := matches.Args("arg")
	if opts.listOnly || opts.typeList {
		return opts, args, nil
	}
	if len(opts.patterns) == 0 && len(opts.patternFiles) == 0 {
		if len(args) == 0 {
			return opts, nil, nil
		}
		opts.patterns = append(opts.patterns, args[0])
		args = args[1:]
	}
	return opts, args, nil
}

func parseRGFlagInt(value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid number")
	}
	return number, nil
}

func rgLoadPatterns(ctx context.Context, inv *Invocation, opts *rgOptions) ([]string, error) {
	patterns := append([]string(nil), opts.patterns...)
	for _, name := range opts.patternFiles {
		var data []byte
		var err error
		if name == "-" {
			data, err = readAllStdin(ctx, inv)
		} else {
			data, _, err = readAllFile(ctx, inv, name)
		}
		if err != nil {
			return nil, exitf(inv, 2, "rg: %s: %s", name, readAllErrorText(err))
		}
		for _, line := range textLines(data) {
			if line == "" {
				continue
			}
			patterns = append(patterns, line)
		}
	}
	return patterns, nil
}

func rgCompilePattern(patterns []string, opts *rgOptions) (*regexp.Regexp, error) {
	ignoreCase := rgDetermineIgnoreCase(opts, patterns)
	pattern := ""
	switch {
	case len(patterns) == 0:
		pattern = ""
	case opts.fixedStrings:
		pattern = strings.Join(patterns, "\n")
	case len(patterns) == 1:
		pattern = patterns[0]
	default:
		parts := make([]string, 0, len(patterns))
		for _, item := range patterns {
			parts = append(parts, "(?:"+item+")")
		}
		pattern = strings.Join(parts, "|")
	}
	return compileGrepPattern(grepOptions{
		pattern:      pattern,
		ignoreCase:   ignoreCase,
		wordRegexp:   opts.wordRegexp,
		lineRegexp:   opts.lineRegexp,
		fixedStrings: opts.fixedStrings,
	})
}

func rgDetermineIgnoreCase(opts *rgOptions, patterns []string) bool {
	switch opts.caseMode {
	case rgCaseModeIgnore:
		return true
	case rgCaseModeSensitive:
		return false
	default:
		for _, pattern := range patterns {
			for _, r := range pattern {
				if unicode.IsUpper(r) {
					return false
				}
			}
		}
		return true
	}
}

func rgEffectiveLineNumbers(opts *rgOptions, singleExplicitFile bool, fileCount int) bool {
	if opts.explicitLineNumbers {
		return opts.lineNumber
	}
	if opts.onlyMatching {
		return false
	}
	return !singleExplicitFile || fileCount != 1
}

func rgLooksBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0
}

func (c *RG) collectFiles(ctx context.Context, inv *Invocation, roots []string, opts *rgOptions, ignoreMatcher *rgIgnoreMatcher, typeRegistry *rgTypeRegistry) (rgCollectResult, error) {
	result := rgCollectResult{
		files: make([]rgCollectedFile, 0),
	}

	explicitFileCount := 0
	directoryCount := 0
	for _, root := range roots {
		info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, root)
		if err != nil {
			return rgCollectResult{}, err
		}
		if !exists {
			_, _ = fmt.Fprintf(inv.Stderr, "rg: %s: No such file or directory\n", root)
			result.hadError = true
			continue
		}
		if ignoreMatcher != nil {
			if err := ignoreMatcher.loadPath(ctx, inv, abs); err != nil {
				return rgCollectResult{}, err
			}
		}
		if !info.IsDir() {
			explicitFileCount++
			display := rgDisplayExplicitRoot(inv, root, abs)
			if c.includeFile(display, abs, opts, ignoreMatcher, typeRegistry) {
				result.files = append(result.files, rgCollectedFile{abs: abs, display: display})
			}
			continue
		}
		directoryCount++
		if err := c.walkRoot(ctx, inv, &rgWalkState{
			rootAbs:      abs,
			prefix:       rgDisplayDirRoot(root),
			depth:        0,
			opts:         opts,
			ignore:       ignoreMatcher,
			typeRegistry: typeRegistry,
		}, &result.files); err != nil {
			return rgCollectResult{}, err
		}
	}

	if opts.sortPath {
		sort.Slice(result.files, func(i, j int) bool {
			return result.files[i].display < result.files[j].display
		})
	}
	result.singleExplicitFile = explicitFileCount == 1 && directoryCount == 0
	return result, nil
}

type rgWalkState struct {
	rootAbs      string
	prefix       string
	depth        int
	opts         *rgOptions
	ignore       *rgIgnoreMatcher
	typeRegistry *rgTypeRegistry
}

func (c *RG) walkRoot(ctx context.Context, inv *Invocation, state *rgWalkState, files *[]rgCollectedFile) error {
	if state.depth >= state.opts.maxDepth {
		return nil
	}
	if state.ignore != nil {
		if err := state.ignore.loadPath(ctx, inv, state.rootAbs); err != nil {
			return err
		}
	}

	entries, _, err := readDir(ctx, inv, state.rootAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if state.ignore != nil && !state.opts.noIgnore && rgIsCommonIgnoredDir(name) {
			continue
		}

		childAbs := joinChildPath(state.rootAbs, name)
		display := name
		if state.prefix != "" {
			display = state.prefix + "/" + name
		}

		linfo, _, err := lstatPath(ctx, inv, childAbs)
		if err != nil {
			return err
		}
		isSymlink := linfo.Mode()&stdfs.ModeSymlink != 0
		if isSymlink && !state.opts.followSymlinks {
			continue
		}

		info := linfo
		if isSymlink {
			info, _, err = statPath(ctx, inv, childAbs)
			if err != nil {
				continue
			}
		}

		isDir := info.IsDir()
		if state.ignore != nil && state.ignore.matches(childAbs, isDir) {
			continue
		}
		if !state.opts.hidden && strings.HasPrefix(name, ".") && (state.ignore == nil || !state.ignore.whitelisted(childAbs, isDir)) {
			continue
		}

		if isDir {
			next := *state
			next.rootAbs = childAbs
			next.prefix = display
			next.depth++
			if err := c.walkRoot(ctx, inv, &next, files); err != nil {
				return err
			}
			continue
		}

		if c.includeFile(display, childAbs, state.opts, state.ignore, state.typeRegistry) {
			*files = append(*files, rgCollectedFile{abs: childAbs, display: display})
		}
	}
	return nil
}

func (c *RG) includeFile(display, abs string, opts *rgOptions, ignoreMatcher *rgIgnoreMatcher, typeRegistry *rgTypeRegistry) bool {
	if ignoreMatcher != nil && ignoreMatcher.matches(abs, false) {
		return false
	}

	filename := path.Base(display)
	if len(opts.types) > 0 && !typeRegistry.MatchesType(filename, opts.types) {
		return false
	}
	if len(opts.typesNot) > 0 && typeRegistry.MatchesType(filename, opts.typesNot) {
		return false
	}

	if !rgMatchGlobSet(display, filename, opts.globs, opts.globCaseInsensitive) {
		return false
	}
	if !rgMatchGlobSet(display, filename, opts.iglobs, true) {
		return false
	}
	return true
}

func rgMatchGlobSet(display, filename string, globs []string, ignoreCase bool) bool {
	if len(globs) == 0 {
		return true
	}
	positive := make([]string, 0, len(globs))
	negative := make([]string, 0, len(globs))
	for _, glob := range globs {
		trimmed := glob
		if trimmed == "" {
			continue
		}
		if negatedGlob, ok := strings.CutPrefix(trimmed, "!"); ok {
			negative = append(negative, negatedGlob)
			continue
		}
		positive = append(positive, trimmed)
	}

	if len(positive) > 0 {
		matched := false
		for _, glob := range positive {
			ok, _ := rgMatchGlob(filename, glob, ignoreCase)
			if rootedGlob, rooted := strings.CutPrefix(glob, "/"); rooted {
				ok, _ = rgMatchGlob(display, rootedGlob, ignoreCase)
			} else if !ok {
				ok, _ = rgMatchGlob(display, glob, ignoreCase)
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	for _, glob := range negative {
		ok, _ := rgMatchGlob(filename, glob, ignoreCase)
		if rootedGlob, rooted := strings.CutPrefix(glob, "/"); rooted {
			ok, _ = rgMatchGlob(display, rootedGlob, ignoreCase)
		} else if !ok {
			ok, _ = rgMatchGlob(display, glob, ignoreCase)
		}
		if ok {
			return false
		}
	}
	return true
}

func rgMatchGlob(value, glob string, ignoreCase bool) (bool, error) {
	re, err := rgGlobRegexp(glob, ignoreCase)
	if err != nil {
		return false, err
	}
	return re.MatchString(value), nil
}

func rgGlobRegexp(glob string, ignoreCase bool) (*regexp.Regexp, error) {
	var b strings.Builder
	if ignoreCase {
		b.WriteString("(?i)")
	}
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := i + 1
			if j < len(glob) && glob[j] == '!' {
				j++
			}
			if j < len(glob) && glob[j] == ']' {
				j++
			}
			for j < len(glob) && glob[j] != ']' {
				j++
			}
			if j >= len(glob) {
				return nil, fmt.Errorf("unclosed character class")
			}
			class := glob[i : j+1]
			if strings.HasPrefix(class, "[!") {
				class = "[^" + class[2:]
			}
			b.WriteString(class)
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func rgDisplayExplicitRoot(inv *Invocation, root, abs string) string {
	if root == "" {
		root = abs
	}
	cleaned := path.Clean(root)
	if cleaned == "." {
		if rel := rgRelativeToCwd(inv, abs); rel != "" && rel != "." {
			return rel
		}
	}
	return cleaned
}

func rgDisplayDirRoot(root string) string {
	cleaned := path.Clean(root)
	switch cleaned {
	case "", ".":
		return ""
	default:
		return cleaned
	}
}

func rgRelativeToCwd(inv *Invocation, abs string) string {
	if inv == nil || inv.FS == nil {
		return abs
	}
	cwd := inv.FS.Getwd()
	if abs == cwd {
		return "."
	}
	prefix := cwd
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if rel, ok := strings.CutPrefix(abs, prefix); ok {
		return rel
	}
	return abs
}

func rgIsCommonIgnoredDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "__pycache__", ".pytest_cache", ".mypy_cache", "venv", ".venv", ".next", ".nuxt", ".cargo":
		return true
	default:
		return false
	}
}

func rgWriteTypeList(inv *Invocation) error {
	output := formatRGTypeList()
	if _, err := io.WriteString(inv.Stdout, output); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*RG)(nil)
var _ SpecProvider = (*RG)(nil)
var _ ParsedRunner = (*RG)(nil)
var _ ParseErrorNormalizer = (*RG)(nil)
