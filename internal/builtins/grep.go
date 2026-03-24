package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type Grep struct{}

type grepPatternMode uint8

const (
	grepPatternModeBRE grepPatternMode = iota
	grepPatternModeERE
	grepPatternModeFixed
	grepPatternModePCRE
)

type grepFilenameMode uint8

const (
	grepFilenameDefault grepFilenameMode = iota
	grepFilenameWith
	grepFilenameWithout
)

type grepPatternInputKind uint8

const (
	grepPatternInputText grepPatternInputKind = iota
	grepPatternInputFile
)

type grepPatternInput struct {
	kind  grepPatternInputKind
	value string
}

type grepOptions struct {
	patternInputs     []grepPatternInput
	ignoreCase        bool
	lineNumber        bool
	invert            bool
	count             bool
	listFiles         bool
	filesWithoutMatch bool
	recursive         bool
	wordRegexp        bool
	lineRegexp        bool
	onlyMatching      bool
	quiet             bool
	suppressMessages  bool
	matchMode         grepPatternMode
	filenameMode      grepFilenameMode
	maxCount          int
	maxCountSet       bool
	beforeContext     int
	afterContext      int
}

type grepMatcher struct {
	re *regexp.Regexp
}

type grepSearchResult struct {
	output        string
	matched       bool
	selectedLines int
}

type grepRunState struct {
	matchedAny           bool
	filesWithoutMatchAny bool
	hadError             bool
	quietMatched         bool
}

type grepFileRecord struct {
	abs string
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
			{Name: "with-filename", Short: 'H', Long: "with-filename", Help: "print the file name for each match"},
			{Name: "no-filename", Short: 'h', Long: "no-filename", Help: "suppress the file name prefix on output"},
			{Name: "quiet", Short: 'q', Long: "quiet", Aliases: []string{"silent"}, HelpAliases: []string{"silent"}, Help: "suppress all normal output"},
			{Name: "no-messages", Short: 's', Long: "no-messages", Help: "suppress error messages"},
			{Name: "regexp", Short: 'e', Arity: OptionRequiredValue, ValueName: "PATTERNS", Help: "use PATTERNS for matching"},
			{Name: "file", Short: 'f', Long: "file", Arity: OptionRequiredValue, ValueName: "FILE", Help: "take PATTERNS from FILE"},
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

func (c *Grep) NormalizeParseError(inv *Invocation, err error) error {
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

func (c *Grep) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, files, err := parseGrepMatches(inv, matches)
	if err != nil {
		return err
	}

	matcher, err := compileGrepPattern(ctx, inv, opts)
	if err != nil {
		return err
	}

	state := &grepRunState{}
	defaultShowNames := (len(files) > 1 || opts.recursive)

	if len(files) == 0 {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return err
		}
		if err := writeGrepResult(inv, matcher, data, "", false, opts, state); err != nil {
			return err
		}
	} else {
		for _, file := range files {
			records := make([]grepFileRecord, 0, 8)
			visitedDirs := make(map[string]struct{})
			if err := c.enumerateTopLevelPath(ctx, inv, file, opts, state, visitedDirs, &records); err != nil {
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
					grepNoteFileError(inv, opts, state, record.abs, err)
					continue
				}
				showNames := grepShouldShowFilename(record.abs, defaultShowNames, opts)
				if err := writeGrepResult(inv, matcher, data, record.abs, showNames, opts, state); err != nil {
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
	opts := grepOptions{
		matchMode: grepPatternModeBRE,
	}

	regexpValues := matches.Values("regexp")
	regexpIndex := 0
	fileValues := matches.Values("file")
	fileIndex := 0
	maxCountValues := matches.Values("max-count")
	maxCountIndex := 0
	afterValues := matches.Values("after-context")
	afterIndex := 0
	beforeValues := matches.Values("before-context")
	beforeIndex := 0
	contextValues := matches.Values("context")
	contextIndex := 0

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
			opts.matchMode = grepPatternModeERE
		case "fixed-strings":
			opts.matchMode = grepPatternModeFixed
		case "perl-regexp":
			opts.matchMode = grepPatternModePCRE
		case "only-matching":
			opts.onlyMatching = true
		case "with-filename":
			opts.filenameMode = grepFilenameWith
		case "no-filename":
			opts.filenameMode = grepFilenameWithout
		case "quiet":
			opts.quiet = true
		case "no-messages":
			opts.suppressMessages = true
		case "regexp":
			value := grepNextOptionValue(regexpValues, &regexpIndex)
			opts.patternInputs = append(opts.patternInputs, grepPatternInput{
				kind:  grepPatternInputText,
				value: value,
			})
		case "file":
			value := grepNextOptionValue(fileValues, &fileIndex)
			opts.patternInputs = append(opts.patternInputs, grepPatternInput{
				kind:  grepPatternInputFile,
				value: value,
			})
		case "max-count":
			value := grepNextOptionValue(maxCountValues, &maxCountIndex)
			number, err := parseGrepFlagInt(value)
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid max count %q", value)
			}
			opts.maxCount = number
			opts.maxCountSet = true
		case "after-context":
			value := grepNextOptionValue(afterValues, &afterIndex)
			number, err := parseGrepFlagInt(value)
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", value)
			}
			setGrepContext(&opts, "-A", number)
		case "before-context":
			value := grepNextOptionValue(beforeValues, &beforeIndex)
			number, err := parseGrepFlagInt(value)
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", value)
			}
			setGrepContext(&opts, "-B", number)
		case "context":
			value := grepNextOptionValue(contextValues, &contextIndex)
			number, err := parseGrepFlagInt(value)
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", value)
			}
			setGrepContext(&opts, "-C", number)
		}
	}

	args := matches.Args("arg")
	if len(opts.patternInputs) == 0 {
		if len(args) == 0 {
			return grepOptions{}, nil, exitf(inv, 2, "grep: missing pattern")
		}
		opts.patternInputs = append(opts.patternInputs, grepPatternInput{
			kind:  grepPatternInputText,
			value: args[0],
		})
		args = args[1:]
	}

	return opts, args, nil
}

