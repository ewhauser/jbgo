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
	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/internal/shfork/expand"
	"github.com/ewhauser/gbash/internal/shfork/interp"
	"github.com/ewhauser/gbash/internal/shfork/syntax"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

type Engine interface {
	Parse(name, script string) (*syntax.File, error)
	Run(ctx context.Context, exec *Execution) (*RunResult, error)
	RunCommand(ctx context.Context, exec *Execution) (*RunResult, error)
}

type InteractiveEngine interface {
	Interact(ctx context.Context, exec *Execution) (*InteractiveResult, error)
}

type Execution struct {
	Name              string
	ScriptPath        string
	Script            string
	Command           []string
	Program           *syntax.File
	Args              []string
	StartupOptions    []string
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
	pipelineSubshells map[*syntax.Stmt]*syntax.Stmt
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
	parser   *syntax.Parser
	parserMu sync.Mutex
}

var internalHelperCommands = map[string]struct{}{
	loopIterCommandName: {},
}

func New() *MVdan {
	return &MVdan{
		parser: syntax.NewParser(),
	}
}

func (m *MVdan) Parse(name, script string) (*syntax.File, error) {
	m.parserMu.Lock()
	defer m.parserMu.Unlock()
	return m.parser.Parse(strings.NewReader(script), name)
}

func (m *MVdan) parseUserProgram(name, script string) (*syntax.File, error) {
	return m.Parse(name, script)
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
		parsed, err := m.parseUserProgram(executionSourceName(exec), exec.Script)
		if err != nil {
			return nil, err
		}
		validationProgram = parsed
	}
	validationPipelineSubshells := normalizeExecutionProgram(validationProgram)
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
		parsed, err := m.parseUserProgram(executionSourceName(exec), exec.Script)
		if err != nil {
			return nil, err
		}
		program = parsed
	}
	if program != validationProgram {
		validationPipelineSubshells = normalizeExecutionProgram(program)
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
	if err := instrumentLoopBudgets(program, exec.Policy); err != nil {
		return nil, err
	}

	effectiveExec := *exec
	effectiveExec.pipelineSubshells = validationPipelineSubshells
	cleanupProcSubst := withProcSubstScope(&effectiveExec)
	defer cleanupProcSubst()

	runner, err := interp.NewVirtual(m.runnerConfig(&effectiveExec, budget), m.runnerOptions(&effectiveExec, budget)...)
	if err != nil {
		return nil, err
	}
	if err := applyRunnerParams(runner, effectiveExec.StartupOptions, effectiveExec.Args); err != nil {
		return &RunResult{FinalEnv: envMapFromVars(runner.Vars), ShellExited: runner.Exited()}, err
	}
	runErr = runner.Run(ctx, program)
	return &RunResult{
		FinalEnv:    envMapFromVars(runner.Vars),
		ShellExited: runner.Exited(),
	}, runErr
}

func (m *MVdan) RunCommand(ctx context.Context, exec *Execution) (*RunResult, error) {
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

	env := expand.ListEnviron(envPairs(finalEnv)...)
	virtualWD := gbfs.Clean(exec.Dir)
	resolved, ok, err := lookupCommand(ctx, exec, virtualWD, env, exec.Command[0])
	if err != nil {
		if policy.IsDenied(err) {
			recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), "", exec.Command[0], "")
			return &RunResult{FinalEnv: finalEnv}, shellFailureToWriter(ctx, exec.Stderr, 126, "%v", err)
		}
		return &RunResult{FinalEnv: finalEnv}, err
	}
	start := time.Now().UTC()
	if !ok {
		return &RunResult{FinalEnv: finalEnv}, shellFailureToWriter(ctx, exec.Stderr, 127, "%s: command not found", exec.Command[0])
	}
	if err := allowCommand(ctx, exec.Policy, resolved.name, exec.Command); err != nil {
		recordPolicyDenied(exec.Trace, err, "", resolved.path, resolved.name, resolved.source)
		return &RunResult{FinalEnv: finalEnv}, shellFailureToWriter(ctx, exec.Stderr, 126, "%v", err)
	}
	recordCommand(exec.Trace, trace.EventCommandStart, traceCommandInfo(exec.Command, false, &commandTraceResolution{
		Dir:              virtualWD,
		ResolvedName:     resolved.name,
		ResolvedPath:     resolved.path,
		ResolutionSource: resolved.source,
	}))

	finalEnv, err = invokeResolvedCommand(ctx, exec, resolved, exec.Command, finalEnv, virtualWD, exec.Stdin, exec.Stdout, exec.Stderr)
	result := &RunResult{FinalEnv: finalEnv}
	if err == nil {
		recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(exec.Command, false, &commandTraceResolution{
			Dir:              virtualWD,
			ExitCode:         0,
			Duration:         time.Since(start),
			ResolvedName:     resolved.name,
			ResolvedPath:     resolved.path,
			ResolutionSource: resolved.source,
		}))
		return result, nil
	}
	if code, ok := commands.ExitCode(err); ok {
		if message, ok := commands.DiagnosticMessage(err); ok && exec.Stderr != nil {
			_, _ = fmt.Fprintln(exec.Stderr, message)
		}
		recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(exec.Command, false, &commandTraceResolution{
			Dir:              virtualWD,
			ExitCode:         code,
			Duration:         time.Since(start),
			ResolvedName:     resolved.name,
			ResolvedPath:     resolved.path,
			ResolutionSource: resolved.source,
		}))
		return result, interp.ExitStatus(code)
	}
	recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(exec.Command, false, &commandTraceResolution{
		Dir:              virtualWD,
		ExitCode:         1,
		Duration:         time.Since(start),
		ResolvedName:     resolved.name,
		ResolvedPath:     resolved.path,
		ResolutionSource: resolved.source,
	}))
	return result, err
}

