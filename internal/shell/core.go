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
	"syscall"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/commandutil"
	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

type Execution struct {
	Name              string
	Interpreter       string
	PassthroughArgs   []string
	ScriptPath        string
	Script            string
	Command           []string
	Args              []string
	StartupOptions    []string
	StartupHome       string
	Interactive       bool
	Env               map[string]string
	Dir               string
	VisiblePWD        string
	HasVisiblePWD     bool
	BuiltinCommandDir string
	CompletionState   *shellstate.CompletionState
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
	procSubst         *procSubstManager
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

type core struct{}

var defaultShellCore = newShellCore()

var internalHelperCommands = map[string]struct{}{
	loopIterCommandName: {},
}

func newShellCore() *core {
	return &core{}
}

func Run(ctx context.Context, exec *Execution) (*RunResult, error) {
	return defaultShellCore.Run(ctx, exec)
}

func RunCommand(ctx context.Context, exec *Execution) (*RunResult, error) {
	return defaultShellCore.RunCommand(ctx, exec)
}

func Interact(ctx context.Context, exec *Execution) (*InteractiveResult, error) {
	return defaultShellCore.Interact(ctx, exec)
}

func (m *core) Run(ctx context.Context, exec *Execution) (result *RunResult, runErr error) {
	if exec == nil {
		exec = &Execution{}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if exec.Stderr != nil {
				_, _ = fmt.Fprintln(exec.Stderr, sanitizeRunnerPanic(recovered))
			}
			result = &RunResult{FinalEnv: nil}
			runErr = interp.ExitStatus(2)
		}
	}()
	if exec.Dir == "" {
		exec.Dir = "/"
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
	if exec.CompletionState == nil {
		exec.CompletionState = shellstate.NewCompletionState()
	}
	budget := newExecutionBudget(exec.Policy)

	effectiveExec := *exec
	cleanupProcSubst := withProcSubstScope(&effectiveExec)
	defer cleanupProcSubst()

	runner, err := m.newRunner(&effectiveExec, budget)
	if err != nil {
		return nil, err
	}
	runErr = runner.RunReaderWithMetadata(
		ctx,
		strings.NewReader(exec.Script),
		executionSourceName(exec),
		effectiveExec.ScriptPath,
		func(file *syntax.File) (map[*syntax.Stmt]*syntax.Stmt, error) {
			return compileChunk(file, effectiveExec.Policy, budget, budget.nextLoopNamespace())
		},
	)
	if code, ok := compilationExitStatus(runErr); ok {
		writeCompilationError(exec.Stderr, runErr)
		return &RunResult{FinalEnv: runner.ShellEnv()}, interp.ExitStatus(code)
	}
	return &RunResult{
		FinalEnv:    runner.ShellEnv(),
		ShellExited: runner.Exited(),
	}, runErr
}

func (m *core) RunCommand(ctx context.Context, exec *Execution) (*RunResult, error) {
	if exec == nil {
		exec = &Execution{}
	}
	if exec.Dir == "" {
		exec.Dir = "/"
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

	finalEnv := mergeEnv(nil, exec.Env)
	if len(exec.Command) == 0 {
		return &RunResult{FinalEnv: finalEnv}, nil
	}

	finalEnv, err := m.executeCommand(ctx, exec, &commandExecuteRequest{
		Argv:       exec.Command,
		VirtualWD:  gbfs.Clean(exec.Dir),
		Env:        expand.ListEnviron(envPairs(finalEnv)...),
		CurrentEnv: finalEnv,
		Stdin:      exec.Stdin,
		Stdout:     exec.Stdout,
		Stderr:     exec.Stderr,
	})
	return &RunResult{FinalEnv: finalEnv}, err
}

func (m *core) runnerConfig(exec *Execution, budget *executionBudget) *interp.RunnerConfig {
	cfg := &interp.RunnerConfig{
		Env:              expand.ListEnviron(envPairs(m.runnerEnv(exec))...),
		CallHandler:      m.callHandler(exec, budget),
		ExecHandler:      m.execHandler(exec, budget),
		OpenHandler:      m.openHandler(exec),
		ReadDirHandler:   m.readDirHandler(exec),
		StatHandler:      m.statHandler(exec),
		RealpathHandler:  m.realpathHandler(exec),
		ProcSubstHandler: m.procSubstHandler(exec),
	}
	if exec == nil {
		return cfg
	}
	cfg.StartupHome = exec.StartupHome
	cfg.Dir = exec.Dir
	cfg.Stdin = exec.Stdin
	cfg.Stdout = exec.Stdout
	cfg.Stderr = exec.Stderr
	cfg.Params = runnerParamArgs(exec.StartupOptions, exec.Args)
	cfg.Interactive = exec.Interactive
	cfg.LegacyBashCompat = exec.Interpreter == "bash" || exec.Interpreter == "sh"
	cfg.CommandString = executionUsesCommandString(exec)
	return cfg
}

func (m *core) runnerEnv(exec *Execution) map[string]string {
	if exec == nil {
		return nil
	}
	env := mergeEnv(nil, exec.Env)
	if exec.HasVisiblePWD && strings.TrimSpace(exec.VisiblePWD) != "" {
		env["PWD"] = exec.VisiblePWD
	}
	return env
}

func (m *core) procSubstHandler(exec *Execution) interp.ProcSubstHandlerFunc {
	return func(ctx context.Context, ps *syntax.ProcSubst) (*interp.ProcSubstEndpoint, error) {
		if exec == nil || exec.procSubst == nil {
			return nil, fmt.Errorf("process substitution unavailable")
		}
		return exec.procSubst.endpoint(ctx, ps)
	}
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

func executionSourceName(exec *Execution) string {
	if exec == nil {
		return "stdin"
	}
	switch {
	case strings.TrimSpace(exec.ScriptPath) != "":
		return exec.ScriptPath
	case strings.TrimSpace(exec.Name) != "":
		return exec.Name
	default:
		return "stdin"
	}
}

func executionUsesCommandString(exec *Execution) bool {
	if exec == nil {
		return false
	}
	args := exec.PassthroughArgs
	if len(args) > 0 && path.Base(strings.TrimSpace(args[0])) == exec.Interpreter {
		args = args[1:]
	}
	switch exec.Interpreter {
	case "bash", "sh":
		return hasBashCommandStringPassthroughArg(args)
	default:
		return false
	}
}

func hasBashCommandStringPassthroughArg(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "":
			continue
		case arg == "--":
			return false
		case arg == "--command" || strings.HasPrefix(arg, "--command="):
			return true
		case !strings.HasPrefix(arg, "-") || arg == "-":
			return false
		case strings.HasPrefix(arg, "--"):
			continue
		}
		shorts := arg[1:]
		for j := 0; j < len(shorts); j++ {
			switch shorts[j] {
			case 'c':
				return true
			case 'o':
				if j == len(shorts)-1 {
					i++
				}
				j = len(shorts)
			}
		}
	}
	return false
}

func attachParseErrorSourceLine(err error, script string) error {
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		return err
	}
	if parseErr.SourceLine != "" || !parseErr.WantsSourceLine() {
		return err
	}
	sourceLine := sourceLineAt(script, parseErr.Pos.Line())
	if sourceLine == "" {
		return err
	}
	parseErr.SourceLine = sourceLine
	return parseErr
}

