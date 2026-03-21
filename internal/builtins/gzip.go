package builtins

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"runtime"
	"strings"
)

type Gzip struct {
	name string
}

type gzipOptions struct {
	decompress bool
	toStdout   bool
	force      bool
	keep       bool
	quiet      bool
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
			{Name: "quiet", Short: 'q', Long: "quiet", Aliases: []string{"silent"}, HelpAliases: []string{"silent"}, Help: "suppress all warnings"},
			{Name: "suffix", Short: 'S', Long: "suffix", ValueName: "SUF", Arity: OptionRequiredValue, Help: "use suffix SUF on compressed files"},
			{Name: "test", Short: 't', Long: "test", Help: "test compressed file integrity"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "verbose output"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
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
	opts.quiet = matches.Has("quiet")
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

func gzipMaybeQuietError(err error, quiet bool) error {
	if err == nil || !quiet || runtime.GOOS != "darwin" {
		return err
	}
	if code, ok := ExitCode(err); ok {
		return &ExitError{Code: code}
	}
	return &ExitError{Code: 1}
}

func gzipExitf(inv *Invocation, quiet bool, format string, args ...any) error {
	if quiet && runtime.GOOS == "darwin" {
		return &ExitError{Code: 1}
	}
	return exitf(inv, 1, format, args...)
}

func runGzipItem(ctx context.Context, inv *Invocation, opts *gzipOptions, name, commandName string) error {
	reader, sourceAbs, sourceInfo, closeInput, err := openGzipInput(ctx, inv, name, opts)
	if err != nil {
		return gzipMaybeQuietError(err, opts.quiet)
	}
	defer closeInput()

	if opts.test {
		return gzipMaybeQuietError(gzipTestStream(inv, opts.verbose, name, reader), opts.quiet)
	}

	if opts.toStdout || name == "-" {
		if opts.decompress {
			if err := gunzipStream(reader, inv.Stdout); err != nil {
				return gzipExitf(inv, opts.quiet, "%s: %v", commandName, err)
			}
		} else {
			if err := gzipStream(inv.Stdout, reader); err != nil {
				return gzipExitf(inv, opts.quiet, "%s: %v", commandName, err)
			}
		}
		return nil
	}

	targetAbs, err := resolveGzipOutputPath(inv, opts, sourceAbs)
	if err != nil {
		return gzipMaybeQuietError(err, opts.quiet)
	}
	if err := ensureParentDirExists(ctx, inv, targetAbs); err != nil {
		return gzipMaybeQuietError(err, opts.quiet)
	}
	if exists, err := gzipTargetExists(ctx, inv, targetAbs); err != nil {
		return gzipMaybeQuietError(err, opts.quiet)
	} else if exists {
		if !opts.force {
			return gzipExitf(inv, opts.quiet, "%s: %s already exists; use -f to overwrite", commandName, targetAbs)
		}
		if err := inv.FS.Remove(ctx, targetAbs, false); err != nil {
			return gzipMaybeQuietError(&ExitError{Code: 1, Err: err}, opts.quiet)
		}
	}

	perm := stdfs.FileMode(0o644)
	if sourceInfo != nil {
		perm = sourceInfo.Mode().Perm()
	}
	output, err := inv.FS.OpenFile(ctx, targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return gzipMaybeQuietError(&ExitError{Code: 1, Err: err}, opts.quiet)
	}

	writeErr := func() error {
		defer func() { _ = output.Close() }()
		if opts.decompress {
			return gunzipStream(reader, output)
		}
		return gzipStream(output, reader)
	}()
	if writeErr != nil {
		return gzipExitf(inv, opts.quiet, "%s: %v", commandName, writeErr)
	}
	recordFileMutation(inv.TraceRecorder(), "write", targetAbs, sourceAbs, targetAbs)

	if opts.verbose {
		_, _ = fmt.Fprintf(inv.Stderr, "%s -> %s\n", sourceAbs, targetAbs)
	}
	if !opts.keep {
		if err := inv.FS.Remove(ctx, sourceAbs, false); err != nil {
			return gzipMaybeQuietError(&ExitError{Code: 1, Err: err}, opts.quiet)
		}
	}
	return nil
}

func openGzipInput(ctx context.Context, inv *Invocation, name string, opts *gzipOptions) (reader io.Reader, abs string, info stdfs.FileInfo, closeFn func(), err error) {
	if name == "-" {
		return inv.Stdin, "-", nil, func() {}, nil
	}

	candidates := gzipInputCandidates(name, opts)
	lastCandidate := name
	for i, candidate := range candidates {
		lastCandidate = candidate
		info, abs, err = statPath(ctx, inv, candidate)
		if err != nil {
			if i+1 < len(candidates) && gzipIsNotExist(err) {
				continue
			}
			return nil, "", nil, nil, gzipInputError(inv, opts.quiet, candidate, err)
		}
		if info.IsDir() {
			return nil, "", nil, nil, gzipExitf(inv, opts.quiet, "gzip: %s: Is a directory", abs)
		}
		file, _, err := openRead(ctx, inv, candidate)
		if err != nil {
			if i+1 < len(candidates) && gzipIsNotExist(err) {
				continue
			}
			return nil, "", nil, nil, gzipInputError(inv, opts.quiet, candidate, err)
		}
		return file, abs, info, func() { _ = file.Close() }, nil
	}
	return nil, "", nil, nil, gzipInputError(inv, opts.quiet, lastCandidate, stdfs.ErrNotExist)
}

func gzipInputCandidates(name string, opts *gzipOptions) []string {
	if name == "-" || opts == nil || !opts.decompress {
		return []string{name}
	}
	if name == "" {
		return []string{opts.suffix}
	}
	if strings.HasSuffix(name, opts.suffix) {
		return []string{name}
	}
	return []string{name, name + opts.suffix}
}

func gzipIsNotExist(err error) bool {
	return errors.Is(err, stdfs.ErrNotExist) || os.IsNotExist(err)
}

func gzipInputError(inv *Invocation, quiet bool, name string, err error) error {
	if gzipIsNotExist(err) {
		return gzipExitf(inv, quiet, "gzip: %s: No such file or directory", name)
	}
	return gzipExitf(inv, quiet, "gzip: %s: %v", name, err)
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
	return "", gzipExitf(inv, opts.quiet, "gzip: %s: unknown suffix -- ignored", path.Base(sourceAbs))
}

func gzipTargetExists(ctx context.Context, inv *Invocation, targetAbs string) (bool, error) {
	_, _, exists, err := lstatMaybe(ctx, inv, targetAbs)
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
