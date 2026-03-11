package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cadencerpm/just-bash-go/commands"
	jbfs "github.com/cadencerpm/just-bash-go/fs"
	"github.com/cadencerpm/just-bash-go/network"
	"github.com/cadencerpm/just-bash-go/policy"
	"github.com/cadencerpm/just-bash-go/trace"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type Engine interface {
	Parse(name, script string) (*syntax.File, error)
	Run(ctx context.Context, exec *Execution) (*RunResult, error)
}

type Execution struct {
	Name     string
	Script   string
	Program  *syntax.File
	Args     []string
	Env      map[string]string
	Dir      string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	FS       jbfs.FileSystem
	Network  network.Client
	Registry commands.CommandRegistry
	Policy   policy.Policy
	Trace    trace.Recorder
	Exec     func(context.Context, *commands.ExecutionRequest) (*commands.ExecutionResult, error)
}

type RunResult struct {
	FinalEnv    map[string]string
	ShellExited bool
}

type resolvedCommand struct {
	command commands.Command
	name    string
	path    string
	source  string
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

func (m *MVdan) Run(ctx context.Context, exec *Execution) (*RunResult, error) {
	if exec == nil {
		exec = &Execution{}
	}
	if exec.Dir == "" {
		exec.Dir = "/"
	}

	validationProgram := exec.Program
	if validationProgram == nil {
		parsed, err := m.Parse(exec.Name, exec.Script)
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

	preludeLines := uint(0)
	program := exec.Program
	if program == nil {
		parsed, err := m.Parse(exec.Name, withRuntimePrelude(exec.Dir, exec.Script))
		if err != nil {
			return nil, err
		}
		program = parsed
		preludeLines = runtimePreludeLineCount()
	}
	if err := instrumentLoopBudgets(program, exec.Policy); err != nil {
		return nil, err
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
	budget := newExecutionBudget(exec.Policy, preludeLines)

	options := []interp.RunnerOption{
		interp.Env(expand.ListEnviron(envPairs(exec.Env)...)),
		interp.Dir(hostRunnerDir),
		interp.StdIO(exec.Stdin, exec.Stdout, exec.Stderr),
		interp.OpenHandler(m.openHandler(exec)),
		interp.ReadDirHandler2(m.readDirHandler(exec)),
		interp.StatHandler(m.statHandler(exec)),
		interp.CallHandler(m.callHandler(exec, budget)),
		interp.ExecHandlers(func(_ interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return m.execHandler(exec, budget)
		}),
	}
	if len(exec.Args) > 0 {
		options = append(options, interp.Params(append([]string{"--"}, exec.Args...)...))
	}

	runner, err := interp.New(options...)
	if err != nil {
		return nil, err
	}
	runErr := runner.Run(ctx, program)
	return &RunResult{
		FinalEnv:    envMapFromVars(runner.Vars),
		ShellExited: runner.Exited(),
	}, runErr
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
		abs := jbfs.Resolve(virtualDir(state.Env, state.Dir), name)

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
		return file, nil
	}
}

func (m *MVdan) readDirHandler(exec *Execution) interp.ReadDirHandlerFunc2 {
	return func(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
		state := handlerState(ctx, exec)
		abs := jbfs.Resolve(virtualDir(state.Env, state.Dir), name)
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
		abs := jbfs.Resolve(virtualDir(state.Env, state.Dir), name)
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
				rewritten[0] = path.Join("/bin", args[0])
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
		if !ok {
			return shellFailure(ctx, 127, "%s: command not found", args[0])
		}

		if !internal {
			if err := allowCommand(ctx, exec.Policy, resolved.name, args); err != nil {
				recordPolicyDenied(exec.Trace, err, "", resolved.path, resolved.name, 126, resolved.source)
				return shellFailure(ctx, 126, "%v", err)
			}
		}

		start := time.Now().UTC()
		if !internal {
			recordCommand(exec.Trace, trace.EventCommandStart, traceCommandInfo(args, false, &commandTraceResolution{
				Dir:              virtualWD,
				Position:         hc.Pos.String(),
				ResolvedName:     resolved.name,
				ResolvedPath:     resolved.path,
				ResolutionSource: resolved.source,
			}))
		}

		err = resolved.command.Run(ctx, &commands.Invocation{
			Args:   args[1:],
			Env:    currentEnv,
			Dir:    virtualWD,
			Stdin:  hc.Stdin,
			Stdout: hc.Stdout,
			Stderr: hc.Stderr,
			FS:     exec.FS,
			Net:    exec.Network,
			Policy: exec.Policy,
			Trace:  exec.Trace,
			Exec:   subexecInvoker(exec.Exec, currentEnv, virtualWD),
		})

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

func normalizeSubexecRequest(req *commands.ExecutionRequest, currentEnv map[string]string, currentDir string) *commands.ExecutionRequest {
	if req == nil {
		req = &commands.ExecutionRequest{}
	}

	out := &commands.ExecutionRequest{
		Name:       req.Name,
		Script:     req.Script,
		Args:       append([]string(nil), req.Args...),
		Env:        mergeEnv(currentEnv, req.Env),
		WorkDir:    req.WorkDir,
		Timeout:    req.Timeout,
		ReplaceEnv: true,
		Stdin:      req.Stdin,
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
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
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

func allowPath(ctx context.Context, pol policy.Policy, fsys jbfs.FileSystem, action policy.FileAction, name string) error {
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
		fullPath := jbfs.Resolve(pathDir, name)
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
	fullPath := jbfs.Resolve(dir, name)
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
	if !ok {
		return nil, false, nil
	}
	return &resolvedCommand{
		command: cmd,
		name:    resolvedName,
		path:    fullPath,
		source:  source,
	}, true, nil
}

func pathDirs(env expand.Environ, dir string) []string {
	pathValue := strings.TrimSpace(env.Get("PATH").String())
	if pathValue == "" {
		return nil
	}

	dirs := make([]string, 0, strings.Count(pathValue, ":")+1)
	for _, entry := range strings.Split(pathValue, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		dirs = append(dirs, jbfs.Resolve(dir, entry))
	}
	return dirs
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
	if pwd := strings.TrimSpace(env.Get("PWD").String()); strings.HasPrefix(pwd, "/") {
		return jbfs.Clean(pwd)
	}
	return jbfs.Clean(dir)
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

func withRuntimePrelude(dir, script string) string {
	const preludeTemplate = `
PWD='%s'
OLDPWD=$PWD
export PWD OLDPWD

pwd() {
	case "$#" in
		0)
			printf '%%s\n' "$PWD"
			;;
		1)
			case "$1" in
				-L|-P)
					printf '%%s\n' "$PWD"
					;;
				*)
					printf 'pwd: invalid option: %%s\n' "$1" >&2
					return 2
					;;
			esac
			;;
		*)
			printf 'pwd: too many arguments\n' >&2
			return 1
			;;
	esac
}

cd() {
	old=$PWD
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
	next="$(__jb_cd_resolve "$PWD" "$target")" || return $?
	OLDPWD=$old
	PWD=$next
	export PWD OLDPWD
	if [ -n "$show" ]; then
		printf '%%s\n' "$PWD"
	fi
}
`

	return fmt.Sprintf(preludeTemplate, shellSingleQuote(dir)) + "\n" + script
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
	return uint(strings.Count(withRuntimePrelude("/", ""), "\n"))
}

func isInternalHelperCommand(name string) bool {
	return strings.HasPrefix(name, "__jb_")
}

var _ Engine = (*MVdan)(nil)
