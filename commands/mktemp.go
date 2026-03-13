package commands

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"math/rand"
	"os"
	"path"
	"strings"
	"time"
)

const (
	mktempDefaultTemplate = "tmp.XXXXXXXXXX"
	mktempAlphabet        = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	mktempMaxAttempts     = 256
	mktempTmpDirEnvVar    = "TMPDIR"
	mktempFallbackTmpDir  = "/tmp"
)

type Mktemp struct{}

type mktempOptions struct {
	directory       bool
	dryRun          bool
	quiet           bool
	suffix          string
	suffixProvided  bool
	tmpdir          string
	tmpdirSet       bool
	treatAsTemplate bool
	template        string
}

type mktempParams struct {
	directory string
	prefix    string
	numRandom int
	suffix    string
}

type mktempCreateError struct {
	kind     string
	template string
	err      error
}

func (e *mktempCreateError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf(
		"mktemp: failed to create %s via template %s: %s",
		e.kind,
		quoteGNUOperand(e.template),
		mktempCreateErrorText(e.err),
	)
}

func (e *mktempCreateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func NewMktemp() *Mktemp {
	return &Mktemp{}
}

func (c *Mktemp) Name() string {
	return "mktemp"
}

func (c *Mktemp) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Mktemp) Spec() CommandSpec {
	return CommandSpec{
		Name:  "mktemp",
		About: "Create a temporary file or directory.",
		Usage: "mktemp [OPTION]... [TEMPLATE]",
		Options: []OptionSpec{
			{Name: "directory", Short: 'd', Long: "directory", Help: "Make a directory instead of a file"},
			{Name: "dry-run", Short: 'u', Long: "dry-run", Help: "do not create anything; merely print a name (unsafe)"},
			{Name: "quiet", Short: 'q', Long: "quiet", Help: "Fail silently if an error occurs."},
			{Name: "suffix", Long: "suffix", ValueName: "SUFFIX", Arity: OptionRequiredValue, Help: "append SUFFIX to TEMPLATE; SUFFIX must not contain a path separator. This option is implied if TEMPLATE does not end with X."},
			{Name: "p", Short: 'p', ValueName: "DIR", Arity: OptionRequiredValue, Help: "short form of --tmpdir"},
			{Name: "tmpdir", Long: "tmpdir", ValueName: "DIR", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "interpret TEMPLATE relative to DIR; if DIR is not specified, use $TMPDIR if set, else /tmp. With this option, TEMPLATE must not be an absolute name; unlike with -t, TEMPLATE may contain slashes, but mktemp creates only the final component"},
			{Name: "t", Short: 't', Help: "Generate a template (using the supplied prefix and TMPDIR if set) to create a filename template [deprecated]"},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		HelpRenderer: func(w io.Writer, spec CommandSpec) error {
			_, err := fmt.Fprintf(w, "%s\n\nUsage: %s\n\nOptions:\n  -d, --directory         Make a directory instead of a file\n  -u, --dry-run           do not create anything; merely print a name (unsafe)\n  -q, --quiet             Fail silently if an error occurs.\n      --suffix=SUFFIX     append SUFFIX to TEMPLATE; SUFFIX must not contain a path separator. This option is implied if TEMPLATE does not end with X.\n  -p DIR                  short form of --tmpdir\n      --tmpdir[=DIR]      interpret TEMPLATE relative to DIR; if DIR is not specified, use $TMPDIR if set, else /tmp. With this option, TEMPLATE must not be an absolute name; unlike with -t, TEMPLATE may contain slashes, but mktemp creates only the final component\n  -t                      Generate a template (using the supplied prefix and TMPDIR if set) to create a filename template [deprecated]\n  -h, --help              display this help and exit\n      --version           output version information and exit\n", spec.About, spec.Usage)
			return err
		},
	}
}

func (c *Mktemp) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	positionals := matches.Positionals()
	if len(positionals) > 1 {
		return commandUsageError(inv, c.Name(), "too many templates")
	}

	opts := mktempOptionsFromMatches(inv, matches, positionals)
	if mktempPosixlyCorrect(inv.Env) && len(positionals) == 1 && len(inv.Args) > 0 && inv.Args[len(inv.Args)-1] != positionals[0] {
		return commandUsageError(inv, c.Name(), "too many templates")
	}

	params, err := mktempParamsFromOptions(opts)
	if err != nil {
		return exitf(inv, 1, "%v", err)
	}

	var output string
	if opts.dryRun {
		output, err = mktempDryRun(params)
	} else {
		output, err = mktempExec(ctx, inv, params, opts.directory)
	}
	if err != nil {
		if opts.quiet {
			return &ExitError{Code: 1, Err: err}
		}
		var createErr *mktempCreateError
		if errors.As(err, &createErr) {
			return exitf(inv, 1, "%s", createErr.Error())
		}
		return exitf(inv, 1, "mktemp: %v", err)
	}

	if _, err := fmt.Fprintln(inv.Stdout, output); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func mktempOptionsFromMatches(inv *Invocation, matches *ParsedCommand, positionals []string) mktempOptions {
	opts := mktempOptions{
		directory:       matches.Has("directory"),
		dryRun:          matches.Has("dry-run"),
		quiet:           matches.Has("quiet"),
		suffix:          matches.Value("suffix"),
		suffixProvided:  matches.Has("suffix"),
		treatAsTemplate: matches.Has("t"),
		template:        mktempDefaultTemplate,
	}
	if len(positionals) == 1 {
		opts.template = positionals[0]
	}

	explicitTmpdir, hasExplicitTmpdir := mktempExplicitTmpdir(inv.Env, matches)
	if len(positionals) == 0 {
		opts.tmpdirSet = true
		if hasExplicitTmpdir {
			opts.tmpdir = explicitTmpdir
		} else {
			opts.tmpdir = mktempEnvTmpdirOrDefault(inv.Env)
		}
		return opts
	}

	if opts.treatAsTemplate {
		if envTmpdir, ok := mktempEnvTmpdir(inv.Env); ok {
			opts.tmpdir = envTmpdir
			opts.tmpdirSet = true
			return opts
		}
		if hasExplicitTmpdir {
			opts.tmpdir = explicitTmpdir
			opts.tmpdirSet = true
			return opts
		}
		opts.tmpdir = mktempEnvTmpdirOrDefault(inv.Env)
		opts.tmpdirSet = true
		return opts
	}

	if hasExplicitTmpdir {
		opts.tmpdir = explicitTmpdir
		opts.tmpdirSet = true
	}

	return opts
}

func mktempExplicitTmpdir(env map[string]string, matches *ParsedCommand) (string, bool) {
	switch {
	case matches.Has("tmpdir"):
		if value := matches.Value("tmpdir"); value != "" {
			return value, true
		}
		return mktempEnvTmpdirOrDefault(env), true
	case matches.Has("p"):
		if value := matches.Value("p"); value != "" {
			return value, true
		}
		return mktempEnvTmpdirOrDefault(env), true
	default:
		return "", false
	}
}

func mktempParamsFromOptions(opts mktempOptions) (mktempParams, error) {
	template := opts.template
	if opts.suffixProvided && !strings.HasSuffix(template, "X") {
		return mktempParams{}, fmt.Errorf("mktemp: with --suffix, template %s must end in X", quoteGNUOperand(template))
	}

	start, end, ok := mktempFindLastXBlock(template)
	if !ok {
		return mktempParams{}, fmt.Errorf("mktemp: too few X's in template %s", quoteGNUOperand(template))
	}

	prefixFromTemplate := template[:start]
	if opts.treatAsTemplate && strings.Contains(prefixFromTemplate, "/") {
		return mktempParams{}, fmt.Errorf("mktemp: invalid template, %s, contains directory separator", quoteGNUOperand(template))
	}
	if opts.tmpdirSet && path.IsAbs(prefixFromTemplate) {
		return mktempParams{}, fmt.Errorf("mktemp: invalid template, %s; with --tmpdir, it may not be absolute", quoteGNUOperand(template))
	}

	suffixFromTemplate := template[end:]
	suffix := suffixFromTemplate + opts.suffix
	if strings.Contains(suffix, "/") {
		return mktempParams{}, fmt.Errorf("mktemp: invalid suffix %s, contains directory separator", quoteGNUOperand(suffix))
	}

	combined := mktempJoinVisible(opts.tmpdir, prefixFromTemplate)
	directory, prefix := mktempSplitVisiblePrefix(combined, strings.HasSuffix(prefixFromTemplate, "/"))

	return mktempParams{
		directory: directory,
		prefix:    prefix,
		numRandom: end - start,
		suffix:    suffix,
	}, nil
}

func mktempFindLastXBlock(template string) (start, end int, ok bool) {
	end = strings.LastIndexByte(template, 'X')
	if end < 0 {
		return 0, 0, false
	}
	start = end
	for start > 0 && template[start-1] == 'X' {
		start--
	}
	if end+1-start < 3 {
		return 0, 0, false
	}
	return start, end + 1, true
}

func mktempJoinVisible(dir, suffix string) string {
	switch {
	case dir == "":
		return suffix
	case suffix == "":
		return dir
	case dir == "/":
		return "/" + strings.TrimLeft(suffix, "/")
	default:
		return strings.TrimRight(dir, "/") + "/" + strings.TrimLeft(suffix, "/")
	}
}

func mktempSplitVisiblePrefix(combined string, trailingSlash bool) (dir, prefix string) {
	if trailingSlash {
		return mktempTrimVisibleDir(combined), ""
	}
	if combined == "" {
		return "", ""
	}
	idx := strings.LastIndexByte(combined, '/')
	if idx < 0 {
		return "", combined
	}
	dir = combined[:idx]
	if dir == "" && strings.HasPrefix(combined, "/") {
		dir = "/"
	}
	return dir, combined[idx+1:]
}

func mktempTrimVisibleDir(value string) string {
	switch value {
	case "":
		return ""
	case "/":
		return "/"
	default:
		trimmed := strings.TrimRight(value, "/")
		if trimmed == "" && strings.HasPrefix(value, "/") {
			return "/"
		}
		if trimmed == "" {
			return "."
		}
		return trimmed
	}
}

func mktempDryRun(params mktempParams) (string, error) {
	randomPart, err := mktempRandomString(params.numRandom)
	if err != nil {
		return "", err
	}
	return mktempCandidatePath(params, randomPart), nil
}

func mktempExec(ctx context.Context, inv *Invocation, params mktempParams, makeDir bool) (string, error) {
	templatePath := mktempCandidatePath(params, strings.Repeat("X", params.numRandom))
	kind := "file"
	if makeDir {
		kind = "directory"
	}

	for range mktempMaxAttempts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		randomPart, err := mktempRandomString(params.numRandom)
		if err != nil {
			return "", &mktempCreateError{kind: kind, template: templatePath, err: err}
		}
		visiblePath := mktempCandidatePath(params, randomPart)
		absPath := inv.FS.Resolve(visiblePath)
		if makeDir {
			err = mktempCreateDir(ctx, inv, absPath)
		} else {
			err = mktempCreateFile(ctx, inv, absPath)
		}
		if err == nil {
			return visiblePath, nil
		}
		if errors.Is(err, stdfs.ErrExist) || os.IsExist(err) {
			continue
		}
		return "", &mktempCreateError{kind: kind, template: templatePath, err: err}
	}

	return "", &mktempCreateError{kind: kind, template: templatePath, err: stdfs.ErrExist}
}

