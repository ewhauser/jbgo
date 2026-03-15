package builtins

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/searchadapter"
	"github.com/ewhauser/gbash/policy"
)

type Grep struct{}

type grepOptions struct {
	pattern           string
	ignoreCase        bool
	lineNumber        bool
	invert            bool
	count             bool
	listFiles         bool
	filesWithoutMatch bool
	recursive         bool
	wordRegexp        bool
	lineRegexp        bool
	fixedStrings      bool
	perlRegexp        bool
	onlyMatching      bool
	noFilename        bool
	quiet             bool
	maxCount          int
	beforeContext     int
	afterContext      int
}

type grepSearchResult struct {
	output     string
	matched    bool
	matchCount int
}

type grepRunState struct {
	matchedAny           bool
	filesWithoutMatchAny bool
	hadError             bool
	quietMatched         bool
}

type grepFileRecord struct {
	abs           string
	scope         *grepSearchScope
	indexEligible bool
}

type grepSearchScope struct {
	root          string
	eligiblePaths []string
	candidates    map[string]struct{}
	usedIndex     bool
}

func NewGrep() *Grep {
	return &Grep{}
}

func (c *Grep) Name() string {
	return "grep"
}

func (c *Grep) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Grep) Spec() CommandSpec {
	return CommandSpec{
		Name:  "grep",
		About: "Print lines that match patterns.",
		Usage: "grep [OPTION]... PATTERNS [FILE]...",
		Options: []OptionSpec{
			{Name: "ignore-case", Short: 'i', Long: "ignore-case", Help: "ignore case distinctions"},
			{Name: "line-number", Short: 'n', Long: "line-number", Help: "print line number with output lines"},
			{Name: "invert-match", Short: 'v', Long: "invert-match", Help: "select non-matching lines"},
			{Name: "count", Short: 'c', Long: "count", Help: "print only a count of matching lines per FILE"},
			{Name: "files-with-matches", Short: 'l', Long: "files-with-matches", Help: "print only names of FILEs with selected lines"},
			{Name: "files-without-match", Short: 'L', Long: "files-without-match", Help: "print only names of FILEs with no selected lines"},
			{Name: "recursive", Short: 'r', ShortAliases: []rune{'R'}, Long: "recursive", Help: "read all files under each directory, recursively"},
			{Name: "word-regexp", Short: 'w', Long: "word-regexp", Help: "select only whole words"},
			{Name: "line-regexp", Short: 'x', Long: "line-regexp", Help: "select only whole lines"},
			{Name: "extended-regexp", Short: 'E', Long: "extended-regexp", Help: "interpret PATTERNS as extended regular expressions"},
			{Name: "fixed-strings", Short: 'F', Long: "fixed-strings", Help: "interpret PATTERNS as fixed strings"},
			{Name: "perl-regexp", Short: 'P', Long: "perl-regexp", Help: "interpret PATTERNS as Perl-compatible regular expressions"},
			{Name: "only-matching", Short: 'o', Long: "only-matching", Help: "show only the part of a line matching PATTERNS"},
			{Name: "no-filename", Short: 'h', Long: "no-filename", Help: "suppress the file name prefix on output"},
			{Name: "quiet", Short: 'q', Long: "quiet", Aliases: []string{"silent"}, HelpAliases: []string{"silent"}, Help: "suppress all normal output"},
			{Name: "regexp", Short: 'e', Arity: OptionRequiredValue, ValueName: "PATTERNS", Help: "use PATTERNS for matching"},
			{Name: "max-count", Short: 'm', Long: "max-count", Arity: OptionRequiredValue, ValueName: "NUM", Help: "stop after NUM selected lines"},
			{Name: "after-context", Short: 'A', Arity: OptionRequiredValue, ValueName: "NUM", Help: "print NUM lines of trailing context"},
			{Name: "before-context", Short: 'B', Arity: OptionRequiredValue, ValueName: "NUM", Help: "print NUM lines of leading context"},
			{Name: "context", Short: 'C', Arity: OptionRequiredValue, ValueName: "NUM", Help: "print NUM lines of output context"},
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
		},
	}
}