func grepNextOptionValue(values []string, index *int) string {
	if index == nil || *index >= len(values) {
		return ""
	}
	value := values[*index]
	*index += 1
	return value
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

func compileGrepPattern(ctx context.Context, inv *Invocation, opts grepOptions) (grepMatcher, error) {
	if opts.matchMode == grepPatternModePCRE {
		return grepMatcher{}, exitf(inv, 2, "grep: support for the -P option is not compiled into this build")
	}

	patterns, err := loadGrepPatterns(ctx, inv, opts)
	if err != nil {
		return grepMatcher{}, err
	}
	if len(patterns) == 0 {
		return grepMatcher{}, nil
	}

	parts := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		part, err := grepCompilePart(pattern, opts.matchMode)
		if err != nil {
			return grepMatcher{}, exitf(inv, 2, "grep: invalid pattern: %v", err)
		}
		parts = append(parts, part)
	}

	compiledPattern := grepJoinPatterns(parts)
	if opts.wordRegexp {
		compiledPattern = `\b(?:` + compiledPattern + `)\b`
	}
	if opts.lineRegexp {
		compiledPattern = `^(?:` + compiledPattern + `)$`
	}
	if opts.ignoreCase {
		compiledPattern = `(?i)` + compiledPattern
	}

	re, err := regexp.Compile(compiledPattern)
	if err != nil {
		return grepMatcher{}, exitf(inv, 2, "grep: invalid pattern: %v", err)
	}
	return grepMatcher{re: re}, nil
}

func loadGrepPatterns(ctx context.Context, inv *Invocation, opts grepOptions) ([]string, error) {
	patterns := make([]string, 0, len(opts.patternInputs))
	for _, input := range opts.patternInputs {
		switch input.kind {
		case grepPatternInputText:
			patterns = append(patterns, splitGrepPatternArg(input.value)...)
		case grepPatternInputFile:
			data, _, err := readAllFile(ctx, inv, input.value)
			if err != nil {
				if grepShouldPropagateError(err) {
					return nil, err
				}
				grepPrintFileError(inv, opts, input.value, err)
				return nil, &ExitError{Code: 2}
			}
			patterns = append(patterns, splitGrepPatternFile(string(data))...)
		}
	}
	return patterns, nil
}

func splitGrepPatternArg(value string) []string {
	parts := strings.Split(value, "\n")
	if strings.HasSuffix(value, "\n") && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func splitGrepPatternFile(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "\n")
	if strings.HasSuffix(value, "\n") && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func grepCompilePart(pattern string, mode grepPatternMode) (string, error) {
	switch mode {
	case grepPatternModeBRE:
		return translateBasicRegexp(pattern), nil
	case grepPatternModeERE:
		return pattern, nil
	case grepPatternModeFixed:
		return regexp.QuoteMeta(pattern), nil
	default:
		return "", fmt.Errorf("unsupported grep mode")
	}
}

func grepJoinPatterns(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}

	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString("(?:")
		b.WriteString(part)
		b.WriteByte(')')
	}
	return b.String()
}

func (m grepMatcher) FindAllStringIndex(line string, count int) [][]int {
	if m.re == nil {
		return nil
	}
	return m.re.FindAllStringIndex(line, count)
}