func (m *MVdan) runnerConfig(exec *Execution, budget *executionBudget) *interp.VirtualConfig {
	return &interp.VirtualConfig{
		Env:              expand.ListEnviron(envPairs(m.runnerEnv(exec))...),
		Dir:              exec.Dir,
		ExecHandler:      m.execHandler(exec, budget),
		OpenHandler:      m.openHandler(exec),
		ReadDirHandler2:  m.readDirHandler(exec),
		StatHandler:      m.statHandler(exec),
		RealpathHandler:  m.realpathHandler(exec),
		ProcSubstHandler: m.procSubstHandler(exec),
	}
}

func (m *MVdan) runnerOptions(exec *Execution, budget *executionBudget) []interp.RunnerOption {
	if exec == nil {
		exec = &Execution{}
	}
	options := []interp.RunnerOption{
		interp.StdIO(exec.Stdin, exec.Stdout, exec.Stderr),
		interp.CallHandler(m.callHandler(exec, budget)),
		interp.SyntheticPipelineSubshells(exec.pipelineSubshells),
	}
	if strings.TrimSpace(exec.ScriptPath) != "" {
		options = append(options, interp.TopLevelScriptPath(exec.ScriptPath))
	}
	if exec.Interactive {
		options = append(options, interp.Interactive(true))
	}
	return options
}

func (m *MVdan) runnerEnv(exec *Execution) map[string]string {
	if exec == nil {
		return nil
	}
	env := mergeEnv(nil, exec.Env)
	if exec.HasVisiblePWD && strings.TrimSpace(exec.VisiblePWD) != "" {
		env["PWD"] = exec.VisiblePWD
	}
	return env
}

func (m *MVdan) procSubstHandler(exec *Execution) interp.ProcSubstHandlerFunc {
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

func (m *MVdan) readDirHandler(exec *Execution) interp.ReadDirHandlerFunc2 {
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

func (m *MVdan) statHandler(exec *Execution) interp.StatHandlerFunc {
	return func(ctx context.Context, name string, followSymlinks bool) (stdfs.FileInfo, error) {
		state := handlerState(ctx, exec)
		abs := gbfs.Resolve(state.Dir, name)
		if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionStat, abs); err != nil {
			recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), abs, "", "")
			return nil, handlerPathError(ctx, state.Stderr, "stat", abs, err)
		}
		recordFile(exec.Trace, string(policy.FileActionStat), abs)
		if followSymlinks {
			return exec.FS.Stat(ctx, abs)
		}
		return exec.FS.Lstat(ctx, abs)
	}
}

