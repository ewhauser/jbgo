package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"maps"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/commandutil"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type Engine interface {
	Parse(name, script string) (*syntax.File, error)
	Run(ctx context.Context, exec *Execution) (*RunResult, error)
}

type InteractiveEngine interface {
	Interact(ctx context.Context, exec *Execution) (*InteractiveResult, error)
}

type Execution struct {
	Name              string
	Script            string
	Program           *syntax.File
	Args              []string
	StartupOptions    []string
	Interactive       bool
	Env               map[string]string
	Dir               string
	VisiblePWD        string
	HasVisiblePWD     bool
	BuiltinCommandDir string
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
	FS                gbfs.FileSystem
	Network           network.Client
	Registry          commands.CommandRegistry
	Policy            policy.Policy
	Trace             trace.Recorder
	Exec              func(context.Context, *commands.ExecutionRequest) (*commands.ExecutionResult, error)
	Interact          func(context.Context, *commands.InteractiveRequest) (*commands.InteractiveResult, error)
}

type RunResult struct {
	FinalEnv    map[string]string
	ShellExited bool
}

type InteractiveResult struct {
	ExitCode int
}

type resolvedCommand struct {
	command commands.Command
	name    string
	path    string
	source  string
	args    []string
}

type MVdan struct {
	parser *syntax.Parser
}

const hostRunnerDir = "/"

func New() *MVdan {
	return &MVdan{
		parser: syntax.NewParser(),
	}
}

func (m *MVdan) Parse(name, script string) (*syntax.File, error) {
	return m.parser.Parse(strings.NewReader(script), name)
}

func (m *MVdan) parseUserProgram(name, script string) (*syntax.File, error) {
	return m.Parse(name, prependRuntimePreludeLines(script))
}

func (m *MVdan) Run(ctx context.Context, exec *Execution) (result *RunResult, runErr error) {
	if exec == nil {
		exec = &Execution{}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if exec.Stderr != nil {
				_, _ = fmt.Fprintln(exec.Stderr, sanitizeRunnerPanic(recovered))
			}
			result = &RunResult{FinalEnv: envMapFromVars(nil)}
			runErr = interp.ExitStatus(2)
		}
	}()
	if exec.Dir == "" {
		exec.Dir = "/"
	}

	validationProgram := exec.Program
	if validationProgram == nil {
		parsed, err := m.parseUserProgram(exec.Name, exec.Script)
		if err != nil {
			return nil, err
		}
		validationProgram = parsed
	}
	if violation := validateExecutionBudgets(validationProgram, exec.Policy); violation != nil {
		if exec.Stderr != nil {
			_, _ = fmt.Fprintln(exec.Stderr, violation.Error())
		}
		return &RunResult{FinalEnv: envMapFromVars(nil)}, interp.ExitStatus(126)
	}
	if invalid := validateInterpreterSafety(validationProgram); invalid != nil {
		if exec.Stderr != nil {
			_, _ = fmt.Fprintln(exec.Stderr, invalid.Error())
		}
		return &RunResult{FinalEnv: envMapFromVars(nil)}, interp.ExitStatus(2)
	}
	program := exec.Program
	if program == nil {
		parsed, err := m.parseUserProgram(exec.Name, exec.Script)
		if err != nil {
			return nil, err
		}
		program = parsed
	}

	if exec.Stdin == nil {
		exec.Stdin = strings.NewReader("")
	}
	if exec.Stdout == nil {
		exec.Stdout = io.Discard
	}
	if exec.Stderr == nil {
		exec.Stderr = io.Discard
	}
	if exec.Trace == nil {
		exec.Trace = trace.NopRecorder{}
	}
	budget := newExecutionBudget(exec.Policy, runtimePreludeLineCount())
	if err := instrumentLoopBudgets(program, exec.Policy); err != nil {
		return nil, err
	}

	runner, err := interp.New(m.runnerOptions(exec, budget)...)
	if err != nil {
		return nil, err
	}
	if err := m.bootstrapRunner(ctx, runner, exec); err != nil {
		return &RunResult{FinalEnv: envMapFromVars(runner.Vars), ShellExited: runner.Exited()}, err
	}
	if err := applyRunnerParams(runner, exec.StartupOptions, exec.Args); err != nil {
		return &RunResult{FinalEnv: envMapFromVars(runner.Vars), ShellExited: runner.Exited()}, err
	}
	runErr = runner.Run(ctx, program)
	return &RunResult{
		FinalEnv:    envMapFromVars(runner.Vars),
		ShellExited: runner.Exited(),
	}, runErr
}

