package commands

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

type Grep struct{}

type grepOptions struct {
	pattern    string
	ignoreCase bool
	lineNumber bool
	invert     bool
	count      bool
	listFiles  bool
	recursive  bool
	wordRegexp bool
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

	matchedAny := false
	hadError := false

	if len(files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		matched, err := grepContent(inv, re, data, "", false, opts)
		if err != nil {
			return err
		}
		matchedAny = matchedAny || matched
	} else {
		showNames := len(files) > 1 || opts.recursive
		for _, file := range files {
			info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, file)
			if err != nil {
				return err
			}
			if !exists {
				_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: No such file or directory\n", file)
				hadError = true
				continue
			}

			if info.IsDir() {
				if !opts.recursive {
					_, _ = fmt.Fprintf(inv.Stderr, "grep: %s: Is a directory\n", file)
					hadError = true
					continue
				}
				err := c.walkRecursive(ctx, inv, abs, re, opts, showNames, &matchedAny)
				if err != nil {
					return err
				}
				continue
			}

			data, _, err := readAllFile(ctx, inv, abs)
			if err != nil {
				return err
			}
			matched, err := grepContent(inv, re, data, abs, showNames, opts)
			if err != nil {
				return err
			}
			matchedAny = matchedAny || matched
		}
	}

	if hadError {
		return &ExitError{Code: 2}
	}
	if matchedAny {
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

		switch arg {
		case "-i", "--ignore-case":
			opts.ignoreCase = true
		case "-n", "--line-number":
			opts.lineNumber = true
		case "-v", "--invert-match":
			opts.invert = true
		case "-c", "--count":
			opts.count = true
		case "-l", "--files-with-matches":
			opts.listFiles = true
		case "-r", "-R":
			opts.recursive = true
		case "-w", "--word-regexp":
			opts.wordRegexp = true
		case "-E", "--extended-regexp":
		case "-e":
			if len(args) < 2 {
				return grepOptions{}, nil, exitf(inv, 2, "grep: missing pattern")
			}
			opts.pattern = args[1]
			args = args[2:]
			continue
		default:
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 2 {
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
					case 'r', 'R':
						opts.recursive = true
					case 'w':
						opts.wordRegexp = true
					case 'E':
					default:
						return grepOptions{}, nil, exitf(inv, 2, "grep: unsupported flag -%c", flag)
					}
				}
			} else {
				return grepOptions{}, nil, exitf(inv, 2, "grep: unsupported flag %s", arg)
			}
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

func compileGrepPattern(opts grepOptions) (*regexp.Regexp, error) {
	pattern := opts.pattern
	if opts.wordRegexp {
		pattern = `\b(?:` + pattern + `)\b`
	}
	if opts.ignoreCase {
		pattern = `(?i)` + pattern
	}
	return regexp.Compile(pattern)
}

func grepContent(inv *Invocation, re *regexp.Regexp, data []byte, name string, showName bool, opts grepOptions) (bool, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	lineNo := 0
	matchCount := 0
	matchedAny := false

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		matched := re.MatchString(line)
		if opts.invert {
			matched = !matched
		}
		if !matched {
			continue
		}

		matchedAny = true
		matchCount++
		if opts.listFiles {
			_, err := fmt.Fprintln(inv.Stdout, name)
			return true, err
		}
		if opts.count {
			continue
		}

		prefix := ""
		if showName {
			prefix += name + ":"
		}
		if opts.lineNumber {
			prefix += fmt.Sprintf("%d:", lineNo)
		}
		if _, err := fmt.Fprintf(inv.Stdout, "%s%s\n", prefix, line); err != nil {
			return matchedAny, &ExitError{Code: 1, Err: err}
		}
	}
	if err := scanner.Err(); err != nil {
		return matchedAny, &ExitError{Code: 1, Err: err}
	}

	if opts.count {
		if showName {
			_, err := fmt.Fprintf(inv.Stdout, "%s:%d\n", name, matchCount)
			if err != nil {
				return matchedAny, &ExitError{Code: 1, Err: err}
			}
		} else {
			_, err := fmt.Fprintf(inv.Stdout, "%d\n", matchCount)
			if err != nil {
				return matchedAny, &ExitError{Code: 1, Err: err}
			}
		}
	}

	return matchedAny, nil
}

func (c *Grep) walkRecursive(ctx context.Context, inv *Invocation, currentAbs string, re *regexp.Regexp, opts grepOptions, showNames bool, matchedAny *bool) error {
	info, _, err := statPath(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		data, _, err := readAllFile(ctx, inv, currentAbs)
		if err != nil {
			return err
		}
		matched, err := grepContent(inv, re, data, currentAbs, showNames, opts)
		if err != nil {
			return err
		}
		*matchedAny = *matchedAny || matched
		return nil
	}

	entries, _, err := readDir(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		childAbs := path.Join(currentAbs, entry.Name())
		if err := c.walkRecursive(ctx, inv, childAbs, re, opts, showNames, matchedAny); err != nil {
			return err
		}
	}
	return nil
}

var _ Command = (*Grep)(nil)
