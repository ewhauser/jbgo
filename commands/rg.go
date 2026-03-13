package commands

import (
	"bufio"
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type RG struct{}

type rgOptions struct {
	pattern    string
	ignoreCase bool
	lineNumber bool
	count      bool
	listFiles  bool
	hidden     bool
	listOnly   bool
	globs      []string
}

func NewRG() *RG {
	return &RG{}
}

func (c *RG) Name() string {
	return "rg"
}

func (c *RG) Run(ctx context.Context, inv *Invocation) error {
	opts, roots, err := parseRGArgs(inv)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}

	if opts.listOnly {
		files, hadError, _, err := c.collectFiles(ctx, inv, roots, opts)
		if err != nil {
			return err
		}
		for _, file := range files {
			if _, err := fmt.Fprintln(inv.Stdout, file); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if hadError {
			return &ExitError{Code: 2}
		}
		return nil
	}

	re, err := compileGrepPattern(grepOptions{
		pattern:    opts.pattern,
		ignoreCase: opts.ignoreCase,
	})
	if err != nil {
		return exitf(inv, 2, "rg: invalid pattern: %v", err)
	}

	files, hadError, anyDir, err := c.collectFiles(ctx, inv, roots, opts)
	if err != nil {
		return err
	}

	matchedAny := false
	showNames := len(files) > 1 || anyDir || len(roots) > 1
	for _, file := range files {
		if opts.listFiles {
			matched, err := rgFileHasMatch(ctx, inv, re, file)
			if err != nil {
				return err
			}
			if !matched {
				continue
			}
			matchedAny = true
			if _, err := fmt.Fprintln(inv.Stdout, file); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			continue
		}

		data, _, err := readAllFile(ctx, inv, file)
		if err != nil {
			return err
		}
		matched, err := grepContent(inv, re, data, file, showNames, grepOptions{
			pattern:    opts.pattern,
			ignoreCase: opts.ignoreCase,
			lineNumber: opts.lineNumber,
			count:      opts.count,
			listFiles:  opts.listFiles,
		})
		if err != nil {
			return err
		}
		matchedAny = matchedAny || matched
	}

	if hadError {
		return &ExitError{Code: 2}
	}
	if matchedAny {
		return nil
	}
	return &ExitError{Code: 1}
}

func parseRGArgs(inv *Invocation) (rgOptions, []string, error) {
	args := inv.Args
	var opts rgOptions
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		switch arg {
		case "-n":
			opts.lineNumber = true
		case "-i":
			opts.ignoreCase = true
		case "-l":
			opts.listFiles = true
		case "-c":
			opts.count = true
		case "--hidden":
			opts.hidden = true
		case "--files":
			opts.listOnly = true
		case "-g":
			if len(args) < 2 {
				return rgOptions{}, nil, exitf(inv, 2, "rg: missing value for -g")
			}
			opts.globs = append(opts.globs, args[1])
			args = args[2:]
			continue
		default:
			if strings.HasPrefix(arg, "-g") && len(arg) > 2 {
				opts.globs = append(opts.globs, arg[2:])
			} else {
				return rgOptions{}, nil, exitf(inv, 2, "rg: unsupported flag %s", arg)
			}
		}
		args = args[1:]
	}

	if opts.listOnly {
		return opts, args, nil
	}
	if len(args) == 0 {
		return rgOptions{}, nil, exitf(inv, 2, "rg: missing pattern")
	}
	opts.pattern = args[0]
	return opts, args[1:], nil
}

func (c *RG) collectFiles(ctx context.Context, inv *Invocation, roots []string, opts rgOptions) (files []string, hadError, anyDir bool, err error) {
	files = make([]string, 0)
	for _, root := range roots {
		info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, root)
		if err != nil {
			return nil, false, false, err
		}
		if !exists {
			_, _ = fmt.Fprintf(inv.Stderr, "rg: %s: No such file or directory\n", root)
			hadError = true
			continue
		}
		if !info.IsDir() {
			if c.includeFile(path.Base(abs), abs, abs, opts) {
				files = append(files, abs)
			}
			continue
		}
		anyDir = true
		if err := c.walkRoot(ctx, inv, abs, abs, opts, &files); err != nil {
			return nil, false, false, err
		}
	}
	return files, hadError, anyDir, nil
}

func (c *RG) walkRoot(ctx context.Context, inv *Invocation, rootAbs, currentAbs string, opts rgOptions, files *[]string) error {
	entries, _, err := readDir(ctx, inv, currentAbs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !opts.hidden && strings.HasPrefix(name, ".") {
			continue
		}
		child := joinChildPath(currentAbs, name)
		info, err := entry.Info()
		if err != nil {
			info, _, err = statPath(ctx, inv, child)
			if err != nil {
				return err
			}
		}
		if info.IsDir() {
			if err := c.walkRoot(ctx, inv, rootAbs, child, opts, files); err != nil {
				return err
			}
			continue
		}
		if c.includeFile(name, child, rootAbs, opts) {
			*files = append(*files, child)
		}
	}
	return nil
}

func (c *RG) includeFile(name, abs, rootAbs string, opts rgOptions) bool {
	if len(opts.globs) == 0 {
		return true
	}
	rel := strings.TrimPrefix(abs, rootAbs)
	rel = strings.TrimPrefix(rel, "/")
	for _, glob := range opts.globs {
		if matched, _ := path.Match(glob, name); matched {
			return true
		}
		if rel != "" {
			if matched, _ := path.Match(glob, rel); matched {
				return true
			}
		}
	}
	return false
}

func rgFileHasMatch(ctx context.Context, inv *Invocation, re *regexp.Regexp, name string) (bool, error) {
	file, _, err := openRead(ctx, inv, name)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	var buf [4 * 1024]byte
	scanner.Buffer(buf[:], ScannerTokenLimit(inv))
	for scanner.Scan() {
		if re.Match(scanner.Bytes()) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, &ExitError{Code: 1, Err: err}
	}
	return false, nil
}

var _ Command = (*RG)(nil)