func mktempCreateFile(ctx context.Context, inv *Invocation, absPath string) error {
	parent := path.Dir(absPath)
	info, err := inv.FS.Stat(ctx, parent)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}

	file, err := inv.FS.OpenFile(ctx, absPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func mktempCreateDir(ctx context.Context, inv *Invocation, absPath string) error {
	if _, err := inv.FS.Lstat(ctx, absPath); err == nil {
		return stdfs.ErrExist
	} else if !errors.Is(err, stdfs.ErrNotExist) {
		return err
	}

	parent := path.Dir(absPath)
	info, err := inv.FS.Stat(ctx, parent)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}

	if err := inv.FS.MkdirAll(ctx, absPath, 0o700); err != nil {
		return err
	}
	return inv.FS.Chmod(ctx, absPath, 0o700)
}

func mktempCandidatePath(params mktempParams, randomPart string) string {
	return mktempJoinVisible(params.directory, params.prefix+randomPart+params.suffix)
}

func mktempRandomString(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}

	buf := make([]byte, length)
	if _, err := crand.Read(buf); err != nil {
		fallback := rand.New(rand.NewSource(time.Now().UnixNano()))
		for i := range buf {
			buf[i] = byte(fallback.Intn(len(mktempAlphabet)))
		}
	} else {
		for i := range buf {
			buf[i] %= byte(len(mktempAlphabet))
		}
	}

	out := make([]byte, length)
	for i, b := range buf {
		out[i] = mktempAlphabet[int(b)]
	}
	return string(out), nil
}

func mktempCreateErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, stdfs.ErrNotExist), os.IsNotExist(err):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrExist), os.IsExist(err):
		return "File exists"
	default:
		lower := strings.ToLower(err.Error())
		switch {
		case strings.Contains(lower, "not a directory"):
			return "Not a directory"
		case strings.Contains(lower, "permission denied"):
			return "Permission denied"
		default:
			return err.Error()
		}
	}
}

func mktempEnvTmpdir(env map[string]string) (string, bool) {
	value, ok := env[mktempTmpDirEnvVar]
	return value, ok
}

func mktempEnvTmpdirOrDefault(env map[string]string) string {
	if value, ok := mktempEnvTmpdir(env); ok {
		if value == "" {
			return mktempFallbackTmpDir
		}
		return value
	}
	return mktempFallbackTmpDir
}

func mktempPosixlyCorrect(env map[string]string) bool {
	_, ok := env["POSIXLY_CORRECT"]
	return ok
}

var _ Command = (*Mktemp)(nil)
var _ SpecProvider = (*Mktemp)(nil)
var _ ParsedRunner = (*Mktemp)(nil)