func (m *MVdan) runnerOptions(exec *Execution, budget *executionBudget) []interp.RunnerOption {
	options := []interp.RunnerOption{
		interp.Env(expand.ListEnviron(envPairs(exec.Env)...)),
		runnerDirOption(hostRunnerDir),
		interp.StdIO(exec.Stdin, exec.Stdout, exec.Stderr),
		interp.OpenHandler(m.openHandler(exec)),
		interp.ReadDirHandler2(m.readDirHandler(exec)),
		interp.StatHandler(m.statHandler(exec)),
		interp.CallHandler(m.callHandler(exec, budget)),
		interp.ExecHandlers(func(_ interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return m.execHandler(exec, budget)
		}),
	}
	if exec.Interactive {
		options = append(options, interp.Interactive(true))
	}
	return options
}

func runnerParamArgs(startupOptions, args []string) []string {
	out := make([]string, 0, len(startupOptions)+len(args)+1)
	for _, option := range startupOptions {
		if strings.TrimSpace(option) == "" {
			continue
		}
		out = append(out, "-o", option)
	}
	if len(args) == 0 {
		if len(out) == 0 {
			return nil
		}
		return out
	}
	out = append(out, "--")
	out = append(out, args...)
	return out
}

func sanitizeRunnerPanic(recovered any) string {
	message := fmt.Sprint(recovered)
	switch {
	case strings.HasPrefix(message, "unhandled >& arg:"),
		strings.HasPrefix(message, "unhandled > arg:"),
		strings.HasPrefix(message, "unhandled < arg:"),
		strings.HasPrefix(message, "unsupported redirect fd:"),
		strings.HasPrefix(message, "unhandled redirect op:"):
		return "invalid redirection"
	default:
		return "shell execution failed"
	}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var status interp.ExitStatus
	if errors.As(err, &status) {
		return int(status)
	}
	return 1
}

func IsExitStatus(err error) bool {
	if err == nil {
		return false
	}
	var status interp.ExitStatus
	return errors.As(err, &status)
}

func (m *MVdan) openHandler(exec *Execution) interp.OpenHandlerFunc {
	return func(ctx context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(virtualDir(state.Env, state.Dir), name)

		if canRead := flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY; canRead {
			if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionRead, abs); err != nil {
				recordPolicyDenied(exec.Trace, err, string(policy.FileActionRead), abs, "", 126, "")
				return nil, handlerPathError(ctx, state.Stderr, "open", abs, err)
			}
		}
		if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
			if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionWrite, abs); err != nil {
				recordPolicyDenied(exec.Trace, err, string(policy.FileActionWrite), abs, "", 126, "")
				return nil, handlerPathError(ctx, state.Stderr, "open", abs, err)
			}
		}

		recordFile(exec.Trace, string(policy.FileActionRead), abs)
		if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
			recordFile(exec.Trace, string(policy.FileActionWrite), abs)
		}

		file, err := exec.FS.OpenFile(ctx, abs, flag, stdfs.FileMode(perm))
		if err != nil {
			return nil, err
		}
		if mutationAction := fileMutationAction(flag); mutationAction != "" {
			recordFileMutation(exec.Trace, mutationAction, abs, "", "")
		}
		return commandutil.WrapRedirectedFile(file, abs, flag), nil
	}
}

func (m *MVdan) readDirHandler(exec *Execution) interp.ReadDirHandlerFunc2 {
	return func(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(virtualDir(state.Env, state.Dir), name)
		if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionReadDir, abs); err != nil {
			recordPolicyDenied(exec.Trace, err, string(policy.FileActionReadDir), abs, "", 126, "")
			return nil, handlerPathError(ctx, state.Stderr, "readdir", abs, err)
		}
		recordFile(exec.Trace, string(policy.FileActionReadDir), abs)
		return exec.FS.ReadDir(ctx, abs)
	}
}