func (m *MVdan) realpathHandler(exec *Execution) interp.RealpathHandlerFunc {
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

func (m *MVdan) callHandler(exec *Execution, budget *executionBudget) interp.CallHandlerFunc {
	return func(ctx context.Context, args []string) ([]string, error) {
		if len(args) == 0 {
			return args, nil
		}
		if isInternalHelperCommand(args[0]) {
			return args, nil
		}
		hc := interp.HandlerCtx(ctx)
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
			if _, ok := exec.Registry.Lookup(args[0]); ok {
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
	case "true", "false", "pwd", "cd", "dirs", "pushd", "popd", "type", "command", "source", ".":
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

func (m *MVdan) execHandler(exec *Execution, budget *executionBudget) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if len(args) == 0 {
			return nil
		}
		if args[0] == loopIterCommandName {
			return budget.beforeLoopIteration(ctx, args[1:])
		}

		hc := interp.HandlerCtx(ctx)
		virtualWD := hc.Dir
		currentEnv := envMap(hc.Env)
		internal := isInternalHelperCommand(args[0])
		fromBootstrap := hc.Internal
		resolved, ok, err := lookupCommand(ctx, exec, virtualWD, hc.Env, args[0])
		if err != nil {
			if policy.IsDenied(err) {
				recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), "", args[0], "")
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
				recordPolicyDenied(exec.Trace, err, "", resolved.path, resolved.name, resolved.source)
				return shellFailure(ctx, 126, "%v", err)
			}
			if !fromBootstrap {
				recordCommand(exec.Trace, trace.EventCommandStart, traceCommandInfo(args, false, &commandTraceResolution{
					Dir:              virtualWD,
					Position:         hc.Pos.String(),
					ResolvedName:     resolved.name,
					ResolvedPath:     resolved.path,
					ResolutionSource: resolved.source,
				}))
			}
		}

		shellVars := shellstate.NewShellVarAssignments()
		commandCtx := shellstate.WithShellVarAssignments(ctx, shellVars)
		finalEnv, err := invokeResolvedCommand(commandCtx, exec, resolved, args, currentEnv, virtualWD, hc.Stdin, hc.Stdout, hc.Stderr)
		if syncErr := syncShellVarAssignments(ctx, &hc, shellVars); syncErr != nil {
			return syncErr
		}
		if syncErr := syncCommandHistory(ctx, &hc, currentEnv, finalEnv); syncErr != nil {
			return syncErr
		}
		if syncErr := syncUmaskEnv(ctx, &hc, currentEnv, finalEnv); syncErr != nil {
			return syncErr
		}

		if err == nil {
			if !internal && !fromBootstrap {
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
			if !internal && !fromBootstrap {
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

		if !internal && !fromBootstrap {
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

func applyRunnerParams(runner *interp.Runner, startupOptions, args []string) error {
	params := runnerParamArgs(startupOptions, args)
	if len(params) == 0 {
		return nil
	}
	return interp.Params(params...)(runner)
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

func lookupCommand(ctx context.Context, exec *Execution, dir string, env expand.Environ, name string) (_ *resolvedCommand, ok bool, err error) {
	if isInternalHelperCommand(name) {
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
		recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), fullPath, commandName, source)
		return nil, false, err
	}
	info, err := exec.FS.Stat(ctx, fullPath)
	if err != nil || info.IsDir() {
		return nil, false, nil //nolint:nilerr // stat error means the file doesn't exist as a command
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

func shellSingleQuote(value string) string {
	return strings.ReplaceAll(value, `'`, `'"'"'`)
}

type executionBudget struct {
	maxCommandCount   int64
	maxLoopIterations int64
	count             atomic.Int64
	disabled          atomic.Int32
	mu                sync.Mutex
	loopCounts        map[string]int64
}

func newExecutionBudget(pol policy.Policy) *executionBudget {
	if pol == nil {
		return &executionBudget{}
	}

	return &executionBudget{
		maxCommandCount:   pol.Limits().MaxCommandCount,
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

func isInternalHelperCommand(name string) bool {
	_, ok := internalHelperCommands[name]
	return ok
}

const umaskEnvVar = "GBASH_UMASK"

func syncUmaskEnv(ctx context.Context, hc *interp.HandlerContext, before, after map[string]string) error {
	if hc == nil {
		return nil
	}
	beforeValue, beforeOK := before[umaskEnvVar]
	afterValue, afterOK := after[umaskEnvVar]
	if beforeOK == afterOK && beforeValue == afterValue {
		return nil
	}
	if !afterOK {
		return hc.Builtin(ctx, []string{"unset", umaskEnvVar})
	}
	return hc.Builtin(ctx, []string{"eval", fmt.Sprintf("%s='%s'; export %s", umaskEnvVar, shellSingleQuote(afterValue), umaskEnvVar)})
}

func syncShellVarAssignments(ctx context.Context, hc *interp.HandlerContext, assignments *shellstate.ShellVarAssignments) error {
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
			if err := hc.Builtin(ctx, []string{"unset", name}); err != nil {
				return err
			}
			continue
		}
		if err := hc.Builtin(ctx, []string{"eval", fmt.Sprintf("%s='%s'", name, shellSingleQuote(update.Value))}); err != nil {
			return err
		}
	}
	return nil
}

var _ Engine = (*MVdan)(nil)