func (c *Grep) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, files, err := parseGrepMatches(inv, matches)
	if err != nil {
		return err
	}

	re, err := compileGrepPattern(opts)
	if err != nil {
		return exitf(inv, 2, "grep: invalid pattern: %v", err)
	}

	state := &grepRunState{}
	if len(files) == 0 {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return err
		}
		if err := writeGrepResult(inv, re, data, "", false, opts, state); err != nil {
			return err
		}
	} else {
		showNames := (len(files) > 1 || opts.recursive) && !opts.noFilename
		for _, file := range files {
			records := make([]grepFileRecord, 0, 8)
			scope, err := c.enumerateTopLevelPath(ctx, inv, file, opts, state, &records)
			if err != nil {
				return err
			}
			if scope != nil {
				if err := c.prefilterScopes(ctx, inv, []*grepSearchScope{scope}, re, opts); err != nil {
					return err
				}
			}
			for _, record := range records {
				if state.quietMatched {
					return nil
				}
				if !grepPathNeedsVerification(record) {
					if err := writeGrepGuaranteedMiss(inv, record.abs, showNames, opts, state); err != nil {
						return err
					}
					continue
				}

				data, _, err := readAllFile(ctx, inv, record.abs)
				if err != nil {
					return err
				}
				if err := writeGrepResult(inv, re, data, record.abs, showNames, opts, state); err != nil {
					return err
				}
			}
			if state.quietMatched {
				return nil
			}
		}
	}

	if state.quietMatched {
		return nil
	}
	if state.hadError {
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

func parseGrepMatches(inv *Invocation, matches *ParsedCommand) (grepOptions, []string, error) {
	var opts grepOptions

	for _, name := range matches.OptionOrder() {
		switch name {
		case "ignore-case":
			opts.ignoreCase = true
		case "line-number":
			opts.lineNumber = true
		case "invert-match":
			opts.invert = true
		case "count":
			opts.count = true
		case "files-with-matches":
			opts.listFiles = true
		case "files-without-match":
			opts.filesWithoutMatch = true
		case "recursive":
			opts.recursive = true
		case "word-regexp":
			opts.wordRegexp = true
		case "line-regexp":
			opts.lineRegexp = true
		case "extended-regexp":
			// accepted for compatibility; default regexp engine already handles the current surface
		case "fixed-strings":
			opts.fixedStrings = true
		case "perl-regexp":
			opts.perlRegexp = true
		case "only-matching":
			opts.onlyMatching = true
		case "no-filename":
			opts.noFilename = true
		case "quiet":
			opts.quiet = true
		case "regexp":
			opts.pattern = matches.Value("regexp")
		case "max-count":
			value, err := parseGrepFlagInt(matches.Value("max-count"))
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid max count %q", matches.Value("max-count"))
			}
			opts.maxCount = value
		case "after-context":
			value, err := parseGrepFlagInt(matches.Value("after-context"))
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", matches.Value("after-context"))
			}
			setGrepContext(&opts, "-A", value)
		case "before-context":
			value, err := parseGrepFlagInt(matches.Value("before-context"))
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", matches.Value("before-context"))
			}
			setGrepContext(&opts, "-B", value)
		case "context":
			value, err := parseGrepFlagInt(matches.Value("context"))
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", matches.Value("context"))
			}
			setGrepContext(&opts, "-C", value)
		}
	}

	args := matches.Args("arg")
	if opts.pattern == "" {
		if len(args) == 0 {
			return grepOptions{}, nil, exitf(inv, 2, "grep: missing pattern")
		}
		opts.pattern = args[0]
		args = args[1:]
	}

	return opts, args, nil
}

func setGrepContext(opts *grepOptions, flag string, value int) {
	switch flag {
	case "-A":
		opts.afterContext = value
	case "-B":
		opts.beforeContext = value
	case "-C":
		opts.beforeContext = value
		opts.afterContext = value
	}
}

func parseGrepFlagInt(value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid number")
	}
	return number, nil
}

func compileGrepPattern(opts grepOptions) (*regexp.Regexp, error) {
	pattern := opts.pattern
	if opts.fixedStrings {
		patterns := strings.Split(pattern, "\n")
		for i, part := range patterns {
			patterns[i] = regexp.QuoteMeta(part)
		}
		pattern = strings.Join(patterns, "|")
		if len(patterns) > 1 {
			pattern = "(?:" + pattern + ")"
		}
	}
	if opts.wordRegexp {
		pattern = `\b(?:` + pattern + `)\b`
	}
	if opts.lineRegexp {
		pattern = `^(?:` + pattern + `)$`
	}
	if opts.ignoreCase {
		pattern = `(?i)` + pattern
	}
	return regexp.Compile(pattern)
}