func (m *MVdan) statHandler(exec *Execution) interp.StatHandlerFunc {
	return func(ctx context.Context, name string, _ bool) (stdfs.FileInfo, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(virtualDir(state.Env, state.Dir), name)
		if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionStat, abs); err != nil {
			recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), abs, "", 126, "")
			return nil, handlerPathError(ctx, state.Stderr, "stat", abs, err)
		}
		recordFile(exec.Trace, string(policy.FileActionStat), abs)
		return exec.FS.Stat(ctx, abs)
	}
}

func (m *MVdan) callHandler(exec *Execution, budget *executionBudget) interp.CallHandlerFunc {
	return func(ctx context.Context, args []string) ([]string, error) {
		if len(args) == 0 {
			return args, nil
		}
		if isInternalHelperCommand(args[0]) {
			return args, nil
		}
		hc := interp.HandlerCtx(ctx)
		if err := budget.beforeCommand(ctx, hc.Pos); err != nil {
			return nil, err
		}
		commandInfo := traceCommandInfo(args, interp.IsBuiltin(args[0]), &commandTraceResolution{
			Dir:      virtualDir(hc.Env, hc.Dir),
			Position: hc.Pos.String(),
		})
		recordCommand(exec.Trace, trace.EventCallExpanded, commandInfo)

		if interp.IsBuiltin(args[0]) && shouldRewriteBuiltin(args[0]) {
			if _, ok := exec.Registry.Lookup(args[0]); ok {
				rewritten := make([]string, len(args))
				copy(rewritten[1:], args[1:])
				rewritten[0] = path.Join(builtinCommandDir(exec), args[0])
				return rewritten, nil
			}
		}

		if interp.IsBuiltin(args[0]) {
			if err := allowBuiltin(ctx, exec.Policy, args[0], args); err != nil {
				recordPolicyDenied(exec.Trace, err, "", "", args[0], 126, "builtin")
				return nil, shellFailure(ctx, 126, "%v", err)
			}
		}

		return args, nil
	}
}

func shouldRewriteBuiltin(name string) bool {
	switch name {
	case "true", "false":
		return false
	default:
		return true
	}
}

func builtinCommandDir(exec *Execution) string {
	if exec == nil || strings.TrimSpace(exec.BuiltinCommandDir) == "" {
		return "/bin"
	}
	return gbfs.Clean(exec.BuiltinCommandDir)
}