func sourceLineAt(script string, lineNum uint) string {
	if lineNum == 0 {
		return ""
	}
	lines := strings.Split(script, "\n")
	idx := int(lineNum) - 1
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
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

func (m *core) openHandler(exec *Execution) interp.OpenHandlerFunc {
	return func(ctx context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(state.Dir, name)

		if canRead := flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY; canRead {
			if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionRead, abs); err != nil {
				recordPolicyDenied(exec.Trace, err, string(policy.FileActionRead), abs, "", "")
				return nil, handlerPathError(ctx, state.Stderr, "open", abs, err)
			}
		}
		if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
			if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionWrite, abs); err != nil {
				recordPolicyDenied(exec.Trace, err, string(policy.FileActionWrite), abs, "", "")
				return nil, handlerPathError(ctx, state.Stderr, "open", abs, err)
			}
		}

		recordFile(exec.Trace, string(policy.FileActionRead), abs)
		if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
			recordFile(exec.Trace, string(policy.FileActionWrite), abs)
		}

		file, err := exec.FS.OpenFile(ctx, abs, flag, perm)
		if err != nil {
			return nil, shellOpenPathError(ctx, state.Stderr, name, err)
		}
		if mutationAction := fileMutationAction(flag); mutationAction != "" {
			recordFileMutation(exec.Trace, mutationAction, abs, "", "")
		}
		return commandutil.WrapRedirectedFile(file, abs, flag), nil
	}
}