func grepSearchContent(re *regexp.Regexp, data []byte, name string, showName bool, opts grepOptions) (grepSearchResult, error) {
	if opts.count {
		matchCount := 0
		countMatches := opts.onlyMatching && !opts.invert
		visitTextLines(data, func(line []byte, _ int) bool {
			if countMatches {
				matchCount += len(re.FindAllIndex(line, -1))
				return true
			}
			if re.Match(line) != opts.invert {
				matchCount++
				if opts.maxCount > 0 && matchCount >= opts.maxCount {
					return false
				}
			}
			return true
		})

		prefix := ""
		if showName {
			prefix = name + ":"
		}
		return grepSearchResult{
			output:     fmt.Sprintf("%s%d\n", prefix, matchCount),
			matched:    matchCount > 0,
			matchCount: matchCount,
		}, nil
	}

	lines := textLines(data)

	if opts.beforeContext == 0 && opts.afterContext == 0 {
		outputLines := make([]string, 0)
		matchCount := 0
		hasMatch := false

		for i, line := range lines {
			if opts.maxCount > 0 && matchCount >= opts.maxCount {
				break
			}

			matches := re.FindAllStringIndex(line, -1)
			selected := (len(matches) > 0) != opts.invert
			if !selected {
				continue
			}

			hasMatch = true
			matchCount++
			if opts.onlyMatching {
				for _, match := range matches {
					prefix := grepLinePrefix(name, showName, i+1, opts.lineNumber, true)
					outputLines = append(outputLines, prefix+line[match[0]:match[1]])
				}
				continue
			}

			prefix := grepLinePrefix(name, showName, i+1, opts.lineNumber, true)
			outputLines = append(outputLines, prefix+line)
		}

		return grepSearchResult{
			output:     grepJoinOutput(outputLines),
			matched:    hasMatch,
			matchCount: matchCount,
		}, nil
	}

	matchingLines := make([]int, 0)
	matchCount := 0
	for i, line := range lines {
		if opts.maxCount > 0 && matchCount >= opts.maxCount {
			break
		}
		matches := re.FindAllStringIndex(line, -1)
		if (len(matches) > 0) != opts.invert {
			matchingLines = append(matchingLines, i)
			matchCount++
		}
	}

	outputLines := make([]string, 0)
	printedLines := make(map[int]bool)
	lastPrintedLine := -1

	for _, lineNumber := range matchingLines {
		contextStart := max(lineNumber-opts.beforeContext, 0)

		if lastPrintedLine >= 0 && contextStart > lastPrintedLine+1 {
			outputLines = append(outputLines, "--")
		}

		for i := contextStart; i < lineNumber; i++ {
			if printedLines[i] {
				continue
			}
			printedLines[i] = true
			lastPrintedLine = i
			prefix := grepLinePrefix(name, showName, i+1, opts.lineNumber, false)
			outputLines = append(outputLines, prefix+lines[i])
		}

		if !printedLines[lineNumber] {
			printedLines[lineNumber] = true
			lastPrintedLine = lineNumber

			if opts.onlyMatching {
				matches := re.FindAllStringIndex(lines[lineNumber], -1)
				for _, match := range matches {
					prefix := grepLinePrefix(name, showName, lineNumber+1, opts.lineNumber, true)
					outputLines = append(outputLines, prefix+lines[lineNumber][match[0]:match[1]])
				}
			} else {
				prefix := grepLinePrefix(name, showName, lineNumber+1, opts.lineNumber, true)
				outputLines = append(outputLines, prefix+lines[lineNumber])
			}
		}

		maxAfter := lineNumber + opts.afterContext
		if maxAfter >= len(lines) {
			maxAfter = len(lines) - 1
		}
		for i := lineNumber + 1; i <= maxAfter; i++ {
			if printedLines[i] {
				continue
			}
			printedLines[i] = true
			lastPrintedLine = i
			prefix := grepLinePrefix(name, showName, i+1, opts.lineNumber, false)
			outputLines = append(outputLines, prefix+lines[i])
		}
	}

	return grepSearchResult{
		output:     grepJoinOutput(outputLines),
		matched:    matchCount > 0,
		matchCount: matchCount,
	}, nil
}