func (m *MVdan) execHandler(exec *Execution, budget *executionBudget) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if len(args) == 0 {
			return nil
		}
		if args[0] == loopIterCommandName {
			return budget.beforeLoopIteration(ctx, args[1:])
		}

		hc := interp.HandlerCtx(ctx)
		virtualWD := virtualDir(hc.Env, hc.Dir)
		currentEnv := envMap(hc.Env)
		internal := isInternalHelperCommand(args[0])
		resolved, ok, err := lookupCommand(ctx, exec, virtualWD, hc.Env, args[0])
		if err != nil {
			if policy.IsDenied(err) {
				recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), "", args[0], 126, "")
				return shellFailure(ctx, 126, "%v", err)
			}
			return err
		}
		start := time.Now().UTC()
		if !ok {
			return shellFailure(ctx, 127, "%s: command not found", args[0])
		}

		if !internal {
			if err := allowCommand(ctx, exec.Policy, resolved.name, args); err != nil {
				recordPolicyDenied(exec.Trace, err, "", resolved.path, resolved.name, 126, resolved.source)
				return shellFailure(ctx, 126, "%v", err)
			}
			recordCommand(exec.Trace, trace.EventCommandStart, traceCommandInfo(args, false, &commandTraceResolution{
				Dir:              virtualWD,
				Position:         hc.Pos.String(),
				ResolvedName:     resolved.name,
				ResolvedPath:     resolved.path,
				ResolutionSource: resolved.source,
			}))
		}

		invocationArgs := append([]string(nil), resolved.args...)
		invocationArgs = append(invocationArgs, args[1:]...)
		invocation := commands.NewInvocation(&commands.InvocationOptions{
			Args:       invocationArgs,
			Env:        currentEnv,
			Cwd:        virtualWD,
			Stdin:      hc.Stdin,
			Stdout:     hc.Stdout,
			Stderr:     hc.Stderr,
			FileSystem: exec.FS,
			Network:    exec.Network,
			Policy:     exec.Policy,
			Trace:      exec.Trace,
			Exec:       subexecInvoker(exec.Exec, currentEnv, virtualWD),
			Interact:   interactiveInvoker(exec.Interact, currentEnv, virtualWD),
			GetRegisteredCommands: func() []string {
				if exec.Registry == nil {
					return nil
				}
				return exec.Registry.Names()
			},
		})
		err = commands.RunCommand(ctx, resolved.command, invocation)
		if syncErr := syncCommandHistory(ctx, hc, currentEnv, invocation.Env); syncErr != nil {
			return syncErr
		}

		if err == nil {
			if !internal {
				recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(args, false, &commandTraceResolution{
					Dir:              virtualWD,
					Position:         hc.Pos.String(),
					ExitCode:         0,
					Duration:         time.Since(start),
					ResolvedName:     resolved.name,
					ResolvedPath:     resolved.path,
					ResolutionSource: resolved.source,
				}))
			}
			return nil
		}

		if code, ok := commands.ExitCode(err); ok {
			if message, ok := commands.DiagnosticMessage(err); ok && hc.Stderr != nil {
				_, _ = fmt.Fprintln(hc.Stderr, message)
			}
			if !internal {
				recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(args, false, &commandTraceResolution{
					Dir:              virtualWD,
					Position:         hc.Pos.String(),
					ExitCode:         code,
					Duration:         time.Since(start),
					ResolvedName:     resolved.name,
					ResolvedPath:     resolved.path,
					ResolutionSource: resolved.source,
				}))
			}
			return interp.ExitStatus(code)
		}

		if !internal {
			recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(args, false, &commandTraceResolution{
				Dir:              virtualWD,
				Position:         hc.Pos.String(),
				ExitCode:         1,
				Duration:         time.Since(start),
				ResolvedName:     resolved.name,
				ResolvedPath:     resolved.path,
				ResolutionSource: resolved.source,
			}))
		}
		return err
	}
}

func subexecInvoker(execFn func(context.Context, *commands.ExecutionRequest) (*commands.ExecutionResult, error), currentEnv map[string]string, currentDir string) func(context.Context, *commands.ExecutionRequest) (*commands.ExecutionResult, error) {
	if execFn == nil {
		return nil
	}
	return func(ctx context.Context, req *commands.ExecutionRequest) (*commands.ExecutionResult, error) {
		normalized := normalizeSubexecRequest(req, currentEnv, currentDir)
		return execFn(ctx, normalized)
	}
}

func interactiveInvoker(interactFn func(context.Context, *commands.InteractiveRequest) (*commands.InteractiveResult, error), currentEnv map[string]string, currentDir string) func(context.Context, *commands.InteractiveRequest) (*commands.InteractiveResult, error) {
	if interactFn == nil {
		return nil
	}
	return func(ctx context.Context, req *commands.InteractiveRequest) (*commands.InteractiveResult, error) {
		normalized := normalizeInteractiveRequest(req, currentEnv, currentDir)
		return interactFn(ctx, normalized)
	}
}

