package python

import (
	"bufio"
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
	"github.com/ewhauser/gbash/internal/commandutil"
	monty "github.com/ewhauser/gomonty"
	montyvfs "github.com/ewhauser/gomonty/vfs"
	"golang.org/x/term"
)

const (
	pythonEvalEntry              = ".gbash-python-eval.py"
	pythonStdinEntry             = ".gbash-python-stdin.py"
	pythonReplEntry              = ".gbash-python-repl.py"
	pythonReplDisplayEntry       = ".gbash-python-repl-display.py"
	pythonReplPrompt             = ">>> "
	pythonReplContinuationPrompt = "... "
)

type Python struct {
	name          string
	terminalInput func() io.Reader
}

type sourceKind int

const (
	sourceStdin sourceKind = iota
	sourceEval
	sourceFile
	sourceRepl
)

type pythonSource struct {
	kind      sourceKind
	code      string
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
	return &Python{
		name:          name,
		terminalInput: func() io.Reader { return os.Stdin },
	}
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
	if source.kind == sourceRepl {
		return c.runRepl(ctx, inv, source)
	}

	return c.runSource(ctx, inv, source)
}

func (c *Python) runSource(ctx context.Context, inv *commands.Invocation, source pythonSource) error {
	runner, err := monty.New(source.code, monty.CompileOptions{
		ScriptName: source.entryPath,
	})
	if err != nil {
		return pythonExit(inv, err)
	}

	_, err = runner.Run(ctx, monty.RunOptions{
		Print: monty.WriterPrintCallback(inv.Stdout),
		OS:    pythonOSHandler(ctx, inv),
	})
	if err != nil {
		return pythonExit(inv, err)
	}
	return nil
}

