package shell

import (
	"bytes"
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
	"github.com/ewhauser/gbash/host"
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
	HostPlatform      host.Platform
	HostProcessMeta   host.ExecutionMeta
	NewPipe           func() (io.ReadCloser, io.WriteCloser, error)
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
	command  commands.Command
	name     string
	path     string
	source   string
	args     []string
	hashPath string
}

const virtualCommandStubPrefix = "# gbash virtual command stub: "
const maxVirtualCommandStubBytes = 256

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
	ctx = shellstate.WithCompletionState(ctx, exec.CompletionState)
	budget := newExecutionBudget(exec.Policy)

	effectiveExec := *exec
	cleanupProcSubst := withProcSubstScope(&effectiveExec)
	defer cleanupProcSubst()

	runner, err := m.newRunner(&effectiveExec, budget)
	if err != nil {
		return nil, err
	}
	if family, ok := shellstate.SignalFamilyFromContext(ctx); ok {
		if owner, ok := family.Owner.(*interp.Runner); ok {
			runner.InheritSignalFamily(owner, family.StablePID, family.ParentBASHPID)
		}
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
		Env:        executionEnviron(exec, finalEnv),
		CurrentEnv: finalEnv,
		Stdin:      exec.Stdin,
		Stdout:     exec.Stdout,
		Stderr:     exec.Stderr,
	})
	return &RunResult{FinalEnv: finalEnv}, err
}