func normalizeSubexecRequest(req *commands.ExecutionRequest, currentEnv map[string]string, currentDir string) *commands.ExecutionRequest {
	if req == nil {
		req = &commands.ExecutionRequest{}
	}

	out := &commands.ExecutionRequest{
		Name:            req.Name,
		Interpreter:     req.Interpreter,
		PassthroughArgs: append([]string(nil), req.PassthroughArgs...),
		Script:          req.Script,
		Args:            append([]string(nil), req.Args...),
		StartupOptions:  append([]string(nil), req.StartupOptions...),
		Env:             mergeEnv(currentEnv, req.Env),
		WorkDir:         req.WorkDir,
		Timeout:         req.Timeout,
		ReplaceEnv:      true,
		Interactive:     req.Interactive,
		Stdin:           req.Stdin,
		Stdout:          req.Stdout,
		Stderr:          req.Stderr,
	}
	if req.ReplaceEnv {
		out.Env = mergeEnv(nil, req.Env)
	}
	if out.WorkDir == "" {
		out.WorkDir = currentDir
	}
	return out
}

func normalizeInteractiveRequest(req *commands.InteractiveRequest, currentEnv map[string]string, currentDir string) *commands.InteractiveRequest {
	if req == nil {
		req = &commands.InteractiveRequest{}
	}
	out := &commands.InteractiveRequest{
		Name:           req.Name,
		Args:           append([]string(nil), req.Args...),
		StartupOptions: append([]string(nil), req.StartupOptions...),
		Env:            mergeEnv(currentEnv, req.Env),
		WorkDir:        req.WorkDir,
		ReplaceEnv:     true,
		Stdin:          req.Stdin,
		Stdout:         req.Stdout,
		Stderr:         req.Stderr,
	}
	if req.ReplaceEnv {
		out.Env = mergeEnv(nil, req.Env)
	}
	if out.WorkDir == "" {
		out.WorkDir = currentDir
	}
	return out
}

func applyRunnerParams(runner *interp.Runner, startupOptions, args []string) error {
	params := runnerParamArgs(startupOptions, args)
	if len(params) == 0 {
		return nil
	}
	return interp.Params(params...)(runner)
}

func (m *MVdan) bootstrapRunner(ctx context.Context, runner *interp.Runner, exec *Execution) error {
	if runner == nil {
		return nil
	}
	bootstrap, err := m.Parse(exec.Name, withRuntimePrelude(exec.Dir, exec.VisiblePWD, exec.HasVisiblePWD, ""))
	if err != nil {
		return err
	}
	if violation := validateExecutionBudgets(bootstrap, exec.Policy); violation != nil {
		if exec.Stderr != nil {
			_, _ = fmt.Fprintln(exec.Stderr, violation.Error())
		}
		return interp.ExitStatus(126)
	}
	if invalid := validateInterpreterSafety(bootstrap); invalid != nil {
		if exec.Stderr != nil {
			_, _ = fmt.Fprintln(exec.Stderr, invalid.Error())
		}
		return interp.ExitStatus(2)
	}
	if err := instrumentLoopBudgets(bootstrap, exec.Policy); err != nil {
		return err
	}
	return runner.Run(ctx, bootstrap)
}

func mergeEnv(base, override map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(override))
	maps.Copy(out, base)
	maps.Copy(out, override)
	return out
}

func allowCommand(ctx context.Context, pol policy.Policy, name string, argv []string) error {
	if pol == nil {
		return nil
	}
	return pol.AllowCommand(ctx, name, argv)
}

func allowBuiltin(ctx context.Context, pol policy.Policy, name string, argv []string) error {
	if pol == nil {
		return nil
	}
	return pol.AllowBuiltin(ctx, name, argv)
}

func allowPath(ctx context.Context, pol policy.Policy, fsys gbfs.FileSystem, action policy.FileAction, name string) error {
	return policy.CheckPath(ctx, pol, fsys, action, name)
}

func envPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, env[key]))
	}
	return pairs
}

func envMap(env expand.Environ) map[string]string {
	out := make(map[string]string)
	env.Each(func(name string, vr expand.Variable) bool {
		if vr.IsSet() {
			out[name] = vr.String()
		}
		return true
	})
	return out
}

func envMapFromVars(vars map[string]expand.Variable) map[string]string {
	if len(vars) == 0 {
		return nil
	}
	out := make(map[string]string, len(vars))
	for name, vr := range vars {
		if !vr.IsSet() {
			continue
		}
		out[name] = vr.String()
	}
	return out
}

