package commands

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"

	"github.com/ewhauser/gbash/policy"
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

	createMode := stdfs.FileMode(0o755)
	if opts.modeSet {
		createMode = opts.mode
	}

	for _, name := range args {
		abs, err := allowPath(ctx, inv, policy.FileActionMkdir, name)
		if err != nil {
			return err
		}

		created, err := mkdirPath(ctx, inv, abs, createMode, opts.parents)
		if err != nil {
			return err
		}
		if opts.modeSet && created {
			if err := inv.FS.Chmod(ctx, abs, opts.mode); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if opts.verbose && created {
			if _, err := fmt.Fprintf(inv.Stdout, "mkdir: created directory %s\n", quoteGNUOperand(name)); err != nil {
				return &ExitError{Code: 1, Err: err}
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

func mkdirPath(ctx context.Context, inv *Invocation, abs string, perm stdfs.FileMode, parents bool) (bool, error) {
	if info, err := inv.FS.Lstat(ctx, abs); err == nil {
		if parents && info.IsDir() {
			return false, nil
		}
		return false, exitf(inv, 1, "mkdir: cannot create directory %q: File exists", abs)
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return false, &ExitError{Code: 1, Err: err}
	}

	if !parents {
		parent := path.Dir(abs)
		info, err := inv.FS.Stat(ctx, parent)
		if err != nil {
			if errors.Is(err, stdfs.ErrNotExist) {
				return false, exitf(inv, 1, "mkdir: cannot create directory %q: No such file or directory", abs)
			}
			return false, &ExitError{Code: 1, Err: err}
		}
		if !info.IsDir() {
			return false, exitf(inv, 1, "mkdir: cannot create directory %q: Not a directory", abs)
		}
	}

	if err := inv.FS.MkdirAll(ctx, abs, perm); err != nil {
		return false, &ExitError{Code: 1, Err: err}
	}
	return true, nil
}

var _ Command = (*Mkdir)(nil)
var _ SpecProvider = (*Mkdir)(nil)
var _ ParsedRunner = (*Mkdir)(nil)