func (m *core) readDirHandler(exec *Execution) interp.ReadDirHandlerFunc {
	return func(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(state.Dir, name)
		if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionReadDir, abs); err != nil {
			recordPolicyDenied(exec.Trace, err, string(policy.FileActionReadDir), abs, "", "")
			return nil, handlerPathError(ctx, state.Stderr, "readdir", abs, err)
		}
		recordFile(exec.Trace, string(policy.FileActionReadDir), abs)
		return exec.FS.ReadDir(ctx, abs)
	}
}

func (m *core) statHandler(exec *Execution) interp.StatHandlerFunc {
	return func(ctx context.Context, name string, followSymlinks bool) (stdfs.FileInfo, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(state.Dir, name)
		action := policy.FileActionStat
		if !followSymlinks {
			action = policy.FileActionLstat
		}
		if err := allowPath(ctx, exec.Policy, exec.FS, action, abs); err != nil {
			recordPolicyDenied(exec.Trace, err, string(action), abs, "", "")
			return nil, handlerPathError(ctx, state.Stderr, "stat", abs, err)
		}
		recordFile(exec.Trace, string(action), abs)
		if followSymlinks {
			return exec.FS.Stat(ctx, abs)
		}
		return exec.FS.Lstat(ctx, abs)
	}
}

func (m *core) realpathHandler(exec *Execution) interp.RealpathHandlerFunc {
	return func(ctx context.Context, name string) (string, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(state.Dir, name)
		if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionStat, abs); err != nil {
			recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), abs, "", "")
			return "", handlerPathError(ctx, state.Stderr, "realpath", abs, err)
		}
		recordFile(exec.Trace, string(policy.FileActionStat), abs)
		return exec.FS.Realpath(ctx, abs)
	}
}

func (m *core) callHandler(exec *Execution, budget *executionBudget) interp.CallHandlerFunc {
	return func(ctx context.Context, args []string) ([]string, error) {
		if len(args) == 0 {
			return args, nil
		}
		if isInternalHelperCommand(args[0]) {
			return args, nil
		}
		hc, ok := interp.LookupHandlerContext(ctx)
		if !ok {
			return nil, fmt.Errorf("missing handler context")
		}
		fromBootstrap := hc.Internal
		if !fromBootstrap {
			if err := budget.beforeCommand(ctx); err != nil {
				return nil, err
			}
		}
		if !fromBootstrap {
			commandInfo := traceCommandInfo(args, interp.IsBuiltin(args[0]), &commandTraceResolution{
				Dir:      hc.Dir,
				Position: hc.Pos.String(),
			})
			recordCommand(exec.Trace, trace.EventCallExpanded, commandInfo)
		}

		if interp.IsBuiltin(args[0]) && shouldRewriteBuiltin(args[0]) {
			if _, ok := lookupRegistryCommand(exec, args[0]); ok {
				rewritten := make([]string, len(args))
				copy(rewritten[1:], args[1:])
				rewritten[0] = path.Join(builtinCommandDir(exec), args[0])
				return rewritten, nil
			}
		}

		if interp.IsBuiltin(args[0]) {
			if err := allowBuiltin(ctx, exec.Policy, args[0], args); err != nil {
				recordPolicyDenied(exec.Trace, err, "", "", args[0], "builtin")
				return nil, shellFailure(ctx, 126, "%v", err)
			}
			for _, invocation := range wrappedBuiltinInvocations(args) {
				if err := allowBuiltin(ctx, exec.Policy, invocation.name, invocation.argv); err != nil {
					recordPolicyDenied(exec.Trace, err, "", "", invocation.name, "builtin")
					return nil, shellFailure(ctx, 126, "%v", err)
				}
			}
		}

		return args, nil
	}
}