func (m *core) runnerConfig(exec *Execution, budget *executionBudget) *interp.RunnerConfig {
	cfg := &interp.RunnerConfig{
		Env:              executionEnviron(exec, m.runnerEnv(exec)),
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
	cfg.Platform = exec.HostPlatform
	cfg.PID = exec.HostProcessMeta.PID
	cfg.PPID = exec.HostProcessMeta.PPID
	cfg.NewPipe = exec.NewPipe
	cfg.LegacyBashCompat = exec.Interpreter == "bash" || exec.Interpreter == "sh"
	cfg.CommandString = executionUsesCommandString(exec)
	if exec.ScriptPath == "" && exec.Script != "" {
		cfg.CommandStringValue = exec.Script
	}
	return cfg
}

func executionEnviron(exec *Execution, env map[string]string) expand.Environ {
	caseInsensitive := exec != nil && exec.HostPlatform.EnvCaseInsensitive
	return expand.ListEnvironWithCase(caseInsensitive, envPairs(env)...)
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
		case arg == "--rcfile" || arg == "--option":
			i++
			continue
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
		"printf", "test", "[", "complete", "compgen", "compopt":
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
			next := builtinCommandTarget(current[1:])
			if len(next) == 0 || !interp.IsBuiltin(next[0]) {
				return invocations
			}
			current = append([]string(nil), next...)
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

func builtinCommandTarget(args []string) []string {
	rest := append([]string(nil), args...)
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	return rest
}

func commandBuiltinTarget(args []string) []string {
	show := false
	rest := append([]string(nil), args...)
	for len(rest) > 0 {
		switch rest[0] {
		case "-v", "-V":
			show = true
			rest = rest[1:]
		case "--":
			rest = rest[1:]
			if show || len(rest) == 0 {
				return nil
			}
			return rest
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
		shellVarLookup := shellstate.ShellVarLookup(func(name string) (string, bool) {
			vr := hc.Env.Get(name)
			if !vr.IsSet() {
				return "", false
			}
			return vr.String(), true
		})
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
				callCtx = shellstate.WithShellVarAssignments(callCtx, shellVars)
				callCtx = shellstate.WithShellVarLookup(callCtx, shellVarLookup)
				callCtx = shellstate.WithSignalDispatcher(callCtx, hc.DispatchSignal)
				if pgrp, ok := hc.ProcessGroup(); ok {
					callCtx = shellstate.WithProcessGroup(callCtx, pgrp)
				}
				return shellstate.WithSignalFamily(callCtx, hc.SignalFamily())
			},
			SyncEnv: func(callCtx context.Context, before, after map[string]string) error {
				if syncErr := syncShellVarAssignments(hc, shellVars); syncErr != nil { //nolint:contextcheck // runner state mutation is intentionally in-process and context-free
					return syncErr
				}
				if syncErr := syncCommandHistory(hc, before, after); syncErr != nil { //nolint:contextcheck // runner state mutation is intentionally in-process and context-free
					return syncErr
				}
				return syncUmaskEnv(hc, before, after) //nolint:contextcheck // runner state mutation is intentionally in-process and context-free
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
	for name, vr := range env.Each() {
		if !vr.IsSet() {
			delete(out, name)
			continue
		}
		if !vr.Exported || (vr.Kind != expand.String && vr.Kind != expand.NameRef) {
			delete(out, name)
			continue
		}
		value := vr.String()
		if vr.Kind == expand.NameRef {
			value = vr.Str
		}
		out[name] = value
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

	if hc, ok := optionalHandlerCtx(ctx); ok {
		if cachedPath, ok := hc.LookupCommandHash(name); ok {
			return lookupCachedCommand(ctx, exec, dir, name, cachedPath)
		}
	}

	for _, candidate := range pathCandidates(exec, env, name) {
		resolved, ok, err := lookupCommandPath(ctx, exec, dir, candidate.display, "path-search", name)
		if err != nil {
			return nil, false, err
		}
		if ok {
			resolved.hashPath = candidate.display
			return resolved, true, nil
		}
	}

	return nil, false, nil
}

func lookupCachedCommand(ctx context.Context, exec *Execution, dir, name, cachedPath string) (_ *resolvedCommand, ok bool, err error) {
	fullPath := gbfs.Resolve(dir, cachedPath)
	if exec != nil && exec.FS != nil {
		if err := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionStat, fullPath); err != nil {
			recordPolicyDenied(exec.Trace, err, string(policy.FileActionStat), fullPath, name, "path-cache")
			return nil, false, err
		}
		info, err := exec.FS.Stat(ctx, fullPath)
		if err != nil {
			return nil, false, shellFailureToWriter(ctx, handlerState(ctx, exec).Stderr, 127, "%s: %s", cachedPath, shellPathErrorText(err))
		}
		if info.IsDir() {
			return nil, false, shellFailureToWriter(ctx, handlerState(ctx, exec).Stderr, 126, "%s: Is a directory", cachedPath)
		}
	}

	resolved, ok, err := lookupCommandPath(ctx, exec, dir, cachedPath, "path-cache", name)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, shellFailureToWriter(ctx, handlerState(ctx, exec).Stderr, 126, "%s: Permission denied", cachedPath)
	}
	resolved.hashPath = cachedPath
	return resolved, true, nil
}

func lookupCommandPath(ctx context.Context, exec *Execution, dir, name, source, commandName string) (_ *resolvedCommand, ok bool, err error) {
	fullPath := gbfs.Resolve(dir, name)
	resolvedName := path.Base(fullPath)
	if exec == nil || exec.FS == nil {
		if dir := builtinCommandDir(exec); dir != "" && path.Dir(fullPath) == dir {
			if cmd, ok := lookupRegistryCommand(exec, resolvedName); ok {
				return &resolvedCommand{
					command: cmd,
					name:    resolvedName,
					path:    fullPath,
					source:  source,
				}, true, nil
			}
		}
		return nil, false, nil
	}
	// For explicit-path invocations (source == "path"), errors are surfaced as
	// shell exit-status failures rather than silently returning "not found".
	explicitPath := source == "path"
	if explicitPath && hasLongPathComponent(name) {
		// Check before stat: filesystems may normalize ENAMETOOLONG to ErrNotExist,
		// so we detect over-long components proactively. Use the original invocation
		// name (name) not the resolved fullPath so the message matches bash's output.
		return nil, false, shellFailureToWriter(ctx, handlerState(ctx, exec).Stderr, 126, "%s: File name too long", name)
	}
	if pathErr := allowPath(ctx, exec.Policy, exec.FS, policy.FileActionStat, fullPath); pathErr != nil {
		recordPolicyDenied(exec.Trace, pathErr, string(policy.FileActionStat), fullPath, commandName, source)
		if explicitPath && !policy.IsDenied(pathErr) {
			return nil, false, classifyExplicitPathError(ctx, exec, fullPath, pathErr)
		}
		return nil, false, pathErr
	}
	info, err := exec.FS.Stat(ctx, fullPath)
	if err != nil {
		if explicitPath {
			return nil, false, classifyExplicitPathError(ctx, exec, fullPath, err)
		}
		return nil, false, nil //nolint:nilerr // stat error means the file doesn't exist as a command
	}
	if info.IsDir() {
		return nil, false, nil
	}
	if dir := builtinCommandDir(exec); dir != "" && path.Dir(fullPath) == dir {
		if cmd, ok := lookupRegistryCommand(exec, resolvedName); ok {
			return &resolvedCommand{
				command: cmd,
				name:    resolvedName,
				path:    fullPath,
				source:  source,
			}, true, nil
		}
	}

	resolved, ok, err := resolveCommandFile(ctx, exec, fullPath, info.Mode(), commandName)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		if explicitPath {
			// File exists but lacks execute permission.
			return nil, false, shellFailureToWriter(ctx, handlerState(ctx, exec).Stderr, 126, "%s: Permission denied", fullPath)
		}
		return nil, false, nil
	}
	resolved.path = fullPath
	if resolved.source == "" {
		resolved.source = source
	}
	return resolved, true, nil
}

// classifyExplicitPathError maps a stat/access error for an explicit-path
// command invocation to the appropriate shell exit-status error, preserving
// the original errno rather than collapsing everything to ENOENT.
//
// Exit 126 (command found but not executable):
//   - ENAMETOOLONG → "File name too long"
//   - ELOOP        → "Too many levels of symbolic links"
//   - EACCES/EPERM → "Permission denied"
//
// Exit 127 (command not found):
//   - ENOENT, ENOTDIR, ErrNotExist, and any unrecognized error → "No such file or directory"
func classifyExplicitPathError(ctx context.Context, exec *Execution, fullPath string, err error) error {
	stderr := handlerState(ctx, exec).Stderr
	switch {
	case errors.Is(err, syscall.ENAMETOOLONG):
		return shellFailureToWriter(ctx, stderr, 126, "%s: File name too long", fullPath)
	case errors.Is(err, syscall.ELOOP):
		return shellFailureToWriter(ctx, stderr, 126, "%s: Too many levels of symbolic links", fullPath)
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return shellFailureToWriter(ctx, stderr, 126, "%s: Permission denied", fullPath)
	default:
		return shellFailureToWriter(ctx, stderr, 127, "%s: No such file or directory", fullPath)
	}
}

// hasLongPathComponent reports whether any component of p exceeds the POSIX
// NAME_MAX limit of 255 bytes.  Used to detect ENAMETOOLONG before stat,
// because some filesystem layers normalize that error to ErrNotExist.
func hasLongPathComponent(p string) bool {
	const nameMax = 255
	for component := range strings.SplitSeq(p, "/") {
		if len(component) > nameMax {
			return true
		}
	}
	return false
}

type pathCandidate struct {
	display string
}

func pathCandidates(exec *Execution, env expand.Environ, name string) []pathCandidate {
	pathValue := strings.TrimSpace(env.Get("PATH").String())
	if pathValue == "" {
		return nil
	}
	exts := commandPathExtensions(exec, env)

	candidates := make([]pathCandidate, 0, (strings.Count(pathValue, ":")+1)*max(1, len(exts)+1))
	for entry := range strings.SplitSeq(pathValue, ":") {
		entry = strings.TrimSpace(entry)
		base := "./" + name
		switch entry {
		case "", ".":
		default:
			base = path.Join(entry, name)
		}
		for _, candidate := range commandPathVariants(base, exts) {
			candidates = append(candidates, pathCandidate{display: candidate})
		}
	}
	return candidates
}

func commandPathExtensions(exec *Execution, env expand.Environ) []string {
	if exec == nil || len(exec.HostPlatform.PathExtensions) == 0 {
		return nil
	}
	if env == nil {
		return append([]string(nil), exec.HostPlatform.PathExtensions...)
	}
	pathext := strings.TrimSpace(env.Get("PATHEXT").String())
	if pathext == "" {
		return append([]string(nil), exec.HostPlatform.PathExtensions...)
	}
	exts := make([]string, 0, strings.Count(pathext, ";")+1)
	for ext := range strings.SplitSeq(strings.ToLower(pathext), ";") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if ext[0] != '.' {
			ext = "." + ext
		}
		exts = append(exts, ext)
	}
	return exts
}

func commandPathVariants(name string, exts []string) []string {
	if len(exts) == 0 || commandPathHasExt(name) {
		return []string{name}
	}
	variants := make([]string, 0, len(exts)+1)
	variants = append(variants, name)
	for _, ext := range exts {
		variants = append(variants, name+ext)
	}
	return variants
}

func commandPathHasExt(file string) bool {
	index := strings.LastIndex(file, ".")
	if index < 0 {
		return false
	}
	return strings.LastIndexAny(file, `:\/`) < index
}

type shebangResolution struct {
	resolved    *resolvedCommand
	interpreter string
}

func resolveShebangCommand(ctx context.Context, exec *Execution, fullPath, invokedPath string) (_ shebangResolution, ok bool, err error) {
	file, err := exec.FS.Open(ctx, fullPath)
	if err != nil {
		return shebangResolution{}, false, nil
	}
	defer func() {
		_ = file.Close()
	}()

	line, ok, err := readShebangLine(file)
	if err != nil || !ok {
		return shebangResolution{}, ok, err
	}
	interpreterPath, shebangInterpreter, argv, ok := parseShebangInterpreter(line)
	if !ok {
		return shebangResolution{}, true, nil
	}
	cmd, ok := lookupRegistryCommand(exec, shebangInterpreter)
	if !ok {
		return shebangResolution{interpreter: interpreterPath}, true, nil
	}
	scriptArg := fullPath
	if strings.Contains(invokedPath, "/") {
		scriptArg = invokedPath
	}
	return shebangResolution{
		resolved: &resolvedCommand{
			command: cmd,
			name:    shebangInterpreter,
			args:    append(argv, scriptArg),
		},
		interpreter: interpreterPath,
	}, true, nil
}

func resolveCommandFile(ctx context.Context, exec *Execution, fullPath string, mode stdfs.FileMode, invokedPath string) (_ *resolvedCommand, ok bool, err error) {
	if !isExecutableCommandFile(exec, mode) {
		return nil, false, nil
	}
	if resolved, ok, err := resolveVirtualCommandStub(ctx, exec, fullPath); ok || err != nil {
		return resolved, ok, err
	}
	if shebang, ok, err := resolveShebangCommand(ctx, exec, fullPath, invokedPath); ok || err != nil {
		if err != nil {
			return nil, false, err
		}
		if shebang.resolved != nil {
			shebang.resolved.source = "shebang"
			return shebang.resolved, true, nil
		}
		if shebang.interpreter != "" {
			return nil, false, shellFailureToWriter(ctx, handlerState(ctx, exec).Stderr, 126, "%s: %s: bad interpreter: No such file or directory", fullPath, shebang.interpreter)
		}
	}
	shellName := defaultScriptInterpreter(exec)
	cmd, ok := lookupRegistryCommand(exec, shellName)
	if !ok {
		return nil, false, nil
	}
	return &resolvedCommand{
		command: cmd,
		name:    shellName,
		args:    []string{fullPath},
		source:  "shell-script",
	}, true, nil
}

func resolveVirtualCommandStub(ctx context.Context, exec *Execution, fullPath string) (_ *resolvedCommand, ok bool, err error) {
	file, err := exec.FS.Open(ctx, fullPath)
	if err != nil {
		return nil, false, nil
	}
	defer func() {
		_ = file.Close()
	}()

	name, ok, err := readVirtualCommandStub(ctx, file)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	if path.Base(fullPath) != name {
		return nil, false, nil
	}
	cmd, ok := lookupRegistryCommand(exec, name)
	if !ok {
		return nil, false, nil
	}
	return &resolvedCommand{
		command: cmd,
		name:    name,
	}, true, nil
}

func readVirtualCommandStub(ctx context.Context, r io.Reader) (string, bool, error) {
	reader := commandutil.ReaderWithContext(ctx, r)
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(reader, maxVirtualCommandStubBytes+1))
	if err != nil {
		return "", false, err
	}
	if n > maxVirtualCommandStubBytes {
		return "", false, nil
	}
	name, ok := parseVirtualCommandStub(strings.TrimSpace(buf.String()))
	if !ok {
		return "", false, nil
	}
	return name, true, nil
}

