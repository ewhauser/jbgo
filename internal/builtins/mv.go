package builtins

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"strings"
)

type MV struct{}

func NewMV() *MV {
	return &MV{}
}

func (c *MV) Name() string {
	return "mv"
}

func (c *MV) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *MV) Spec() CommandSpec {
	return CommandSpec{
		Name:  "mv",
		About: "Rename SOURCE to DEST, or move SOURCE(s) to DIRECTORY.",
		Usage: "mv [OPTION]... SOURCE DEST\n" +
			"       mv [OPTION]... SOURCE... DIRECTORY\n" +
			"       mv [OPTION]... -t DIRECTORY SOURCE...",
		Options: []OptionSpec{
			{Name: "force", Short: 'f', Long: "force", Help: "do not prompt before overwriting"},
			{Name: "no-clobber", Short: 'n', Long: "no-clobber", Help: "do not overwrite an existing file"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "explain what is being done"},
			{Name: "target-directory", Short: 't', Long: "target-directory", Arity: OptionRequiredValue, ValueName: "DIRECTORY", Help: "move all SOURCE arguments into DIRECTORY"},
			{Name: "no-target-directory", Short: 'T', Long: "no-target-directory", Help: "treat DEST as a normal file"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
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

func (c *MV) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, args, err := parseMVMatches(inv, matches)
	if err != nil {
		return err
	}

	if len(args) < 2 {
		return exitf(inv, 1, "mv: missing destination file operand")
	}

	sources := args[:len(args)-1]
	destArg := args[len(args)-1]
	multipleSources := len(sources) > 1

	for _, source := range sources {
		srcInfo, srcAbs, err := statPath(ctx, inv, source)
		if err != nil {
			return exitf(inv, 1, "mv: cannot stat %q: No such file or directory", source)
		}

		destAbs, _, _, err := resolveDestination(ctx, inv, srcAbs, destArg, multipleSources)
		if err != nil {
			return err
		}
		destInfo, _, destExists, err := statMaybe(ctx, inv, destAbs)
		if err != nil {
			return err
		}
		if opts.noClobber && destExists {
			continue
		}
		if srcInfo.IsDir() && isWithinMovedTree(srcAbs, destAbs) {
			return exitf(inv, 1, "mv: cannot move %q into itself", source)
		}
		if err := ensureParentDirExists(ctx, inv, destAbs); err != nil {
			return err
		}
		if destExists {
			if err := inv.FS.Remove(ctx, destAbs, destInfo != nil && destInfo.IsDir()); err != nil && !isNotExist(err) {
				return &ExitError{Code: 1, Err: err}
			}
		}

		if err := inv.FS.Rename(ctx, srcAbs, destAbs); err != nil {
			if isExists(err) {
				if err := inv.FS.Remove(ctx, destAbs, isDirInfo(destInfo)); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				if err := inv.FS.Rename(ctx, srcAbs, destAbs); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
				continue
			}
			return &ExitError{Code: 1, Err: err}
		}
		if opts.verbose {
			if _, err := fmt.Fprintf(inv.Stdout, "renamed '%s' -> '%s'\n", source, destAbs); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	return nil
}

type mvOptions struct {
	force     bool
	noClobber bool
	verbose   bool
	targetDir string
	noTarget  bool
}

func parseMVMatches(inv *Invocation, matches *ParsedCommand) (mvOptions, []string, error) {
	opts := mvOptions{}
	for _, name := range matches.OptionOrder() {
		switch name {
		case "force":
			opts.force = true
			opts.noClobber = false
		case "no-clobber":
			opts.noClobber = true
			opts.force = false
		case "verbose":
			opts.verbose = true
		case "target-directory":
			opts.targetDir = matches.Value("target-directory")
		case "no-target-directory":
			opts.noTarget = true
		}
	}
	args := matches.Args("file")
	if opts.targetDir != "" {
		if opts.noTarget {
			return mvOptions{}, nil, commandUsageError(inv, "mv", "cannot combine --target-directory and --no-target-directory")
		}
		args = append(append([]string(nil), args...), opts.targetDir)
	}
	return opts, args, nil
}

func isNotExist(err error) bool {
	return err != nil && strings.Contains(err.Error(), stdfs.ErrNotExist.Error())
}

func isExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), stdfs.ErrExist.Error())
}

func isDirInfo(info stdfs.FileInfo) bool {
	return info != nil && info.IsDir()
}

func isWithinMovedTree(srcAbs, destAbs string) bool {
	return destAbs == srcAbs || len(destAbs) > len(srcAbs) && destAbs[:len(srcAbs)] == srcAbs && destAbs[len(srcAbs)] == '/'
}

var _ Command = (*MV)(nil)
var _ SpecProvider = (*MV)(nil)
var _ ParsedRunner = (*MV)(nil)