func shouldRewriteBuiltin(name string) bool {
	switch name {
	case "true", "false", "pwd", "cd", "dirs", "pushd", "popd", "type", "command", "source", ".",
		"printf", "test", "[":
		return false
	default:
		return true
	}
}

type builtinInvocation struct {
	name string
	argv []string
}

func wrappedBuiltinInvocations(args []string) []builtinInvocation {
	current := append([]string(nil), args...)
	invocations := make([]builtinInvocation, 0, 2)
	for len(current) > 0 {
		switch current[0] {
		case "builtin":
			if len(current) < 2 || !interp.IsBuiltin(current[1]) {
				return invocations
			}
			current = append([]string(nil), current[1:]...)
			invocations = append(invocations, builtinInvocation{
				name: current[0],
				argv: append([]string(nil), current...),
			})
		case "command":
			next := commandBuiltinTarget(current[1:])
			if len(next) == 0 || !interp.IsBuiltin(next[0]) {
				return invocations
			}
			current = append([]string(nil), next...)
			invocations = append(invocations, builtinInvocation{
				name: current[0],
				argv: append([]string(nil), current...),
			})
		default:
			return invocations
		}
	}
	return invocations
}

func commandBuiltinTarget(args []string) []string {
	show := false
	rest := append([]string(nil), args...)
	for len(rest) > 0 {
		switch rest[0] {
		case "-v":
			show = true
			rest = rest[1:]
		default:
			if show || len(rest) == 0 {
				return nil
			}
			return rest
		}
	}
	return nil
}

func builtinCommandDir(exec *Execution) string {
	if exec == nil || strings.TrimSpace(exec.BuiltinCommandDir) == "" {
		return "/bin"
	}
	return gbfs.Clean(exec.BuiltinCommandDir)
}

func (m *core) execHandler(exec *Execution, budget *executionBudget) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if len(args) == 0 {
			return nil
		}
		if args[0] == loopIterCommandName {
			return budget.beforeLoopIteration(ctx, args[1:])
		}

		hc, ok := interp.LookupHandlerContext(ctx)
		if !ok {
			return fmt.Errorf("missing handler context")
		}
		virtualWD := hc.Dir
		currentEnv := envMap(hc.Env)
		internal := isInternalHelperCommand(args[0])
		fromBootstrap := hc.Internal
		shellVars := shellstate.NewShellVarAssignments()
		_, err := m.executeCommand(ctx, exec, &commandExecuteRequest{
			Argv:          args,
			VirtualWD:     virtualWD,
			Env:           hc.Env,
			CurrentEnv:    currentEnv,
			Stdin:         hc.Stdin,
			Stdout:        hc.Stdout,
			Stderr:        hc.Stderr,
			Position:      hc.Pos.String(),
			Internal:      internal,
			FromBootstrap: fromBootstrap,
			PrepareInvoke: func(callCtx context.Context) context.Context {
				return shellstate.WithShellVarAssignments(callCtx, shellVars)
			},
			SyncEnv: func(callCtx context.Context, before, after map[string]string) error {
				if syncErr := syncShellVarAssignments(&hc, shellVars); syncErr != nil { //nolint:contextcheck // runner state mutation is intentionally in-process and context-free
					return syncErr
				}
				if syncErr := syncCommandHistory(&hc, before, after); syncErr != nil { //nolint:contextcheck // runner state mutation is intentionally in-process and context-free
					return syncErr
				}
				return syncUmaskEnv(&hc, before, after) //nolint:contextcheck // runner state mutation is intentionally in-process and context-free
			},
		})
		return err
	}
}

