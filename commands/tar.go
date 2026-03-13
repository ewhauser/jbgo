package commands

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"sort"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

type Tar struct{}

type tarMode string

const (
	tarModeCreate  tarMode = "create"
	tarModeExtract tarMode = "extract"
	tarModeList    tarMode = "list"
)

type tarOptions struct {
	mode     tarMode
	archive  string
	chdir    string
	gzip     bool
	verbose  bool
	toStdout bool
	keepOld  bool
}

func NewTar() *Tar {
	return &Tar{}
}

func (c *Tar) Name() string {
	return "tar"
}

func (c *Tar) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Tar) Spec() CommandSpec {
	return CommandSpec{
		Name:  "tar",
		About: "Archive helper inside the gbash sandbox.",
		Usage: "tar -c[f] ARCHIVE [PATH...]\n  tar -x[f] ARCHIVE\n  tar -t[f] ARCHIVE",
		Options: []OptionSpec{
			{Name: "create", Short: 'c', Long: "create", Help: "create archive"},
			{Name: "extract", Short: 'x', Long: "extract", Help: "extract archive"},
			{Name: "list", Short: 't', Long: "list", Help: "list archive contents"},
			{Name: "file", Short: 'f', Long: "file", Arity: OptionRequiredValue, ValueName: "ARCHIVE", Help: "read/write ARCHIVE instead of stdin/stdout"},
			{Name: "directory", Short: 'C', Arity: OptionRequiredValue, ValueName: "DIR", Help: "use DIR as create base or extract destination"},
			{Name: "gzip", Short: 'z', Long: "gzip", Help: "gzip-compress or gzip-decompress the archive stream"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "verbose entry listing to stderr"},
			{Name: "to-stdout", Short: 'O', Long: "to-stdout", Help: "write extracted file contents to stdout"},
			{Name: "keep-old", Short: 'k', Long: "keep-old-files", Help: "keep existing files on extract"},
		},
		Args: []ArgSpec{
			{Name: "path", ValueName: "PATH", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
		},
		HelpRenderer: renderStaticHelp(tarHelpText),
	}
}

func (c *Tar) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseTarMatches(inv, matches)
	if err != nil {
		return err
	}
	operands := matches.Args("path")

	switch opts.mode {
	case tarModeCreate:
		if len(operands) == 0 {
			return exitf(inv, 1, "tar: Cowardly refusing to create an empty archive")
		}
		return runTarCreate(ctx, inv, &opts, operands)
	case tarModeExtract:
		return runTarExtract(ctx, inv, &opts)
	case tarModeList:
		return runTarList(ctx, inv, &opts)
	default:
		return exitf(inv, 1, "tar: you must specify one of -c, -x, or -t")
	}
}

func parseTarMatches(inv *Invocation, matches *ParsedCommand) (tarOptions, error) {
	opts := tarOptions{
		archive: "-",
	}
	if matches.Has("create") {
		if err := tarSetMode(&opts, tarModeCreate, inv); err != nil {
			return tarOptions{}, err
		}
	}
	if matches.Has("extract") {
		if err := tarSetMode(&opts, tarModeExtract, inv); err != nil {
			return tarOptions{}, err
		}
	}
	if matches.Has("list") {
		if err := tarSetMode(&opts, tarModeList, inv); err != nil {
			return tarOptions{}, err
		}
	}
	if opts.mode == "" {
		return tarOptions{}, exitf(inv, 1, "tar: you must specify one of -c, -x, or -t")
	}
	if matches.Has("file") {
		opts.archive = matches.Value("file")
	}
	if matches.Has("directory") {
		opts.chdir = matches.Value("directory")
	}
	opts.gzip = matches.Has("gzip")
	opts.verbose = matches.Has("verbose")
	opts.toStdout = matches.Has("to-stdout")
	opts.keepOld = matches.Has("keep-old")
	if opts.toStdout && opts.mode != tarModeExtract {
		return tarOptions{}, exitf(inv, 1, "tar: -O is only supported with -x")
	}
	return opts, nil
}

func tarSetMode(opts *tarOptions, mode tarMode, inv *Invocation) error {
	if opts.mode != "" && opts.mode != mode {
		return exitf(inv, 1, "tar: options -c, -x, and -t are mutually exclusive")
	}
	opts.mode = mode
	return nil
}

