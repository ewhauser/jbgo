package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Mkdir struct{}

type mkdirOptions struct {
	parents bool
	mode    stdfs.FileMode
	modeSet bool
	verbose bool
}

func NewMkdir() *Mkdir {
	return &Mkdir{}
}

func (c *Mkdir) Name() string {
	return "mkdir"
}

func (c *Mkdir) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Mkdir) Spec() CommandSpec {
	return CommandSpec{
		Name:      "mkdir",
		About:     "Create the given DIRECTORY(ies) if they do not exist",
		Usage:     "mkdir [OPTION]... DIRECTORY...",
		AfterHelp: "Each MODE is of the form [ugoa]*([-+=]([rwxXst]*|[ugo]))+|[-+=]?[0-7]+.",
		Options: []OptionSpec{
			{Name: "mode", Short: 'm', Long: "mode", Arity: OptionRequiredValue, ValueName: "MODE", Help: "set file mode (not implemented on windows)"},
			{Name: "parents", Short: 'p', Long: "parents", Help: "make parent directories as needed"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "print a message for each printed directory"},
			{Name: "selinux", Short: 'Z', Help: "set SELinux security context of each created directory to the default type"},
			{Name: "context", Long: "context", Arity: OptionRequiredValue, ValueName: "CTX", Help: "like -Z, or if CTX is specified then set the SELinux or SMACK security context to CTX"},
		},
		Args: []ArgSpec{
			{Name: "directory", ValueName: "DIRECTORY", Repeatable: true, Required: true},
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

func (c *Mkdir) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, args, err := parseMkdirMatches(inv, matches)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return exitf(inv, 1, "mkdir: missing operand")
	}

	defaultMode := mkdirDefaultMode(inv)

	for _, name := range args {
		created, err := mkdirPath(ctx, inv, name, defaultMode, opts)
		if err != nil {
			return err
		}
		if opts.verbose {
			for _, abs := range created {
				if _, err := fmt.Fprintf(inv.Stdout, "mkdir: created directory %s\n", quoteGNUOperand(mkdirVerbosePath(inv, name, abs))); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
		}
	}

	return nil
}

func parseMkdirMatches(inv *Invocation, matches *ParsedCommand) (mkdirOptions, []string, error) {
	opts := mkdirOptions{
		parents: matches.Has("parents"),
		verbose: matches.Has("verbose"),
	}
	if matches.Has("mode") {
		mode, err := parseMkdirMode(inv, matches.Value("mode"))
		if err != nil {
			return mkdirOptions{}, nil, exitf(inv, 1, "mkdir: invalid mode %q", matches.Value("mode"))
		}
		opts.mode = mode
		opts.modeSet = true
	}
	return opts, matches.Args("directory"), nil
}

func parseMkdirMode(inv *Invocation, spec string) (stdfs.FileMode, error) {
	mode, err := computeChmodMode(inv, stdfs.ModeDir|0o777, spec)
	if err != nil {
		return 0, err
	}
	return mode & (stdfs.ModePerm | stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky), nil
}

func mkdirDefaultMode(inv *Invocation) stdfs.FileMode {
	return 0o777 &^ chmodCurrentUmask(inv)
}

func mkdirCompatUsesProcessUmask(inv *Invocation) bool {
	return inv != nil && inv.Env != nil && strings.TrimSpace(inv.Env["GBASH_COMPAT_ROOT"]) != ""
}

func mkdirPath(ctx context.Context, inv *Invocation, raw string, defaultMode stdfs.FileMode, opts mkdirOptions) ([]string, error) {
	rawAbs, abs := mkdirResolveOperand(inv, raw)
	if opts.parents {
		return mkdirParentsPath(ctx, inv, rawAbs, abs, defaultMode, opts)
	}
	return mkdirSinglePath(ctx, inv, abs, defaultMode, opts)
}

func mkdirResolveOperand(inv *Invocation, raw string) (string, string) {
	raw = remapCompatHostPath(inv, raw)
	if raw == "" {
		raw = "."
	}
	if strings.HasPrefix(raw, "/") {
		return raw, gbfs.Clean(raw)
	}
	cwd := "/"
	switch {
	case inv != nil && inv.FS != nil:
		cwd = inv.FS.Getwd()
	case inv != nil && inv.Cwd != "":
		cwd = gbfs.Clean(inv.Cwd)
	}
	if cwd == "/" {
		raw = "/" + raw
	} else {
		raw = cwd + "/" + raw
	}
	return raw, gbfs.Clean(raw)
}

func mkdirSinglePath(ctx context.Context, inv *Invocation, abs string, defaultMode stdfs.FileMode, opts mkdirOptions) ([]string, error) {
	if _, err := inv.FS.Lstat(ctx, abs); err == nil {
		return nil, exitf(inv, 1, "mkdir: cannot create directory %q: File exists", abs)
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return nil, &ExitError{Code: 1, Err: err}
	}

	parent := path.Dir(abs)
	info, err := inv.FS.Stat(ctx, parent)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, exitf(inv, 1, "mkdir: cannot create directory %q: No such file or directory", abs)
		}
		return nil, &ExitError{Code: 1, Err: err}
	}
	if !info.IsDir() {
		return nil, exitf(inv, 1, "mkdir: cannot create directory %q: Not a directory", abs)
	}

	if err := inv.FS.MkdirAll(ctx, abs, 0o777); err != nil {
		return nil, &ExitError{Code: 1, Err: err}
	}
	mode := defaultMode
	if opts.modeSet {
		mode = opts.mode
	}
	if opts.modeSet || !mkdirCompatUsesProcessUmask(inv) {
		if err := inv.FS.Chmod(ctx, abs, mode); err != nil {
			return nil, &ExitError{Code: 1, Err: err}
		}
	}
	return []string{abs}, nil
}

