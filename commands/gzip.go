package commands

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type Gzip struct {
	name string
}

type gzipOptions struct {
	decompress bool
	toStdout   bool
	force      bool
	keep       bool
	test       bool
	verbose    bool
	suffix     string
}

func NewGzip() *Gzip {
	return &Gzip{name: "gzip"}
}

func NewGunzip() *Gzip {
	return &Gzip{name: "gunzip"}
}

func NewZCat() *Gzip {
	return &Gzip{name: "zcat"}
}

func (c *Gzip) Name() string {
	return c.name
}

func (c *Gzip) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Gzip) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.name,
		Usage: c.name + " [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "stdout", Short: 'c', Long: "stdout", Aliases: []string{"to-stdout"}, Help: "write on standard output, keep original files unchanged"},
			{Name: "decompress", Short: 'd', Long: "decompress", Aliases: []string{"uncompress"}, Help: "decompress"},
			{Name: "force", Short: 'f', Long: "force", Help: "overwrite output files"},
			{Name: "keep", Short: 'k', Long: "keep", Help: "keep input files"},
			{Name: "suffix", Short: 'S', Long: "suffix", ValueName: "SUF", Arity: OptionRequiredValue, Help: "use suffix SUF on compressed files"},
			{Name: "test", Short: 't', Long: "test", Help: "test compressed file integrity"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "verbose output"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := io.WriteString(w, gzipHelpText(spec.Name))
			return err
		},
	}
}

func (c *Gzip) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts := c.defaultOptions()
	opts.decompress = opts.decompress || matches.Has("decompress")
	opts.toStdout = opts.toStdout || matches.Has("stdout")
	opts.force = matches.Has("force")
	opts.keep = opts.keep || matches.Has("keep")
	opts.test = matches.Has("test")
	opts.verbose = matches.Has("verbose")
	if matches.Has("suffix") {
		opts.suffix = matches.Value("suffix")
	}
	if opts.suffix == "" {
		return exitf(inv, 1, "%s: suffix must not be empty", c.name)
	}

	inputs := matches.Args("file")
	if len(inputs) == 0 {
		inputs = []string{"-"}
	}

	for _, name := range inputs {
		if err := runGzipItem(ctx, inv, &opts, name, c.name); err != nil {
			return err
		}
	}
	return nil
}

func (c *Gzip) defaultOptions() gzipOptions {
	opts := gzipOptions{
		suffix: ".gz",
	}
	switch c.name {
	case "gunzip":
		opts.decompress = true
	case "zcat":
		opts.decompress = true
		opts.toStdout = true
		opts.keep = true
	}
	return opts
}

func runGzipItem(ctx context.Context, inv *Invocation, opts *gzipOptions, name, commandName string) error {
	reader, sourceAbs, sourceInfo, closeInput, err := openGzipInput(ctx, inv, name)
	if err != nil {
		return err
	}
	defer closeInput()

	if opts.test {
		return gzipTestStream(inv, opts.verbose, name, reader)
	}

	if opts.toStdout || name == "-" {
		if opts.decompress {
			if err := gunzipStream(reader, inv.Stdout); err != nil {
				return exitf(inv, 1, "%s: %v", commandName, err)
			}
		} else {
			if err := gzipStream(inv.Stdout, reader); err != nil {
				return exitf(inv, 1, "%s: %v", commandName, err)
			}
		}
		return nil
	}

	targetAbs, err := resolveGzipOutputPath(inv, opts, sourceAbs)
	if err != nil {
		return err
	}
	if err := ensureParentDirExists(ctx, inv, targetAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionWrite, targetAbs); err != nil {
		return err
	}
	if exists, err := gzipTargetExists(ctx, inv, targetAbs); err != nil {
		return err
	} else if exists {
		if !opts.force {
			return exitf(inv, 1, "%s: %s already exists; use -f to overwrite", commandName, targetAbs)
		}
		if err := inv.FS.Remove(ctx, targetAbs, false); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	perm := stdfs.FileMode(0o644)
	if sourceInfo != nil {
		perm = sourceInfo.Mode().Perm()
	}
	output, err := inv.FS.OpenFile(ctx, targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}

	writeErr := func() error {
		defer func() { _ = output.Close() }()
		if opts.decompress {
			return gunzipStream(reader, output)
		}
		return gzipStream(output, reader)
	}()
	if writeErr != nil {
		return exitf(inv, 1, "%s: %v", commandName, writeErr)
	}
	recordFileMutation(inv.trace, "write", targetAbs, sourceAbs, targetAbs)

	if opts.verbose {
		_, _ = fmt.Fprintf(inv.Stderr, "%s -> %s\n", sourceAbs, targetAbs)
	}
	if !opts.keep {
		if err := inv.FS.Remove(ctx, sourceAbs, false); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func openGzipInput(ctx context.Context, inv *Invocation, name string) (reader io.Reader, abs string, info stdfs.FileInfo, closeFn func(), err error) {
	if name == "-" {
		return inv.Stdin, "-", nil, func() {}, nil
	}
	info, abs, err = statPath(ctx, inv, name)
	if err != nil {
		return nil, "", nil, nil, err
	}
	if info.IsDir() {
		return nil, "", nil, nil, exitf(inv, 1, "gzip: %s: Is a directory", abs)
	}
	file, _, err := openRead(ctx, inv, name)
	if err != nil {
		return nil, "", nil, nil, err
	}
	return file, abs, info, func() { _ = file.Close() }, nil
}

func gzipTestStream(inv *Invocation, verbose bool, displayName string, reader io.Reader) error {
	err := gunzipStream(reader, io.Discard)
	if err != nil {
		return exitf(inv, 1, "gzip: %v", err)
	}
	if verbose && displayName != "-" {
		_, _ = fmt.Fprintf(inv.Stderr, "%s: ok\n", displayName)
	}
	return nil
}

func resolveGzipOutputPath(inv *Invocation, opts *gzipOptions, sourceAbs string) (string, error) {
	if !opts.decompress {
		return sourceAbs + opts.suffix, nil
	}
	if before, ok := strings.CutSuffix(sourceAbs, opts.suffix); ok {
		return before, nil
	}
	return "", exitf(inv, 1, "gzip: %s: unknown suffix -- ignored", path.Base(sourceAbs))
}

func gzipTargetExists(ctx context.Context, inv *Invocation, targetAbs string) (bool, error) {
	_, _, exists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, targetAbs)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func gzipStream(dst io.Writer, src io.Reader) error {
	zw := gzip.NewWriter(dst)
	if _, err := io.Copy(zw, src); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

func gunzipStream(src io.Reader, dst io.Writer) error {
	zr, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	_, err = io.Copy(dst, zr)
	return err
}

func gzipHelpText(commandName string) string {
	return fmt.Sprintf(`%s - gzip-compatible compression inside the gbash sandbox

Usage:
  %s [OPTIONS] [FILE...]

Supported options:
  -c        write to stdout
  -d        decompress
  -f        overwrite output files
  -k        keep input files
  -S SUF    use SUF instead of .gz
  -t        test compressed input
  -v        verbose output
  --help    show this help

Notes:
  - gunzip behaves like %s -d
  - zcat behaves like %s -d -c
  - when no file is provided, stdin/stdout is used
`, commandName, commandName, commandName, commandName)
}

var (
	_ Command      = (*Gzip)(nil)
	_ SpecProvider = (*Gzip)(nil)
	_ ParsedRunner = (*Gzip)(nil)
)
