package commands

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

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

func NewGrep() *Grep {
	return &Grep{}
}

func (c *Grep) Name() string {
	return "grep"
}

func (c *Grep) Run(ctx context.Context, inv *Invocation) error {
	opts, files, err := parseGrepArgs(inv)
	if err != nil {
		return err
	}

	re, err := compileGrepPattern(opts)
	if err != nil {
		return exitf(inv, 2, "grep: invalid pattern: %v", err)
	}

	state := &grepRunState{}
	if len(files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		if err := writeGrepResult(inv, re, data, "", false, opts, state); err != nil {
			return err
		}
	} else {
		showNames := (len(files) > 1 || opts.recursive) && !opts.noFilename
		for _, file := range files {
			if err := c.processPath(ctx, inv, file, re, opts, showNames, state); err != nil {
				return err
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

func parseGrepArgs(inv *Invocation) (grepOptions, []string, error) {
	args := inv.Args
	var opts grepOptions

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}

		switch {
		case arg == "-i" || arg == "--ignore-case":
			opts.ignoreCase = true
		case arg == "-n" || arg == "--line-number":
			opts.lineNumber = true
		case arg == "-v" || arg == "--invert-match":
			opts.invert = true
		case arg == "-c" || arg == "--count":
			opts.count = true
		case arg == "-l" || arg == "--files-with-matches":
			opts.listFiles = true
		case arg == "-L" || arg == "--files-without-match":
			opts.filesWithoutMatch = true
		case arg == "-r" || arg == "-R" || arg == "--recursive":
			opts.recursive = true
		case arg == "-w" || arg == "--word-regexp":
			opts.wordRegexp = true
		case arg == "-x" || arg == "--line-regexp":
			opts.lineRegexp = true
		case arg == "-E" || arg == "--extended-regexp":
		case arg == "-F" || arg == "--fixed-strings":
			opts.fixedStrings = true
		case arg == "-P" || arg == "--perl-regexp":
			opts.perlRegexp = true
		case arg == "-o" || arg == "--only-matching":
			opts.onlyMatching = true
		case arg == "-h" || arg == "--no-filename":
			opts.noFilename = true
		case arg == "-q" || arg == "--quiet" || arg == "--silent":
			opts.quiet = true
		case arg == "-e":
			if len(args) < 2 {
				return grepOptions{}, nil, exitf(inv, 2, "grep: missing pattern")
			}
			opts.pattern = args[1]
			args = args[2:]
			continue
		case arg == "-m":
			if len(args) < 2 {
				return grepOptions{}, nil, exitf(inv, 2, "grep: option requires an argument -- 'm'")
			}
			value, err := parseGrepFlagInt(args[1])
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid max count %q", args[1])
			}
			opts.maxCount = value
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-m") && len(arg) > 2:
			value, err := parseGrepFlagInt(arg[2:])
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid max count %q", arg[2:])
			}
			opts.maxCount = value
		case strings.HasPrefix(arg, "--max-count="):
			value, err := parseGrepFlagInt(strings.TrimPrefix(arg, "--max-count="))
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid max count %q", strings.TrimPrefix(arg, "--max-count="))
			}
			opts.maxCount = value
		case arg == "--max-count":
			if len(args) < 2 {
				return grepOptions{}, nil, exitf(inv, 2, "grep: option requires an argument -- max-count")
			}
			value, err := parseGrepFlagInt(args[1])
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid max count %q", args[1])
			}
			opts.maxCount = value
			args = args[2:]
			continue
		case arg == "-A" || arg == "-B" || arg == "-C":
			if len(args) < 2 {
				return grepOptions{}, nil, exitf(inv, 2, "grep: option requires an argument -- %s", strings.TrimPrefix(arg, "-"))
			}
			value, err := parseGrepFlagInt(args[1])
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", args[1])
			}
			setGrepContext(&opts, arg, value)
			args = args[2:]
			continue
		case len(arg) > 2 && (strings.HasPrefix(arg, "-A") || strings.HasPrefix(arg, "-B") || strings.HasPrefix(arg, "-C")):
			value, err := parseGrepFlagInt(arg[2:])
			if err != nil {
				return grepOptions{}, nil, exitf(inv, 2, "grep: invalid context length %q", arg[2:])
			}
			setGrepContext(&opts, arg[:2], value)
		case len(arg) > 2 && arg[0] == '-' && arg[1] != '-':
			for _, flag := range arg[1:] {
				switch flag {
				case 'i':
					opts.ignoreCase = true
				case 'n':
					opts.lineNumber = true
				case 'v':
					opts.invert = true
				case 'c':
					opts.count = true
				case 'l':
					opts.listFiles = true
				case 'L':
					opts.filesWithoutMatch = true
				case 'r', 'R':
					opts.recursive = true
				case 'w':
					opts.wordRegexp = true
				case 'x':
					opts.lineRegexp = true
				case 'E':
				case 'F':
					opts.fixedStrings = true
				case 'P':
					opts.perlRegexp = true
				case 'o':
					opts.onlyMatching = true
				case 'h':
					opts.noFilename = true
				case 'q':
					opts.quiet = true
				default:
					return grepOptions{}, nil, exitf(inv, 2, "grep: unsupported flag -%c", flag)
				}
			}
		default:
			return grepOptions{}, nil, exitf(inv, 2, "grep: unsupported flag %s", arg)
		}
		args = args[1:]
	}

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