func invokeResolvedCommand(
	ctx context.Context,
	exec *Execution,
	resolved *resolvedCommand,
	argv []string,
	currentEnv map[string]string,
	virtualWD string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) (map[string]string, error) {
	invocationArgs := append([]string(nil), resolved.args...)
	if len(argv) > 1 {
		invocationArgs = append(invocationArgs, argv[1:]...)
	}
	invocation := commands.NewInvocation(&commands.InvocationOptions{
		Args:       invocationArgs,
		Env:        currentEnv,
		Cwd:        virtualWD,
		Stdin:      stdin,
		Stdout:     stdout,
		Stderr:     stderr,
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
	commandCtx := shellstate.WithCompletionState(ctx, completionStateForExecution(exec))
	return invocation.Env, commands.RunCommand(commandCtx, resolved.command, invocation)
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

func completionStateForExecution(exec *Execution) *shellstate.CompletionState {
	if exec == nil || exec.CompletionState == nil {
		return shellstate.NewCompletionState()
	}
	return exec.CompletionState
}

func normalizeSubexecRequest(req *commands.ExecutionRequest, currentEnv map[string]string, currentDir string) *commands.ExecutionRequest {
	if req == nil {
		req = &commands.ExecutionRequest{}
	}

	out := &commands.ExecutionRequest{
		Name:            req.Name,
		Interpreter:     req.Interpreter,
		PassthroughArgs: append([]string(nil), req.PassthroughArgs...),
		ScriptPath:      req.ScriptPath,
		Script:          req.Script,
		Command:         append([]string(nil), req.Command...),
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
		if !vr.IsSet() {
			delete(out, name)
			return true
		}
		out[name] = vr.String()
		return true
	})
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

func shellOpenPathError(ctx context.Context, stderr io.Writer, name string, err error) error {
	if policy.IsDenied(err) {
		return shellFailureToWriter(ctx, stderr, 126, "%v", err)
	}
	return shellWrappedOpenError(name, err)
}

func shellPathErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, syscall.EISDIR):
		return "Is a directory"
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		switch {
		case errors.Is(pathErr.Err, syscall.EISDIR):
			return "Is a directory"
		case errors.Is(pathErr.Err, stdfs.ErrNotExist):
			return "No such file or directory"
		case errors.Is(pathErr.Err, stdfs.ErrInvalid):
			return "Is a directory"
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "is a directory") {
		return "Is a directory"
	}
	return err.Error()
}

type shellOpenError struct {
	pathErr *os.PathError
	text    string
}

func shellWrappedOpenError(name string, err error) error {
	return &shellOpenError{
		pathErr: &os.PathError{Op: "open", Path: name, Err: err},
		text:    fmt.Sprintf("%s: %s", name, shellPathErrorText(err)),
	}
}

func (e *shellOpenError) Error() string {
	if e == nil {
		return ""
	}
	return e.text
}

func (e *shellOpenError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.pathErr
}

func lookupRegistryCommand(exec *Execution, name string) (commands.Command, bool) {
	if exec == nil || exec.Registry == nil {
		return nil, false
	}
	return exec.Registry.Lookup(name)
}

func lookupCommand(ctx context.Context, exec *Execution, dir string, env expand.Environ, name string) (_ *resolvedCommand, ok bool, err error) {
	if isInternalHelperCommand(name) {
		cmd, ok := lookupRegistryCommand(exec, name)
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
		recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), fullPath, commandName, source)
		return nil, false, err
	}
	info, err := exec.FS.Stat(ctx, fullPath)
	if err != nil || info.IsDir() {
		return nil, false, nil //nolint:nilerr // stat error means the file doesn't exist as a command
	}

	resolvedName := path.Base(fullPath)
	cmd, ok := lookupRegistryCommand(exec, resolvedName)
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
	shebangInterpreter, argv, ok := parseShebangInterpreter(line)
	if !ok {
		return nil, false, nil
	}
	cmd, ok := lookupRegistryCommand(exec, shebangInterpreter)
	if !ok {
		return nil, false, nil
	}
	return &resolvedCommand{
		command: cmd,
		name:    shebangInterpreter,
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
	if hc, ok := interp.LookupHandlerContext(ctx); ok {
		return shellFailureToWriter(ctx, hc.Stderr, code, format, args...)
	}
	return shellFailureToWriter(ctx, nil, code, format, args...)
}