func grepSearchContent(matcher grepMatcher, data []byte, name string, showName bool, opts grepOptions) grepSearchResult {
	lines := textLines(data)

	if opts.count {
		if matcher.re == nil && !opts.invert {
			return grepSearchResult{}
		}

		total := 0
		selectedLines := 0
		countMatches := opts.onlyMatching && !opts.invert
		for _, line := range lines {
			if opts.maxCountSet && selectedLines >= opts.maxCount {
				break
			}
			matches := matcher.FindAllStringIndex(line, -1)
			selected := (len(matches) > 0) != opts.invert
			if !selected {
				continue
			}
			selectedLines++
			if countMatches {
				total += len(matches)
			} else {
				total++
			}
		}

		prefix := ""
		if showName {
			prefix = name + ":"
		}
		return grepSearchResult{
			output:        fmt.Sprintf("%s%d\n", prefix, total),
			matched:       selectedLines > 0,
			selectedLines: selectedLines,
		}
	}

	if opts.beforeContext == 0 && opts.afterContext == 0 {
		outputLines := make([]string, 0)
		selectedLines := 0
		hasMatch := false

		for i, line := range lines {
			if opts.maxCountSet && selectedLines >= opts.maxCount {
				break
			}

			matches := matcher.FindAllStringIndex(line, -1)
			selected := (len(matches) > 0) != opts.invert
			if !selected {
				continue
			}

			hasMatch = true
			selectedLines++
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
			output:        grepJoinOutput(outputLines),
			matched:       hasMatch,
			selectedLines: selectedLines,
		}
	}

	matchingLines := make([]int, 0)
	selectedLines := 0
	for i, line := range lines {
		if opts.maxCountSet && selectedLines >= opts.maxCount {
			break
		}
		matches := matcher.FindAllStringIndex(line, -1)
		if (len(matches) > 0) != opts.invert {
			matchingLines = append(matchingLines, i)
			selectedLines++
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
				matches := matcher.FindAllStringIndex(lines[lineNumber], -1)
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
		output:        grepJoinOutput(outputLines),
		matched:       selectedLines > 0,
		selectedLines: selectedLines,
	}
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

func writeGrepResult(inv *Invocation, matcher grepMatcher, data []byte, name string, showName bool, opts grepOptions, state *grepRunState) error {
	result := grepSearchContent(matcher, data, name, showName, opts)

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

func grepShouldShowFilename(name string, defaultShow bool, opts grepOptions) bool {
	if name == "" {
		return false
	}
	switch opts.filenameMode {
	case grepFilenameWith:
		return true
	case grepFilenameWithout:
		return false
	default:
		return defaultShow
	}
}

func grepShouldPropagateError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func grepNoteFileError(inv *Invocation, opts grepOptions, state *grepRunState, name string, err error) {
	grepPrintFileError(inv, opts, name, err)
	state.hadError = true
}

func grepNoteDirectoryError(inv *Invocation, opts grepOptions, state *grepRunState, name string) {
	if !opts.suppressMessages && inv != nil && inv.Stderr != nil {
		_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: Is a directory\n", name)
	}
	state.hadError = true
}

func grepPrintFileError(inv *Invocation, opts grepOptions, name string, err error) {
	if opts.suppressMessages || inv == nil || inv.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: %s\n", name, readAllErrorText(err))
}

func (c *Grep) enumerateTopLevelPath(ctx context.Context, inv *Invocation, file string, opts grepOptions, state *grepRunState, visitedDirs map[string]struct{}, records *[]grepFileRecord) error {
	linfo, abs, exists, err := lstatMaybe(ctx, inv, file)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, opts, state, file, err)
		return nil
	}
	if !exists {
		grepNoteFileError(inv, opts, state, file, stdfs.ErrNotExist)
		return nil
	}

	info := linfo
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, _, err = statPath(ctx, inv, abs)
		if err != nil {
			if grepShouldPropagateError(err) {
				return err
			}
			grepNoteFileError(inv, opts, state, file, err)
			return nil
		}
	}

	if info.IsDir() {
		if !opts.recursive {
			grepNoteDirectoryError(inv, opts, state, file)
			return nil
		}
		return c.enumerateRecursive(ctx, inv, abs, opts, state, visitedDirs, records)
	}

	*records = append(*records, grepFileRecord{abs: abs})
	return nil
}

func (c *Grep) enumerateRecursive(ctx context.Context, inv *Invocation, currentAbs string, opts grepOptions, state *grepRunState, visitedDirs map[string]struct{}, records *[]grepFileRecord) error {
	linfo, _, err := lstatPath(ctx, inv, currentAbs)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, opts, state, currentAbs, err)
		return nil
	}

	info := linfo
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, _, err = statPath(ctx, inv, currentAbs)
		if err != nil {
			if grepShouldPropagateError(err) {
				return err
			}
			grepNoteFileError(inv, opts, state, currentAbs, err)
			return nil
		}
	}

	if !info.IsDir() {
		*records = append(*records, grepFileRecord{abs: currentAbs})
		return nil
	}

	resolvedDir, err := inv.FS.Realpath(ctx, currentAbs)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, opts, state, currentAbs, err)
		return nil
	}
	if _, seen := visitedDirs[resolvedDir]; seen {
		return nil
	}
	visitedDirs[resolvedDir] = struct{}{}

	entries, err := readDir(ctx, inv, currentAbs)
	if err != nil {
		if grepShouldPropagateError(err) {
			return err
		}
		grepNoteFileError(inv, opts, state, currentAbs, err)
		return nil
	}
	for _, entry := range entries {
		childAbs := path.Join(currentAbs, entry.Name())
		if err := c.enumerateRecursive(ctx, inv, childAbs, opts, state, visitedDirs, records); err != nil {
			return err
		}
	}
	return nil
}

var _ Command = (*Grep)(nil)
var _ SpecProvider = (*Grep)(nil)
var _ ParsedRunner = (*Grep)(nil)
var _ ParseErrorNormalizer = (*Grep)(nil)