func runTarCreate(ctx context.Context, inv *Invocation, opts *tarOptions, operands []string) error {
	baseDir, err := tarBaseDir(ctx, inv, opts)
	if err != nil {
		return err
	}
	tw, closeArchive, err := openTarWriter(ctx, inv, opts)
	if err != nil {
		return err
	}

	for _, operand := range operands {
		sourceAbs := gbfs.Resolve(baseDir, operand)
		info, _, err := lstatPath(ctx, inv, sourceAbs)
		if err != nil {
			return err
		}
		entryName := tarArchiveName(operand, sourceAbs)
		if entryName == "" {
			entryName = path.Base(sourceAbs)
		}
		if err := tarWritePath(ctx, inv, tw, sourceAbs, entryName, info); err != nil {
			_ = closeArchive()
			return err
		}
	}
	return closeArchive()
}

func runTarList(ctx context.Context, inv *Invocation, opts *tarOptions) error {
	tr, closeArchive, err := openTarReader(ctx, inv, opts)
	if err != nil {
		return err
	}
	defer func() { _ = closeArchive() }()

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return exitf(inv, 1, "tar: %v", err)
		}
		name, err := sanitizeTarEntryName(header.Name)
		if err != nil {
			return exitf(inv, 1, "tar: %v", err)
		}
		if name == "" {
			continue
		}
		if _, err := fmt.Fprintln(inv.Stdout, name); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
}

func runTarExtract(ctx context.Context, inv *Invocation, opts *tarOptions) error {
	baseDir, err := tarBaseDir(ctx, inv, opts)
	if err != nil {
		return err
	}
	tr, closeArchive, err := openTarReader(ctx, inv, opts)
	if err != nil {
		return err
	}
	defer func() { _ = closeArchive() }()

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return exitf(inv, 1, "tar: %v", err)
		}
		entryName, err := sanitizeTarEntryName(header.Name)
		if err != nil {
			return exitf(inv, 1, "tar: %v", err)
		}
		if entryName == "" {
			continue
		}

		if opts.verbose {
			_, _ = fmt.Fprintln(inv.Stderr, entryName)
		}
		if opts.toStdout {
			if tarIsRegularType(header.Typeflag) {
				if _, err := io.Copy(inv.Stdout, tr); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			continue
		}

		targetAbs := gbfs.Resolve(baseDir, entryName)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := tarEnsureDir(ctx, inv, targetAbs, stdfs.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg, 0:
			if err := tarExtractFile(ctx, inv, tr, targetAbs, header, opts.keepOld); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := tarExtractSymlink(ctx, inv, entryName, targetAbs, header, opts.keepOld); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := tarExtractHardlink(ctx, inv, baseDir, targetAbs, header, opts.keepOld); err != nil {
				return err
			}
		default:
			return exitf(inv, 1, "tar: unsupported entry type %q", string(header.Typeflag))
		}
	}
}

func tarBaseDir(ctx context.Context, inv *Invocation, opts *tarOptions) (string, error) {
	if opts.chdir == "" {
		return inv.Cwd, nil
	}
	info, abs, err := statPath(ctx, inv, opts.chdir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", exitf(inv, 1, "tar: %s: Not a directory", opts.chdir)
	}
	return abs, nil
}