func pathError(op, p string, err error) error {
	return &os.PathError{Op: op, Path: p, Err: err}
}

func handlerPathError(ctx context.Context, stderr io.Writer, op, name string, err error) error {
	if policy.IsDenied(err) {
		return shellFailureToWriter(ctx, stderr, 126, "%v", err)
	}
	return pathError(op, name, err)
}

func lookupCommand(ctx context.Context, exec *Execution, dir string, env expand.Environ, name string) (_ *resolvedCommand, ok bool, err error) {
	if strings.HasPrefix(name, "__jb_") {
		cmd, ok := exec.Registry.Lookup(name)
		if !ok {
			return nil, false, nil
		}
		return &resolvedCommand{
			command: cmd,
			name:    name,
			path:    name,
			source:  "internal-helper",
		}, true, nil
	}
	if strings.Contains(name, "/") {
		return lookupCommandPath(ctx, exec, dir, name, "path", name)
	}

	for _, pathDir := range pathDirs(env, dir) {
		fullPath := gbfs.Resolve(pathDir, name)
		resolved, ok, err := lookupCommandPath(ctx, exec, dir, fullPath, "path-search", name)
		if err != nil {
			return nil, false, err
		}
		if ok {
			return resolved, true, nil
		}
	}

	return nil, false, nil
}

func lookupCommandPath(ctx context.Context, exec *Execution, dir, name, source, commandName string) (_ *resolvedCommand, ok bool, err error) {
	fullPath := gbfs.Resolve(dir, name)
	if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionStat, fullPath); err != nil {
		recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), fullPath, commandName, 126, source)
		return nil, false, err
	}
	info, err := exec.FS.Stat(ctx, fullPath)
	if err != nil || info.IsDir() {
		return nil, false, nil
	}

	resolvedName := path.Base(fullPath)
	cmd, ok := exec.Registry.Lookup(resolvedName)
	if ok {
		return &resolvedCommand{
			command: cmd,
			name:    resolvedName,
			path:    fullPath,
			source:  source,
		}, true, nil
	}

	script, ok, err := resolveShebangCommand(ctx, exec, fullPath)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	script.path = fullPath
	script.source = "shebang"
	return script, true, nil
}

func pathDirs(env expand.Environ, dir string) []string {
	pathValue := strings.TrimSpace(env.Get("PATH").String())
	if pathValue == "" {
		return nil
	}

	dirs := make([]string, 0, strings.Count(pathValue, ":")+1)
	for entry := range strings.SplitSeq(pathValue, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			entry = "."
		}
		dirs = append(dirs, gbfs.Resolve(dir, entry))
	}
	return dirs
}

func resolveShebangCommand(ctx context.Context, exec *Execution, fullPath string) (_ *resolvedCommand, ok bool, err error) {
	file, err := exec.FS.Open(ctx, fullPath)
	if err != nil {
		return nil, false, nil
	}
	defer func() {
		_ = file.Close()
	}()

	line, ok, err := readShebangLine(file)
	if err != nil || !ok {
		return nil, false, err
	}
	interpreter, argv, ok := parseShebangInterpreter(line)
	if !ok {
		return nil, false, nil
	}
	cmd, ok := exec.Registry.Lookup(interpreter)
	if !ok {
		return nil, false, nil
	}
	return &resolvedCommand{
		command: cmd,
		name:    interpreter,
		args:    append(argv, fullPath),
	}, true, nil
}

func readShebangLine(r io.Reader) (line string, ok bool, err error) {
	var data [256]byte
	n, err := r.Read(data[:])
	switch {
	case err == nil:
	case errors.Is(err, io.EOF):
	default:
		return "", false, err
	}
	if n < 2 || string(data[:2]) != "#!" {
		return "", false, nil
	}
	line = string(data[:n])
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	return strings.TrimSpace(line[2:]), true, nil
}

func parseShebangInterpreter(line string) (name string, args []string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil, false
	}
	name = path.Base(fields[0])
	if name == "" || name == "." || name == "/" {
		return "", nil, false
	}
	if len(fields) > 1 {
		args = append(args, fields[1:]...)
	}
	return name, args, true
}