func mkdirParentsPath(ctx context.Context, inv *Invocation, rawAbs, abs string, defaultMode stdfs.FileMode, opts mkdirOptions) ([]string, error) {
	current := "/"
	created := make([]string, 0)

	for part := range strings.SplitSeq(rawAbs, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			current = path.Dir(current)
			continue
		}

		next := gbfs.Resolve(current, part)
		info, err := inv.FS.Stat(ctx, next)
		if err == nil {
			if !info.IsDir() {
				if next == abs {
					return nil, exitf(inv, 1, "mkdir: cannot create directory %q: File exists", next)
				}
				return nil, exitf(inv, 1, "mkdir: cannot create directory %q: Not a directory", next)
			}
			current = next
			continue
		}
		if !errors.Is(err, stdfs.ErrNotExist) {
			return nil, &ExitError{Code: 1, Err: err}
		}
		if _, lstatErr := inv.FS.Lstat(ctx, next); lstatErr == nil {
			return nil, exitf(inv, 1, "mkdir: cannot create directory %q: No such file or directory", next)
		} else if !errors.Is(lstatErr, stdfs.ErrNotExist) {
			return nil, &ExitError{Code: 1, Err: lstatErr}
		}

		if err := inv.FS.MkdirAll(ctx, next, 0o777); err != nil {
			return nil, &ExitError{Code: 1, Err: err}
		}

		applyMode := true
		mode := defaultMode
		switch {
		case next != abs:
			mode |= 0o300
		case opts.modeSet:
			mode = opts.mode
		case mkdirCompatUsesProcessUmask(inv):
			applyMode = false
		}
		created = append(created, next)
		if applyMode {
			if err := inv.FS.Chmod(ctx, next, mode); err != nil {
				return nil, &ExitError{Code: 1, Err: err}
			}
		}
		current = next
	}
	return created, nil
}

func mkdirVerbosePath(inv *Invocation, raw, abs string) string {
	raw = remapCompatHostPath(inv, raw)
	if raw == "" {
		raw = "."
	}
	if strings.HasPrefix(raw, "/") {
		return abs
	}
	cwd := "/"
	if inv != nil && inv.FS != nil {
		cwd = inv.FS.Getwd()
	}
	current := cwd
	display := ""
	for part := range strings.SplitSeq(raw, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			current = path.Dir(current)
			if display == "" {
				display = ".."
			} else {
				display += "/.."
			}
		default:
			current = gbfs.Resolve(current, part)
			if display == "" {
				display = part
			} else {
				display += "/" + part
			}
		}
		if current == abs && display != "" {
			return display
		}
	}
	if display != "" {
		return display
	}
	if cwd == "/" {
		return strings.TrimPrefix(abs, "/")
	}
	return strings.TrimPrefix(abs, cwd+"/")
}

var _ Command = (*Mkdir)(nil)
var _ SpecProvider = (*Mkdir)(nil)
var _ ParsedRunner = (*Mkdir)(nil)
