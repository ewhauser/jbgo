package python

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"maps"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	monty "github.com/ewhauser/gomonty"
	montyvfs "github.com/ewhauser/gomonty/vfs"
)

const (
	pythonEvalEntry  = ".gbash-python-eval.py"
	pythonStdinEntry = ".gbash-python-stdin.py"
)

type Python struct {
	name string
}

type sourceKind int

const (
	sourceStdin sourceKind = iota
	sourceEval
	sourceFile
)

type pythonSource struct {
	kind      sourceKind
	code      string
	scriptArg string
	entryPath string
}

type invocationFS struct {
	requestContext func() context.Context
	inv            *commands.Invocation
}

func New(name string) *Python {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "python"
	}
	return &Python{name: name}
}

func Register(registry commands.CommandRegistry) error {
	if registry == nil {
		return nil
	}
	for _, name := range []string{"python", "python3"} {
		commandName := name
		if err := registry.RegisterLazy(commandName, func() (commands.Command, error) {
			return New(commandName), nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Python) Name() string {
	return c.name
}

func (c *Python) Run(ctx context.Context, inv *commands.Invocation) error {
	return commands.RunCommand(ctx, c, inv)
}

func (c *Python) Spec() commands.CommandSpec {
	return commands.CommandSpec{
		Name:  c.Name(),
		About: "Run sandboxed Python code inside gbash",
		Usage: c.Name() + " [-c command] [script.py]",
		Options: []commands.OptionSpec{
			{
				Name:      "command",
				Short:     'c',
				ValueName: "command",
				Arity:     commands.OptionRequiredValue,
				Help:      "program passed in as string",
			},
		},
		Args: []commands.ArgSpec{
			{Name: "arg", ValueName: "arg", Repeatable: true},
		},
		Parse: commands.ParseConfig{
			ShortOptionValueAttached: true,
			StopAtFirstPositional:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		VersionRenderer: func(w io.Writer, _ commands.CommandSpec) error {
			return commands.RenderSimpleVersion(w, c.Name())
		},
	}
}

func (c *Python) RunParsed(ctx context.Context, inv *commands.Invocation, matches *commands.ParsedCommand) error {
	source, err := c.classifySource(ctx, inv, matches)
	if err != nil {
		return err
	}

	runner, err := monty.New(source.code, monty.CompileOptions{
		ScriptName: source.entryPath,
	})
	if err != nil {
		return pythonExit(inv, err)
	}
	if sourceReferencesBarePrint(source.code) {
		return commands.Exitf(inv, 1,
			"%s: print is not supported yet with the pinned gomonty runtime; builtin print output is unavailable and print(..., file=...) is unsupported upstream",
			c.Name(),
		)
	}

	env := montyvfs.MapEnvironment{}
	maps.Copy(env, inv.Env)

	_, err = runner.Run(ctx, monty.RunOptions{
		Print: monty.WriterPrintCallback(inv.Stdout),
		OS: montyvfs.Handler(&invocationFS{
			requestContext: func() context.Context { return ctx },
			inv:            inv,
		}, env),
	})
	if err != nil {
		return pythonExit(inv, err)
	}
	return nil
}

func (c *Python) classifySource(ctx context.Context, inv *commands.Invocation, matches *commands.ParsedCommand) (pythonSource, error) {
	if matches == nil {
		return c.prepareStdinSource(ctx, inv)
	}

	args := matches.Args("arg")
	if matches.Has("command") {
		if len(args) > 0 {
			return pythonSource{}, commands.Exitf(inv, 1, "%s: extra script arguments are not supported", c.Name())
		}
		return c.prepareEvalSource(inv, matches.Value("command")), nil
	}
	switch len(args) {
	case 0:
		return c.prepareStdinSource(ctx, inv)
	case 1:
		return c.prepareFileSource(ctx, inv, args[0])
	default:
		return pythonSource{}, commands.Exitf(inv, 1, "%s: extra script arguments are not supported", c.Name())
	}
}

func (c *Python) prepareEvalSource(inv *commands.Invocation, code string) pythonSource {
	return pythonSource{
		kind:      sourceEval,
		code:      code,
		entryPath: gbfs.Resolve(inv.Cwd, pythonEvalEntry),
	}
}

func (c *Python) prepareStdinSource(ctx context.Context, inv *commands.Invocation) (pythonSource, error) {
	data, err := commands.ReadAllStdin(ctx, inv)
	if err != nil {
		return pythonSource{}, err
	}
	return pythonSource{
		kind:      sourceStdin,
		code:      string(data),
		entryPath: gbfs.Resolve(inv.Cwd, pythonStdinEntry),
	}, nil
}

func (c *Python) prepareFileSource(ctx context.Context, inv *commands.Invocation, scriptArg string) (pythonSource, error) {
	abs := inv.FS.Resolve(scriptArg)
	info, err := inv.FS.Stat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return pythonSource{}, commands.Exitf(inv, 1, "%s: %s: No such file or directory", c.Name(), scriptArg)
		}
		return pythonSource{}, err
	}
	if info.IsDir() {
		return pythonSource{}, commands.Exitf(inv, 1, "%s: %s: Is a directory", c.Name(), scriptArg)
	}

	data, err := inv.FS.ReadFile(ctx, abs)
	if err != nil {
		return pythonSource{}, err
	}
	return pythonSource{
		kind:      sourceFile,
		code:      string(data),
		scriptArg: scriptArg,
		entryPath: abs,
	}, nil
}

func pythonExit(inv *commands.Invocation, err error) error {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	type tracebackError interface {
		TracebackString() string
	}
	var withTrace tracebackError
	if errors.As(err, &withTrace) {
		if trace := strings.TrimSpace(withTrace.TracebackString()); trace != "" {
			message = trace
		}
	}
	if message == "" {
		message = "python: execution failed"
	}
	return commands.Exitf(inv, 1, "%s", message)
}

func (fsys *invocationFS) Exists(pathValue string) (bool, error) {
	_, err := fsys.commandFS().Stat(fsys.ctx(), pathValue)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, stdfs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func (fsys *invocationFS) IsFile(pathValue string) (bool, error) {
	info, err := fsys.commandFS().Stat(fsys.ctx(), pathValue)
	switch {
	case err == nil:
		return !info.IsDir() && info.Mode()&stdfs.ModeSymlink == 0, nil
	case errors.Is(err, stdfs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func (fsys *invocationFS) IsDir(pathValue string) (bool, error) {
	info, err := fsys.commandFS().Stat(fsys.ctx(), pathValue)
	switch {
	case err == nil:
		return info.IsDir(), nil
	case errors.Is(err, stdfs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func (fsys *invocationFS) IsSymlink(pathValue string) (bool, error) {
	info, err := fsys.commandFS().Lstat(fsys.ctx(), pathValue)
	switch {
	case err == nil:
		return info.Mode()&stdfs.ModeSymlink != 0, nil
	case errors.Is(err, stdfs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func (fsys *invocationFS) ReadText(pathValue string) (string, error) {
	data, err := fsys.commandFS().ReadFile(fsys.ctx(), pathValue)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", &stdfs.PathError{Op: "read_text", Path: pathValue, Err: syscall.EILSEQ}
	}
	return string(data), nil
}

func (fsys *invocationFS) ReadBytes(pathValue string) ([]byte, error) {
	return fsys.commandFS().ReadFile(fsys.ctx(), pathValue)
}

func (fsys *invocationFS) WriteText(pathValue, data string) (int, error) {
	return fsys.writeFile(pathValue, []byte(data))
}

func (fsys *invocationFS) WriteBytes(pathValue string, data []byte) (int, error) {
	return fsys.writeFile(pathValue, data)
}

func (fsys *invocationFS) Mkdir(pathValue string, parents, existOK bool) error {
	cmdFS := fsys.commandFS()
	info, err := cmdFS.Lstat(fsys.ctx(), pathValue)
	switch {
	case err == nil:
		if info.IsDir() && existOK {
			return nil
		}
		return &stdfs.PathError{Op: "mkdir", Path: pathValue, Err: stdfs.ErrExist}
	case !errors.Is(err, stdfs.ErrNotExist):
		return err
	}

	if !parents {
		parent := path.Dir(cmdFS.Resolve(pathValue))
		parentInfo, err := cmdFS.Stat(fsys.ctx(), parent)
		if err != nil {
			if errors.Is(err, stdfs.ErrNotExist) {
				return &stdfs.PathError{Op: "mkdir", Path: pathValue, Err: stdfs.ErrNotExist}
			}
			return err
		}
		if !parentInfo.IsDir() {
			return &stdfs.PathError{Op: "mkdir", Path: parent, Err: syscall.ENOTDIR}
		}
	}

	return cmdFS.MkdirAll(fsys.ctx(), pathValue, 0o755)
}

func (fsys *invocationFS) Unlink(pathValue string) error {
	cmdFS := fsys.commandFS()
	info, err := cmdFS.Lstat(fsys.ctx(), pathValue)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &stdfs.PathError{Op: "unlink", Path: pathValue, Err: syscall.EISDIR}
	}
	return cmdFS.Remove(fsys.ctx(), pathValue, false)
}

func (fsys *invocationFS) Rmdir(pathValue string) error {
	cmdFS := fsys.commandFS()
	info, err := cmdFS.Lstat(fsys.ctx(), pathValue)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &stdfs.PathError{Op: "rmdir", Path: pathValue, Err: syscall.ENOTDIR}
	}
	return cmdFS.Remove(fsys.ctx(), pathValue, false)
}

func (fsys *invocationFS) Iterdir(pathValue string) ([]string, error) {
	cmdFS := fsys.commandFS()
	base := cmdFS.Resolve(pathValue)
	entries, err := cmdFS.ReadDir(fsys.ctx(), pathValue)
	if err != nil {
		return nil, err
	}
	children := make([]string, 0, len(entries))
	for _, entry := range entries {
		children = append(children, gbfs.Resolve(base, entry.Name()))
	}
	sort.Strings(children)
	return children, nil
}

func (fsys *invocationFS) Stat(pathValue string) (monty.StatResult, error) {
	info, err := fsys.commandFS().Stat(fsys.ctx(), pathValue)
	if err != nil {
		return monty.StatResult{}, err
	}
	return statResultFromFileInfo(info), nil
}

func (fsys *invocationFS) Rename(oldPath, newPath string) error {
	return fsys.commandFS().Rename(fsys.ctx(), oldPath, newPath)
}

func (fsys *invocationFS) Resolve(pathValue string) (string, error) {
	cmdFS := fsys.commandFS()
	abs := cmdFS.Resolve(pathValue)
	_, err := cmdFS.Lstat(fsys.ctx(), abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return abs, nil
		}
		return "", err
	}
	return cmdFS.Realpath(fsys.ctx(), abs)
}

func (fsys *invocationFS) Absolute(pathValue string) (string, error) {
	return fsys.commandFS().Resolve(pathValue), nil
}

func (fsys *invocationFS) commandFS() *commands.CommandFS {
	if fsys != nil && fsys.inv != nil && fsys.inv.FS != nil {
		return fsys.inv.FS
	}
	return &commands.CommandFS{}
}

func (fsys *invocationFS) ctx() context.Context {
	if fsys != nil && fsys.requestContext != nil {
		return fsys.requestContext()
	}
	return context.Background()
}

func (fsys *invocationFS) writeFile(pathValue string, data []byte) (int, error) {
	file, err := fsys.commandFS().OpenFile(fsys.ctx(), pathValue, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()

	written, err := file.Write(data)
	if err != nil {
		return written, err
	}
	return written, nil
}

func statResultFromFileInfo(info stdfs.FileInfo) monty.StatResult {
	if info == nil {
		return monty.StatResult{}
	}

	ownership, ok := gbfs.OwnershipFromFileInfo(info)
	if !ok {
		ownership = gbfs.DefaultOwnership()
	}

	atime := info.ModTime().UTC()
	mtime := info.ModTime().UTC()
	inode := int64(0)
	device := int64(0)
	nlink := int64(1)
	if info.IsDir() {
		nlink = 2
	}

	if sys := reflect.ValueOf(info.Sys()); sys.IsValid() {
		if sys.Kind() == reflect.Ptr && !sys.IsNil() {
			sys = sys.Elem()
		}
		if sys.IsValid() && sys.Kind() == reflect.Struct {
			if sec, ok := int64Field(sys, "Atime"); ok {
				nsec, _ := int64Field(sys, "AtimeNsec")
				atime = time.Unix(sec, nsec).UTC()
			}
			if value, ok := int64Field(sys, "NodeID"); ok {
				inode = value
			}
			if value, ok := int64Field(sys, "Ino"); ok {
				inode = value
			}
			if value, ok := int64Field(sys, "Dev"); ok {
				device = value
			}
			if value, ok := int64Field(sys, "Nlink"); ok && value > 0 {
				nlink = value
			}
		}
	}

	return monty.StatResult{
		Mode:  fileModeToStatMode(info.Mode()),
		Ino:   inode,
		Dev:   device,
		Nlink: nlink,
		UID:   int64(ownership.UID),
		GID:   int64(ownership.GID),
		Size:  info.Size(),
		Atime: float64(atime.UnixNano()) / float64(time.Second),
		Mtime: float64(mtime.UnixNano()) / float64(time.Second),
		Ctime: float64(mtime.UnixNano()) / float64(time.Second),
	}
}

func fileModeToStatMode(mode stdfs.FileMode) int64 {
	result := int64(mode.Perm())
	switch {
	case mode&stdfs.ModeSymlink != 0:
		result |= 0o120_000
	case mode.IsDir():
		result |= 0o040_000
	case mode&stdfs.ModeNamedPipe != 0:
		result |= 0o010_000
	default:
		result |= 0o100_000
	}
	return result
}

func int64Field(value reflect.Value, name string) (int64, bool) {
	field := value.FieldByName(name)
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return field.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int64(field.Uint()), true
	default:
		return 0, false
	}
}

type pythonTokenKind uint8

const (
	pythonTokenNone pythonTokenKind = iota
	pythonTokenDot
	pythonTokenOther
)

func sourceReferencesBarePrint(source string) bool {
	lastKind := pythonTokenNone

	for index := 0; index < len(source); {
		switch ch := source[index]; {
		case ch == '#':
			index++
			for index < len(source) && source[index] != '\n' {
				index++
			}
		case ch == '"' || ch == '\'':
			index = consumePythonString(source, index)
			lastKind = pythonTokenOther
		case isPythonIdentifierStart(ch):
			start := index
			index++
			for index < len(source) && isPythonIdentifierContinue(source[index]) {
				index++
			}
			if source[start:index] == "print" && lastKind != pythonTokenDot {
				return true
			}
			lastKind = pythonTokenOther
		default:
			index++
			switch ch {
			case ' ', '\t', '\n', '\r', '\f', '\v':
				continue
			case '.':
				lastKind = pythonTokenDot
			default:
				lastKind = pythonTokenOther
			}
		}
	}

	return false
}

func consumePythonString(source string, start int) int {
	quote := source[start]
	if start+2 < len(source) && source[start+1] == quote && source[start+2] == quote {
		index := start + 3
		for index+2 < len(source) {
			if source[index] == '\\' {
				index += 2
				continue
			}
			if source[index] == quote && source[index+1] == quote && source[index+2] == quote {
				if !isEscapedPythonDelimiter(source, index) {
					return index + 3
				}
			}
			index++
		}
		return len(source)
	}

	index := start + 1
	for index < len(source) {
		switch source[index] {
		case '\\':
			if index+1 < len(source) {
				index += 2
				continue
			}
			return len(source)
		case quote:
			return index + 1
		default:
			index++
		}
	}
	return len(source)
}

func isEscapedPythonDelimiter(source string, index int) bool {
	backslashes := 0
	for index > 0 {
		index--
		if source[index] != '\\' {
			break
		}
		backslashes++
	}
	return backslashes%2 == 1
}

func isPythonIdentifierStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isPythonIdentifierContinue(ch byte) bool {
	return isPythonIdentifierStart(ch) || (ch >= '0' && ch <= '9')
}