func grepContent(inv *Invocation, re *regexp.Regexp, data []byte, name string, showName bool, opts grepOptions) (bool, error) {
	result, err := grepSearchContent(re, data, name, showName, opts)
	if err != nil {
		return false, err
	}
	if result.output != "" {
		if _, err := fmt.Fprint(inv.Stdout, result.output); err != nil {
			return result.matched, &ExitError{Code: 1, Err: err}
		}
	}
	return result.matched, nil
}

func grepSearchContent(re *regexp.Regexp, data []byte, name string, showName bool, opts grepOptions) (grepSearchResult, error) {
	lines := textLines(data)

	if opts.count {
		matchCount := 0
		countMatches := opts.onlyMatching && !opts.invert
		for _, line := range lines {
			if countMatches {
				matchCount += len(re.FindAllStringIndex(line, -1))
				continue
			}
			matches := re.FindAllStringIndex(line, -1)
			if (len(matches) > 0) != opts.invert {
				matchCount++
				if opts.maxCount > 0 && matchCount >= opts.maxCount {
					break
				}
			}
		}

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

func (c *Grep) processPath(ctx context.Context, inv *Invocation, file string, re *regexp.Regexp, opts grepOptions, showNames bool, state *grepRunState) error {
	info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, file)
	if err != nil {
		return err
	}
	if !exists {
		_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: No such file or directory\n", file)
		state.hadError = true
		return nil
	}

	if info.IsDir() {
		if !opts.recursive {
			_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: Is a directory\n", file)
			state.hadError = true
			return nil
		}
		return c.walkRecursive(ctx, inv, abs, re, opts, showNames, state)
	}

	data, _, err := readAllFile(ctx, inv, abs)
	if err != nil {
		return err
	}
	return writeGrepResult(inv, re, data, abs, showNames, opts, state)
}

func (c *Grep) walkRecursive(ctx context.Context, inv *Invocation, currentAbs string, re *regexp.Regexp, opts grepOptions, showNames bool, state *grepRunState) error {
	if state.quietMatched {
		return nil
	}

	info, _, err := statPath(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		data, _, err := readAllFile(ctx, inv, currentAbs)
		if err != nil {
			return err
		}
		return writeGrepResult(inv, re, data, currentAbs, showNames, opts, state)
	}

	entries, _, err := readDir(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if state.quietMatched {
			return nil
		}
		childAbs := path.Join(currentAbs, entry.Name())
		if err := c.walkRecursive(ctx, inv, childAbs, re, opts, showNames, state); err != nil {
			return err
		}
	}
	return nil
}

var _ Command = (*Grep)(nil)