func openTarWriter(ctx context.Context, inv *Invocation, opts *tarOptions) (*tar.Writer, func() error, error) {
	writer := inv.Stdout
	closers := make([]io.Closer, 0, 3)

	if opts.archive != "-" {
		archiveAbs, err := allowPath(ctx, inv, policy.FileActionWrite, opts.archive)
		if err != nil {
			return nil, nil, err
		}
		if err := ensureParentDirExists(ctx, inv, archiveAbs); err != nil {
			return nil, nil, err
		}
		file, err := inv.FS.OpenFile(ctx, archiveAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, nil, &ExitError{Code: 1, Err: err}
		}
		writer = file
		closers = append(closers, file)
	}

	if opts.gzip {
		zw := gzip.NewWriter(writer)
		writer = zw
		closers = append([]io.Closer{zw}, closers...)
	}

	tw := tar.NewWriter(writer)
	closers = append([]io.Closer{tw}, closers...)

	return tw, func() error {
		var first error
		for _, closer := range closers {
			if err := closer.Close(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}, nil
}

func openTarReader(ctx context.Context, inv *Invocation, opts *tarOptions) (*tar.Reader, func() error, error) {
	reader := inv.Stdin
	closers := make([]io.Closer, 0, 2)

	if opts.archive != "-" {
		file, _, err := openRead(ctx, inv, opts.archive)
		if err != nil {
			return nil, nil, err
		}
		reader = file
		closers = append(closers, file)
	}

	if opts.gzip {
		zr, err := gzip.NewReader(reader)
		if err != nil {
			for _, closer := range closers {
				_ = closer.Close()
			}
			return nil, nil, exitf(inv, 1, "tar: %v", err)
		}
		reader = zr
		closers = append([]io.Closer{zr}, closers...)
	}

	return tar.NewReader(reader), func() error {
		var first error
		for _, closer := range closers {
			if err := closer.Close(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}, nil
}

func tarWritePath(ctx context.Context, inv *Invocation, tw *tar.Writer, abs, name string, info stdfs.FileInfo) error {
	if err := tarWriteHeader(ctx, inv, tw, abs, name, info); err != nil {
		return err
	}
	if info.Mode()&stdfs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		if info.IsDir() {
			return tarWriteDirChildren(ctx, inv, tw, abs, name)
		}
		return nil
	}

	file, _, err := openRead(ctx, inv, abs)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := io.Copy(tw, file); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func tarWriteHeader(ctx context.Context, inv *Invocation, tw *tar.Writer, abs, name string, info stdfs.FileInfo) error {
	headerName := path.Clean(name)
	if headerName == "." {
		headerName = path.Base(abs)
	}
	if headerName == "" || headerName == "/" {
		return nil
	}

	var (
		header *tar.Header
		err    error
	)
	switch {
	case info.Mode()&stdfs.ModeSymlink != 0:
		target, readErr := inv.FS.Readlink(ctx, abs)
		if readErr != nil {
			return &ExitError{Code: 1, Err: readErr}
		}
		header = &tar.Header{
			Name:     headerName,
			Typeflag: tar.TypeSymlink,
			Linkname: target,
			Mode:     int64(info.Mode().Perm()),
			ModTime:  info.ModTime(),
		}
	case info.IsDir():
		header, err = tar.FileInfoHeader(info, "")
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		header.Name = strings.TrimSuffix(headerName, "/") + "/"
	case info.Mode().IsRegular():
		header, err = tar.FileInfoHeader(info, "")
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		header.Name = headerName
	default:
		return exitf(inv, 1, "tar: unsupported file type for %s", abs)
	}
	if err := tw.WriteHeader(header); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func tarWriteDirChildren(ctx context.Context, inv *Invocation, tw *tar.Writer, abs, name string) error {
	entries, _, err := readDir(ctx, inv, abs)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		childAbs := path.Join(abs, entry.Name())
		childInfo, _, err := lstatPath(ctx, inv, childAbs)
		if err != nil {
			return err
		}
		if err := tarWritePath(ctx, inv, tw, childAbs, path.Join(name, entry.Name()), childInfo); err != nil {
			return err
		}
	}
	return nil
}

func tarArchiveName(arg, sourceAbs string) string {
	if strings.HasPrefix(arg, "/") {
		return strings.TrimPrefix(path.Clean(arg), "/")
	}
	name := path.Clean(arg)
	if name == "." || name == "/" {
		return path.Base(sourceAbs)
	}
	return strings.TrimPrefix(name, "./")
}

func tarIsRegularType(typeflag byte) bool {
	return typeflag == tar.TypeReg || typeflag == 0
}

func sanitizeTarEntryName(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	cleaned := path.Clean(strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/"))
	switch {
	case cleaned == ".", cleaned == "/":
		return "", nil
	case cleaned == "..", strings.HasPrefix(cleaned, "../"):
		return "", fmt.Errorf("unsafe archive path %q", name)
	default:
		return cleaned, nil
	}
}

func tarEnsureDir(ctx context.Context, inv *Invocation, targetAbs string, perm stdfs.FileMode) error {
	if _, err := allowPath(ctx, inv, policy.FileActionMkdir, targetAbs); err != nil {
		return err
	}
	if err := inv.FS.MkdirAll(ctx, targetAbs, perm.Perm()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func tarExtractFile(ctx context.Context, inv *Invocation, tr *tar.Reader, targetAbs string, header *tar.Header, keepOld bool) error {
	if err := tarEnsureParents(ctx, inv, targetAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionWrite, targetAbs); err != nil {
		return err
	}
	if exists, err := gzipTargetExists(ctx, inv, targetAbs); err != nil {
		return err
	} else if exists {
		if keepOld {
			return exitf(inv, 1, "tar: %s: Cannot open: File exists", targetAbs)
		}
		if err := inv.FS.Remove(ctx, targetAbs, false); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	file, err := inv.FS.OpenFile(ctx, targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, stdfs.FileMode(header.Mode).Perm())
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if _, err := io.Copy(file, tr); err != nil {
		_ = file.Close()
		return &ExitError{Code: 1, Err: err}
	}
	if err := file.Close(); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if !header.ModTime.IsZero() {
		mtime := header.ModTime
		if err := inv.FS.Chtimes(ctx, targetAbs, mtime, mtime); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	recordFileMutation(inv.trace, "write", targetAbs, "", targetAbs)
	return nil
}

func tarExtractSymlink(ctx context.Context, inv *Invocation, entryName, targetAbs string, header *tar.Header, keepOld bool) error {
	if err := tarValidateSymlinkTarget(entryName, header.Linkname); err != nil {
		return exitf(inv, 1, "tar: %v", err)
	}
	if err := tarEnsureParents(ctx, inv, targetAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionWrite, targetAbs); err != nil {
		return err
	}
	if exists, err := gzipTargetExists(ctx, inv, targetAbs); err != nil {
		return err
	} else if exists {
		if keepOld {
			return exitf(inv, 1, "tar: %s: Cannot create symlink: File exists", targetAbs)
		}
		if err := inv.FS.Remove(ctx, targetAbs, true); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := inv.FS.Symlink(ctx, header.Linkname, targetAbs); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func tarExtractHardlink(ctx context.Context, inv *Invocation, baseDir, targetAbs string, header *tar.Header, keepOld bool) error {
	linkName, err := sanitizeTarEntryName(header.Linkname)
	if err != nil {
		return exitf(inv, 1, "tar: %v", err)
	}
	linkAbs := gbfs.Resolve(baseDir, linkName)
	if err := tarEnsureParents(ctx, inv, targetAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionWrite, targetAbs); err != nil {
		return err
	}
	if _, err := allowPath(ctx, inv, policy.FileActionRead, linkAbs); err != nil {
		return err
	}
	if exists, err := gzipTargetExists(ctx, inv, targetAbs); err != nil {
		return err
	} else if exists {
		if keepOld {
			return exitf(inv, 1, "tar: %s: Cannot create link: File exists", targetAbs)
		}
		if err := inv.FS.Remove(ctx, targetAbs, false); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := inv.FS.Link(ctx, linkAbs, targetAbs); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func tarValidateSymlinkTarget(entryName, target string) error {
	if target == "" {
		return nil
	}
	if strings.HasPrefix(target, "/") {
		return fmt.Errorf("unsafe symlink target %q", target)
	}
	resolved := path.Clean(path.Join(path.Dir(entryName), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("unsafe symlink target %q", target)
	}
	return nil
}

func tarEnsureParents(ctx context.Context, inv *Invocation, targetAbs string) error {
	parent := path.Dir(targetAbs)
	if parent == "." || parent == "/" {
		return nil
	}
	if _, err := allowPath(ctx, inv, policy.FileActionMkdir, parent); err != nil {
		return err
	}
	if err := inv.FS.MkdirAll(ctx, parent, 0o755); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

const tarHelpText = `tar - archive helper inside the gbash sandbox

Usage:
  tar -c[f] ARCHIVE [PATH...]
  tar -x[f] ARCHIVE
  tar -t[f] ARCHIVE

Supported options:
  -c        create archive
  -x        extract archive
  -t        list archive contents
  -f FILE   read/write FILE instead of stdin/stdout
  -C DIR    use DIR as create base or extract destination
  -z        gzip-compress or gzip-decompress the archive stream
  -v        verbose entry listing to stderr
  -O        write extracted file contents to stdout
  -k        keep existing files on extract
  --help    show this help

Notes:
  - leading slashes are stripped on extract
  - parent-traversal paths are rejected on extract
  - symlink targets that escape the extraction root are rejected
  - bzip2/xz/zstd and append/update modes are not implemented
`

var _ Command = (*Tar)(nil)
var _ SpecProvider = (*Tar)(nil)
var _ ParsedRunner = (*Tar)(nil)