func shellFailure(ctx context.Context, code int, format string, args ...any) error {
	hc := interp.HandlerCtx(ctx)
	return shellFailureToWriter(ctx, hc.Stderr, code, format, args...)
}

func shellFailureToWriter(_ context.Context, stderr io.Writer, code int, format string, args ...any) error {
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, format+"\n", args...)
	}
	return interp.ExitStatus(code)
}

func virtualDir(env expand.Environ, dir string) string {
	if internalPWD := strings.TrimSpace(env.Get("__JB_PWD").String()); strings.HasPrefix(internalPWD, "/") {
		return gbfs.Clean(internalPWD)
	}
	if pwd := strings.TrimSpace(env.Get("PWD").String()); strings.HasPrefix(pwd, "/") {
		return gbfs.Clean(pwd)
	}
	return gbfs.Clean(dir)
}

type resolvedHandlerState struct {
	Env    expand.Environ
	Dir    string
	Stderr io.Writer
}

func handlerState(ctx context.Context, exec *Execution) resolvedHandlerState {
	if hc, ok := optionalHandlerCtx(ctx); ok {
		return resolvedHandlerState{
			Env:    hc.Env,
			Dir:    hc.Dir,
			Stderr: hc.Stderr,
		}
	}
	return resolvedHandlerState{
		Env:    expand.ListEnviron(envPairs(exec.Env)...),
		Dir:    exec.Dir,
		Stderr: exec.Stderr,
	}
}

func optionalHandlerCtx(ctx context.Context) (_ interp.HandlerContext, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return interp.HandlerCtx(ctx), true
}

func withRuntimePrelude(dir, visiblePWD string, hasVisiblePWD bool, script string) string {
	const preludeTemplate = `
__JB_PWD='%s'
OLDPWD=$__JB_PWD
%s
export PWD OLDPWD

pwd() {
	command /bin/pwd "$@"
}

cd() {
	old=$__JB_PWD
	show=
	case "$#" in
		0)
			target=$HOME
			;;
		1)
			target=$1
			;;
		*)
			printf 'cd: usage: cd [dir]\n' >&2
			return 2
			;;
	esac
	if [ "$target" = "-" ]; then
		target=$OLDPWD
		show=1
	fi
	next="$(__jb_cd_resolve "$__JB_PWD" "$target")" || return $?
	OLDPWD=$old
	__JB_PWD=$next
	PWD=$next
	export PWD OLDPWD
	if [ -n "$show" ]; then
		printf '%%s\n' "$PWD"
	fi
	}
`

	pwdAssignment := "PWD=$__JB_PWD"
	if hasVisiblePWD {
		pwdAssignment = "PWD=" + shellSingleQuote(visiblePWD)
	}
	return fmt.Sprintf(preludeTemplate, shellSingleQuote(dir), pwdAssignment) + "\n" + script
}

type commandTraceResolution struct {
	Dir              string
	Position         string
	ExitCode         int
	Duration         time.Duration
	ResolvedName     string
	ResolvedPath     string
	ResolutionSource string
}

func traceCommandInfo(argv []string, builtin bool, resolved *commandTraceResolution) *trace.CommandEvent {
	if len(argv) == 0 {
		return nil
	}

	info := &trace.CommandEvent{
		Name:    argv[0],
		Argv:    append([]string(nil), argv...),
		Builtin: builtin,
	}
	if resolved != nil {
		info.Dir = resolved.Dir
		info.Position = resolved.Position
		info.ExitCode = resolved.ExitCode
		info.Duration = resolved.Duration
		info.ResolvedName = resolved.ResolvedName
		info.ResolvedPath = resolved.ResolvedPath
		info.ResolutionSource = resolved.ResolutionSource
	}
	if builtin && info.ResolutionSource == "" {
		info.ResolutionSource = "builtin"
		info.ResolvedName = info.Name
	}
	return info
}

func recordCommand(rec trace.Recorder, kind trace.Kind, command *trace.CommandEvent) {
	if rec == nil || command == nil {
		return
	}
	rec.Record(&trace.Event{
		Kind:    kind,
		At:      time.Now().UTC(),
		Command: command,
	})
}