func isExecutableCommandFile(exec *Execution, mode stdfs.FileMode) bool {
	if exec != nil && !exec.HostPlatform.RequireExecutableBit {
		return true
	}
	return mode&0o111 != 0
}

func defaultScriptInterpreter(exec *Execution) string {
	if exec != nil {
		switch path.Base(strings.TrimSpace(exec.Interpreter)) {
		case "bash", "sh":
			return path.Base(strings.TrimSpace(exec.Interpreter))
		}
	}
	return "bash"
}

func parseVirtualCommandStub(line string) (string, bool) {
	name, ok := strings.CutPrefix(line, virtualCommandStubPrefix)
	if !ok {
		return "", false
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/ \t\r\n") {
		return "", false
	}
	return name, true
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

func parseShebangInterpreter(line string) (interpreterPath, name string, args []string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", "", nil, false
	}
	interpreterPath = fields[0]
	name = path.Base(fields[0])
	if name == "" || name == "." || name == "/" {
		return "", "", nil, false
	}
	if name == "env" {
		name, args, ok = parseEnvShebangInterpreter(fields[1:])
		if !ok {
			return interpreterPath, "", nil, false
		}
		return interpreterPath, name, args, true
	}
	if len(fields) > 1 {
		args = append(args, fields[1:]...)
	}
	return interpreterPath, name, args, true
}