func (c *Python) runRepl(ctx context.Context, inv *commands.Invocation, source pythonSource) error {
	repl, err := monty.NewRepl(monty.ReplOptions{
		ScriptName: source.entryPath,
	})
	if err != nil {
		return pythonExit(inv, err)
	}

	displayRunner, err := monty.New("repr(_gbash_repl_value)", monty.CompileOptions{
		ScriptName: gbfs.Resolve(inv.Cwd, pythonReplDisplayEntry),
		Inputs:     []string{"_gbash_repl_value"},
	})
	if err != nil {
		return pythonExit(inv, err)
	}

	stdin := inv.Stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	reader := bufio.NewReader(stdin)
	handler := pythonOSHandler(ctx, inv)
	prompt := pythonReplPrompt
	usingTerminalInput := false
	var pending strings.Builder

	for {
		_, _ = io.WriteString(inv.Stdout, prompt)

		line, readErr := reader.ReadString('\n')
		if line == "" && errors.Is(readErr, io.EOF) && pending.Len() == 0 && !usingTerminalInput {
			if terminal := c.terminalReader(inv); terminal != nil {
				reader = bufio.NewReader(terminal)
				usingTerminalInput = true
				line, readErr = reader.ReadString('\n')
			}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if pending.Len() == 0 && pythonReplExitCommand(line) {
			return nil
		}
		if line != "" {
			pending.WriteString(line)
		}
		if pending.Len() == 0 {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			prompt = pythonReplPrompt
			continue
		}

		value, err := repl.FeedRun(ctx, pending.String(), monty.FeedOptions{
			Print: monty.WriterPrintCallback(inv.Stdout),
			OS:    handler,
		})
		if err != nil {
			if pythonReplNeedsContinuation(err, line) && !errors.Is(readErr, io.EOF) {
				prompt = pythonReplContinuationPrompt
				continue
			}
			pending.Reset()
			prompt = pythonReplPrompt
			if pythonReplExitError(err) {
				return nil
			}
			pythonWriteError(inv.Stderr, err)
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			continue
		}

		pending.Reset()
		prompt = pythonReplPrompt
		if err := pythonWriteReplValue(ctx, inv.Stdout, displayRunner, value); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

func (c *Python) terminalReader(inv *commands.Invocation) io.Reader {
	if !pythonEnvTTY(inv.Env) || c == nil || c.terminalInput == nil {
		return nil
	}
	terminal := c.terminalInput()
	if terminal == nil {
		return nil
	}
	if inv != nil && terminal == inv.Stdin {
		return nil
	}
	return terminal
}

func (c *Python) classifySource(ctx context.Context, inv *commands.Invocation, matches *commands.ParsedCommand) (pythonSource, error) {
	if matches == nil {
		return c.prepareNoArgSource(ctx, inv)
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
		return c.prepareNoArgSource(ctx, inv)
	case 1:
		return c.prepareFileSource(ctx, inv, args[0])
	default:
		return pythonSource{}, commands.Exitf(inv, 1, "%s: extra script arguments are not supported", c.Name())
	}
}

func (c *Python) prepareNoArgSource(ctx context.Context, inv *commands.Invocation) (pythonSource, error) {
	if pythonInputIsTTY(inv) {
		return c.prepareReplSource(inv), nil
	}
	return c.prepareStdinSource(ctx, inv)
}

func (c *Python) prepareEvalSource(inv *commands.Invocation, code string) pythonSource {
	return pythonSource{
		kind:      sourceEval,
		code:      code,
		entryPath: gbfs.Resolve(inv.Cwd, pythonEvalEntry),
	}
}

func (c *Python) prepareReplSource(inv *commands.Invocation) pythonSource {
	return pythonSource{
		kind:      sourceRepl,
		entryPath: gbfs.Resolve(inv.Cwd, pythonReplEntry),
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
		entryPath: abs,
	}, nil
}

func pythonExit(inv *commands.Invocation, err error) error {
	if err == nil {
		return nil
	}
	return commands.Exitf(inv, 1, "%s", pythonErrorMessage(err))
}

func pythonOSHandler(ctx context.Context, inv *commands.Invocation) monty.OSHandler {
	env := montyvfs.MapEnvironment{}
	maps.Copy(env, inv.Env)
	return montyvfs.Handler(&invocationFS{
		requestContext: func() context.Context { return ctx },
		inv:            inv,
	}, env)
}

func pythonInputIsTTY(inv *commands.Invocation) bool {
	if inv == nil {
		return false
	}
	if inv.Stdin != nil {
		if meta, ok := inv.Stdin.(commandutil.RedirectMetadata); ok {
			if pythonRecognizedTTYPath(meta.RedirectPath()) {
				return true
			}
		}
		if fd, ok := inv.Stdin.(interface{ Fd() uintptr }); ok {
			if descriptor := fd.Fd(); descriptor != 0 {
				return term.IsTerminal(int(descriptor))
			}
		}
	}
	return pythonEnvTTY(inv.Env)
}

func pythonEnvTTY(env map[string]string) bool {
	if env == nil {
		return false
	}
	ttyValue := strings.TrimSpace(env["TTY"])
	if ttyValue == "" {
		return false
	}
	if !strings.HasPrefix(ttyValue, "/") {
		ttyValue = "/dev/" + strings.TrimLeft(ttyValue, "/")
	}
	return pythonRecognizedTTYPath(ttyValue)
}

func pythonRecognizedTTYPath(name string) bool {
	cleaned := path.Clean(strings.TrimSpace(name))
	switch {
	case cleaned == "/dev/tty", cleaned == "/dev/console":
		return true
	case path.Dir(cleaned) == "/dev" && strings.HasPrefix(path.Base(cleaned), "tty"):
		return true
	case path.Dir(cleaned) == "/dev/pts":
		base := path.Base(cleaned)
		return base != "" && base != "." && base != ".."
	default:
		return false
	}
}

func pythonReplExitCommand(line string) bool {
	switch strings.TrimSpace(line) {
	case "exit", "exit()", "quit", "quit()":
		return true
	default:
		return false
	}
}

func pythonReplExitError(err error) bool {
	return strings.HasPrefix(strings.TrimSpace(err.Error()), "SystemExit")
}

func pythonReplNeedsContinuation(err error, line string) bool {
	var syntaxErr *monty.SyntaxError
	if !errors.As(err, &syntaxErr) {
		return false
	}
	message := strings.TrimSpace(err.Error())
	switch {
	case strings.Contains(message, "unexpected EOF while parsing"):
		return true
	case strings.HasPrefix(message, "SyntaxError: Expected an indented block"):
		return strings.TrimSpace(line) != ""
	default:
		return false
	}
}

func pythonWriteReplValue(ctx context.Context, w io.Writer, displayRunner *monty.Runner, value monty.Value) error {
	if w == nil || string(value.Kind()) == "none" {
		return nil
	}
	repr, err := pythonReplValueString(ctx, displayRunner, value)
	if err != nil {
		return err
	}
	if repr == "" {
		return nil
	}
	_, err = io.WriteString(w, repr+"\n")
	return err
}

func pythonReplValueString(ctx context.Context, displayRunner *monty.Runner, value monty.Value) (string, error) {
	if displayRunner == nil {
		return pythonFallbackValueString(value), nil
	}
	repr, err := displayRunner.Run(ctx, monty.RunOptions{
		Inputs: map[string]monty.Value{
			"_gbash_repl_value": value,
		},
	})
	if err != nil {
		return "", err
	}
	if text, ok := repr.Raw().(string); ok {
		return text, nil
	}
	return repr.String(), nil
}

func pythonFallbackValueString(value monty.Value) string {
	switch string(value.Kind()) {
	case "repr", "cycle":
		if text, ok := value.Raw().(string); ok {
			return text
		}
	}
	return value.String()
}

func pythonWriteError(w io.Writer, err error) {
	if w == nil {
		return
	}
	message := strings.TrimSpace(pythonErrorMessage(err))
	if message == "" {
		return
	}
	_, _ = io.WriteString(w, message+"\n")
}

func pythonErrorMessage(err error) string {
	if err == nil {
		return ""
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
	return message
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
