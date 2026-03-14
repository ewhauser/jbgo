package builtins

import (
	"context"
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type CP struct{}

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
			{Name: "force", Short: 'f', Long: "force", Help: "overwrite an existing destination file"},
			{Name: "recursive", Short: 'r', ShortAliases: []rune{'R'}, Long: "recursive", Help: "copy directories recursively"},
			{Name: "no-clobber", Short: 'n', Long: "no-clobber", Help: "do not overwrite an existing file"},
			{Name: "preserve", Short: 'p', Long: "preserve", Help: "preserve mode bits"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "explain what is being done"},
		},
		Args: []ArgSpec{
			{Name: "source", ValueName: "SOURCE", Repeatable: true, Help: "source paths followed by destination"},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
	}
}

func (c *CP) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts := parseCPMatches(matches)
	args := matches.Positionals()

	if len(args) < 2 {
		return exitf(inv, 1, "cp: missing destination file operand")
	}

	sources := args[:len(args)-1]
	destArg := args[len(args)-1]
	multipleSources := len(sources) > 1

	for _, source := range sources {
		srcInfo, srcAbs, err := statPath(ctx, inv, source)
		if err != nil {
			return exitf(inv, 1, "cp: cannot stat %q: No such file or directory", source)
		}

		destAbs, _, _, err := resolveDestination(ctx, inv, srcAbs, destArg, multipleSources)
		if err != nil {
			return err
		}
		_, _, destExists, err := statMaybe(ctx, inv, policy.FileActionStat, destAbs)
		if err != nil {
			return err
		}
		if opts.noClobber && destExists {
			continue
		}

		if srcInfo.IsDir() {
			if !opts.recursive {
				return exitf(inv, 1, "cp: omitting directory %q", source)
			}
			if destAbs == srcAbs || strings.HasPrefix(destAbs, srcAbs+"/") {
				return exitf(inv, 1, "cp: cannot copy %q into itself", source)
			}
			if err := copyTree(ctx, inv, srcAbs, destAbs); err != nil {
				return err
			}
			continue
		}

		if err := copyFileContents(ctx, inv, srcAbs, destAbs, srcInfo.Mode().Perm()); err != nil {
			return err
		}

		if opts.verbose {
			if _, err := fmt.Fprintf(inv.Stdout, "'%s' -> '%s'\n", source, destAbs); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	return nil
}

type cpOptions struct {
	recursive bool
	noClobber bool
	preserve  bool
	verbose   bool
}

func parseCPMatches(matches *ParsedCommand) cpOptions {
	opts := cpOptions{}
	if matches == nil {
		return opts
	}
	for _, name := range matches.OptionOrder() {
		switch name {
		case "archive":
			opts.recursive = true
			opts.preserve = true
		case "recursive":
			opts.recursive = true
		case "no-clobber":
			opts.noClobber = true
		case "preserve":
			opts.preserve = true
		case "verbose":
			opts.verbose = true
		case "force":
			// Overwrite is already the default sandbox behavior; accept force for GNU compatibility.
		}
	}
	return opts
}

var _ Command = (*CP)(nil)
var _ SpecProvider = (*CP)(nil)
var _ ParsedRunner = (*CP)(nil)