func grepJoinOutput(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func grepLinePrefix(name string, showName bool, lineNumber int, showLineNumber, matching bool) string {
	separator := ":"
	if !matching {
		separator = "-"
	}

	var b strings.Builder
	if showName {
		b.WriteString(name)
		b.WriteString(separator)
	}
	if showLineNumber {
		b.WriteString(strconv.Itoa(lineNumber))
		b.WriteString(separator)
	}
	return b.String()
}

func writeGrepResult(inv *Invocation, re *regexp.Regexp, data []byte, name string, showName bool, opts grepOptions, state *grepRunState) error {
	result, err := grepSearchContent(re, data, name, showName, opts)
	if err != nil {
		return err
	}

	if result.matched {
		state.matchedAny = true
		if opts.quiet && !opts.filesWithoutMatch {
			state.quietMatched = true
			return nil
		}
	}

	if opts.filesWithoutMatch {
		if !result.matched {
			state.filesWithoutMatchAny = true
			if !opts.quiet {
				if _, err := fmt.Fprintln(inv.Stdout, name); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
		}
		return nil
	}

	if opts.listFiles {
		if result.matched && !opts.quiet {
			if _, err := fmt.Fprintln(inv.Stdout, name); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	if opts.quiet || result.output == "" {
		return nil
	}

	if _, err := fmt.Fprint(inv.Stdout, result.output); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func writeGrepGuaranteedMiss(inv *Invocation, name string, showName bool, opts grepOptions, state *grepRunState) error {
	if opts.filesWithoutMatch {
		state.filesWithoutMatchAny = true
		if opts.quiet {
			return nil
		}
		if _, err := fmt.Fprintln(inv.Stdout, name); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	if opts.listFiles || opts.quiet {
		return nil
	}

	if opts.count {
		prefix := ""
		if showName {
			prefix = name + ":"
		}
		if _, err := fmt.Fprintf(inv.Stdout, "%s0\n", prefix); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func (c *Grep) enumerateTopLevelPath(ctx context.Context, inv *Invocation, file string, opts grepOptions, state *grepRunState, records *[]grepFileRecord) (*grepSearchScope, error) {
	linfo, abs, exists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, file)
	if err != nil {
		return nil, err
	}
	if !exists {
		_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: No such file or directory\n", file)
		state.hadError = true
		return nil, nil
	}

	info := linfo
	throughSymlinkDir := false
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, _, err = statPath(ctx, inv, abs)
		if err != nil {
			if errorsIsNotExist(err) {
				_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: No such file or directory\n", file)
				state.hadError = true
				return nil, nil
			}
			return nil, err
		}
		throughSymlinkDir = info.IsDir()
	}

	if info.IsDir() {
		if !opts.recursive {
			_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: Is a directory\n", file)
			state.hadError = true
			return nil, nil
		}
		scope := &grepSearchScope{root: abs}
		if err := c.enumerateRecursive(ctx, inv, abs, throughSymlinkDir, scope, records); err != nil {
			return nil, err
		}
		return scope, nil
	}

	scope := &grepSearchScope{root: abs}
	record := grepFileRecord{
		abs:           abs,
		scope:         scope,
		indexEligible: grepIndexableFileInfo(info),
	}
	if record.indexEligible {
		scope.eligiblePaths = append(scope.eligiblePaths, abs)
	}
	*records = append(*records, record)
	return scope, nil
}

func (c *Grep) enumerateRecursive(ctx context.Context, inv *Invocation, currentAbs string, throughSymlinkDir bool, scope *grepSearchScope, records *[]grepFileRecord) error {
	linfo, _, err := lstatPath(ctx, inv, currentAbs)
	if err != nil {
		return err
	}

	info := linfo
	currentThroughSymlinkDir := throughSymlinkDir
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, _, err = statPath(ctx, inv, currentAbs)
		if err != nil {
			return err
		}
		if info.IsDir() {
			currentThroughSymlinkDir = true
		}
	}

	if !info.IsDir() {
		record := grepFileRecord{
			abs:           currentAbs,
			scope:         scope,
			indexEligible: grepIndexableFileInfo(info) && !currentThroughSymlinkDir,
		}
		if record.indexEligible {
			scope.eligiblePaths = append(scope.eligiblePaths, currentAbs)
		}
		*records = append(*records, record)
		return nil
	}

	entries, _, err := readDir(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childAbs := path.Join(currentAbs, entry.Name())
		if err := c.enumerateRecursive(ctx, inv, childAbs, currentThroughSymlinkDir, scope, records); err != nil {
			return err
		}
	}
	return nil
}

func (c *Grep) prefilterScopes(ctx context.Context, inv *Invocation, scopes []*grepSearchScope, re *regexp.Regexp, opts grepOptions) error {
	if opts.invert || inv == nil || inv.FS == nil {
		return nil
	}
	for _, scope := range scopes {
		if scope == nil || len(scope.eligiblePaths) == 0 {
			continue
		}
		provider, _, ok, err := inv.FS.SearchProviderForPath(ctx, scope.root)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		result, err := searchadapter.PrefilterCandidates(ctx, provider, scope.root, scope.eligiblePaths, re, opts.ignoreCase)
		if err != nil {
			return err
		}
		if result.UsedIndex {
			scope.candidates = result.CandidatePaths
			scope.usedIndex = true
		}
	}
	return nil
}

func grepIndexableFileInfo(info stdfs.FileInfo) bool {
	return info != nil && info.Mode().IsRegular()
}

func grepPathNeedsVerification(record grepFileRecord) bool {
	if record.scope == nil || !record.scope.usedIndex || !record.indexEligible {
		return true
	}
	_, ok := record.scope.candidates[gbfs.Clean(record.abs)]
	return ok
}

var _ Command = (*Grep)(nil)
var _ SpecProvider = (*Grep)(nil)
var _ ParsedRunner = (*Grep)(nil)