func parseEnvShebangInterpreter(fields []string) (name string, args []string, ok bool) {
	for len(fields) > 0 {
		switch field := fields[0]; {
		case field == "--":
			fields = fields[1:]
			goto done
		case field == "-i", field == "--ignore-environment", field == "-0", field == "--null", field == "-v", field == "--debug":
			fields = fields[1:]
			continue
		case field == "-u", field == "--unset", field == "-C", field == "--chdir":
			if len(fields) < 2 {
				return "", nil, false
			}
			fields = fields[2:]
			continue
		case strings.HasPrefix(field, "-u"), strings.HasPrefix(field, "-C"):
			if len(field) == 2 {
				return "", nil, false
			}
			fields = fields[1:]
			continue
		case strings.HasPrefix(field, "--unset="), strings.HasPrefix(field, "--chdir="):
			if !strings.Contains(field, "=") || strings.HasSuffix(field, "=") {
				return "", nil, false
			}
			fields = fields[1:]
			continue
		case field == "-S", field == "--split-string":
			fields = fields[1:]
			goto done
		case strings.HasPrefix(field, "-S"), strings.HasPrefix(field, "--split-string="):
			split, ok := envSplitStringFields(field)
			if !ok {
				return "", nil, false
			}
			fields = append(split, fields[1:]...)
			goto done
		}
		break
	}

done:
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

func envSplitStringFields(field string) ([]string, bool) {
	value := strings.TrimPrefix(field, "-S")
	if remainder, ok := strings.CutPrefix(field, "--split-string="); ok {
		value = remainder
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	return strings.Fields(value), true
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
		Env:    executionEnviron(exec, exec.Env),
		Dir:    exec.Dir,
		Stderr: exec.Stderr,
	}
}

func optionalHandlerCtx(ctx context.Context) (_ *interp.HandlerContext, ok bool) {
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