func recordFile(rec trace.Recorder, action, filePath string) {
	if rec == nil {
		return
	}
	rec.Record(&trace.Event{
		Kind: trace.EventFileAccess,
		At:   time.Now().UTC(),
		File: &trace.FileEvent{
			Action: action,
			Path:   filePath,
		},
	})
}

func recordFileMutation(rec trace.Recorder, action, filePath, fromPath, toPath string) {
	if rec == nil {
		return
	}
	rec.Record(&trace.Event{
		Kind: trace.EventFileMutation,
		At:   time.Now().UTC(),
		File: &trace.FileEvent{
			Action:   action,
			Path:     filePath,
			FromPath: fromPath,
			ToPath:   toPath,
		},
	})
}

func recordPolicyDenied(rec trace.Recorder, err error, action, filePath, command string, exitCode int, resolutionSource string) {
	if rec == nil || !policy.IsDenied(err) {
		return
	}

	denied := &policy.DeniedError{}
	if !errors.As(err, &denied) {
		return
	}
	rec.Record(&trace.Event{
		Kind: trace.EventPolicyDenied,
		At:   time.Now().UTC(),
		Policy: &trace.PolicyEvent{
			Subject:          denied.Subject,
			Reason:           denied.Reason,
			Action:           action,
			Path:             filePath,
			Command:          command,
			ExitCode:         exitCode,
			ResolutionSource: resolutionSource,
		},
		Error: err.Error(),
	})
}

func fileMutationAction(flag int) string {
	if flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		return ""
	}
	if flag&os.O_APPEND != 0 {
		return "append"
	}
	return "write"
}

func shellSingleQuote(value string) string {
	return strings.ReplaceAll(value, `'`, `'"'"'`)
}

type executionBudget struct {
	maxCommandCount   int64
	maxLoopIterations int64
	preludeLines      uint
	count             atomic.Int64
	mu                sync.Mutex
	loopCounts        map[string]int64
}

func newExecutionBudget(pol policy.Policy, preludeLines uint) *executionBudget {
	if pol == nil {
		return &executionBudget{}
	}

	return &executionBudget{
		maxCommandCount:   pol.Limits().MaxCommandCount,
		maxLoopIterations: pol.Limits().MaxLoopIterations,
		preludeLines:      preludeLines,
		loopCounts:        make(map[string]int64),
	}
}

func (b *executionBudget) beforeCommand(ctx context.Context, pos syntax.Pos) error {
	if b == nil || b.maxCommandCount <= 0 {
		return nil
	}
	if pos.IsValid() && pos.Line() <= b.preludeLines {
		return nil
	}
	if b.count.Add(1) <= b.maxCommandCount {
		return nil
	}
	return shellFailure(ctx, 126, "too many commands executed (>%d), increase policy.Limits.MaxCommandCount", b.maxCommandCount)
}

func (b *executionBudget) beforeLoopIteration(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return shellFailure(ctx, 2, "%s: invalid invocation", loopIterCommandName)
	}
	if b == nil || b.maxLoopIterations <= 0 {
		return nil
	}

	loopKind := args[0]
	loopID := args[1]

	b.mu.Lock()
	b.loopCounts[loopID]++
	count := b.loopCounts[loopID]
	b.mu.Unlock()

	if count <= b.maxLoopIterations {
		return nil
	}
	return shellFailure(ctx, 126, "%s loop: too many iterations (%d), increase policy.Limits.MaxLoopIterations", loopKind, b.maxLoopIterations)
}

func runtimePreludeLineCount() uint {
	return uint(strings.Count(withRuntimePrelude("/", "", false, ""), "\n"))
}

func prependRuntimePreludeLines(script string) string {
	count := runtimePreludeLineCount()
	if count == 0 || script == "" {
		return script
	}
	return strings.Repeat("\n", int(count)) + script
}

func isInternalHelperCommand(name string) bool {
	return strings.HasPrefix(name, "__jb_")
}

var _ Engine = (*MVdan)(nil)