func shellFailureToWriter(_ context.Context, stderr io.Writer, code int, format string, args ...any) error {
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, format+"\n", args...)
	}
	return interp.ExitStatus(code)
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
	return interp.LookupHandlerContext(ctx)
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

func recordPolicyDenied(rec trace.Recorder, err error, action, filePath, command, resolutionSource string) {
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
			ExitCode:         126,
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

type executionBudget struct {
	maxCommandCount   int64
	maxGlobOperations int64
	maxLoopIterations int64
	count             atomic.Int64
	globCount         atomic.Int64
	disabled          atomic.Int32
	loopNamespaces    atomic.Int64
	mu                sync.Mutex
	loopCounts        map[string]int64
}

func newExecutionBudget(pol policy.Policy) *executionBudget {
	if pol == nil {
		return &executionBudget{}
	}

	return &executionBudget{
		maxCommandCount:   pol.Limits().MaxCommandCount,
		maxGlobOperations: pol.Limits().MaxGlobOperations,
		maxLoopIterations: pol.Limits().MaxLoopIterations,
		loopCounts:        make(map[string]int64),
	}
}

func (b *executionBudget) beforeCommand(ctx context.Context) error {
	if b == nil || b.maxCommandCount <= 0 {
		return nil
	}
	if b.disabled.Load() > 0 {
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
	if b.disabled.Load() > 0 {
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

func (b *executionBudget) beforeGlob(ops int64) error {
	if b == nil || b.maxGlobOperations <= 0 || ops <= 0 {
		return nil
	}
	if b.disabled.Load() > 0 {
		return nil
	}
	if b.globCount.Add(ops) <= b.maxGlobOperations {
		return nil
	}
	return &budgetViolation{
		message: fmt.Sprintf("Glob operation limit exceeded (%d), increase policy.Limits.MaxGlobOperations", b.maxGlobOperations),
	}
}

func (b *executionBudget) nextLoopNamespace() string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("chunk%d", b.loopNamespaces.Add(1))
}

func isInternalHelperCommand(name string) bool {
	_, ok := internalHelperCommands[name]
	return ok
}

const umaskEnvVar = "GBASH_UMASK"

func syncUmaskEnv(hc *interp.HandlerContext, before, after map[string]string) error {
	if hc == nil {
		return nil
	}
	beforeValue, beforeOK := before[umaskEnvVar]
	afterValue, afterOK := after[umaskEnvVar]
	if beforeOK == afterOK && beforeValue == afterValue {
		return nil
	}
	if !afterOK {
		return hc.UnsetShellVar(umaskEnvVar)
	}
	return hc.SetShellVar(umaskEnvVar, expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      afterValue,
		Exported: true,
	})
}

func syncShellVarAssignments(hc *interp.HandlerContext, assignments *shellstate.ShellVarAssignments) error {
	if hc == nil || assignments == nil {
		return nil
	}
	updates := assignments.Snapshot()
	if len(updates) == 0 {
		return nil
	}
	names := make([]string, 0, len(updates))
	for name := range updates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		update := updates[name]
		if update.Unset {
			if err := hc.UnsetShellVar(name); err != nil {
				return err
			}
			continue
		}
		if err := hc.SetShellVar(name, expand.Variable{
			Set:  true,
			Kind: expand.String,
			Str:  update.Value,
		}); err != nil {
			return err
		}
	}
	return nil
}
