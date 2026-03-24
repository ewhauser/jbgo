package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/internal/shell"
	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/trace"
)

func (s *Session) Exec(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	if isReentrantSessionCall(ctx, s) {
		return s.exec(ctx, req)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.exec(withSessionCallContext(ctx, s), req)
}

func (s *Session) Interact(ctx context.Context, req *InteractiveRequest) (*InteractiveResult, error) {
	if isReentrantSessionCall(ctx, s) {
		return s.interact(ctx, req)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.interact(withSessionCallContext(ctx, s), req)
}

func (s *Session) exec(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	if req == nil {
		req = &ExecutionRequest{}
	}
	if err := validateExecutionRequest(req); err != nil {
		return nil, err
	}
	ctx, cancel := executionContext(ctx, req.Timeout)
	defer cancel()
	processMeta, err := s.cfg.Host.ExecutionMeta(ctx)
	if err != nil {
		return nil, err
	}
	ctx = withHostExecutionMeta(ctx, processMeta)

	workDir := resolveWorkDir(s.cfg.FileSystem.WorkingDir, req.WorkDir)
	execEnv := executionEnv(s.cfg.BaseEnv, req)
	projectPlatformEnv(execEnv, hostPlatform(s.cfg.Host))
	visiblePWD, hasVisiblePWD := execEnv["PWD"]
	execEnv["PWD"] = workDir
	if !s.bootAt.IsZero() {
		execEnv["GBASH_SESSION_BOOT_AT"] = s.bootAt.Format(time.RFC3339)
	}

	if err := s.layout.ensure(ctx, s.fs, execEnv, workDir, s.cfg.Registry.Names()); err != nil {
		return nil, err
	}
	if err := s.fs.Chdir(workDir); err != nil {
		return nil, err
	}
	if hasVisiblePWD && !runtimeVisiblePWDMatchesCurrentDir(ctx, s.fs, visiblePWD) {
		visiblePWD = ""
		hasVisiblePWD = false
	}

	limits := s.cfg.Policy.Limits()
	stdout := newCaptureBuffer(limits.MaxStdoutBytes)
	stderr := newCaptureBuffer(limits.MaxStderrBytes)
	stdoutWriter := newCapturePassthroughWriter(stdout, req.Stdout)
	stderrWriter := newCapturePassthroughWriter(stderr, req.Stderr)
	executionID := nextTraceID("exec")
	recorder, traceBuffer := newExecutionTraceRecorder(ctx, s.id, executionID, s.cfg.Tracing, true)
	if s.layout != nil {
		layoutRecorder := layoutMutationRecorder{layout: s.layout}
		if _, ok := recorder.(trace.NopRecorder); ok {
			recorder = layoutRecorder
		} else {
			recorder = trace.NewFanout(recorder, layoutRecorder)
		}
	}

	started := time.Now().UTC()
	baseLogEvent := LogEvent{
		SessionID:   s.id,
		ExecutionID: executionID,
		Name:        executionLogName(req),
		WorkDir:     workDir,
	}
	logExecutionEvent(ctx, s.cfg.Logger, &LogEvent{
		Kind:        LogExecStart,
		SessionID:   baseLogEvent.SessionID,
		ExecutionID: baseLogEvent.ExecutionID,
		Name:        baseLogEvent.Name,
		WorkDir:     baseLogEvent.WorkDir,
	})
	script, err := s.loadExecutionScript(ctx, req, workDir, recorder)
	if err != nil {
		finished := time.Now().UTC()
		var events []trace.Event
		if traceBuffer != nil {
			events = traceBuffer.Snapshot()
		}
		exitCode := 1
		if code, ok := commands.ExitCode(err); ok {
			exitCode = code
		}
		result := &ExecutionResult{
			ExitCode:        exitCode,
			Stdout:          stdout.String(),
			Stderr:          stderr.String(),
			StartedAt:       started,
			FinishedAt:      finished,
			Duration:        finished.Sub(started),
			Events:          events,
			StdoutTruncated: stdout.Truncated(),
			StderrTruncated: stderr.Truncated(),
		}
		logExecutionOutputs(ctx, s.cfg.Logger, &baseLogEvent, result)
		logExecutionCompletion(ctx, s.cfg.Logger, &baseLogEvent, result, err, false)
		return result, err
	}
	execReq := &shell.Execution{
		Name:            baseLogEvent.Name,
		Interpreter:     req.Interpreter,
		PassthroughArgs: cloneStrings(req.PassthroughArgs),
		ScriptPath:      req.ScriptPath,
		Script:          script,
		Command:         cloneStrings(req.Command),
		Args:            req.Args,
		StartupOptions:  req.StartupOptions,
		StartupHome:     req.StartupHome,
		Interactive:     req.Interactive,
		Env:             execEnv,
		Dir:             workDir,
		VisiblePWD:      visiblePWD,
		HasVisiblePWD:   hasVisiblePWD,
		HostPlatform:    hostPlatform(s.cfg.Host),
		HostProcessMeta: processMeta,
		NewPipe:         s.cfg.Host.NewPipe,
		Stdin:           stdinOrEmpty(req.Stdin),
		Stdout:          stdoutWriter,
		Stderr:          stderrWriter,
		FS:              s.fs,
		Network:         s.cfg.NetworkClient,
		Registry:        s.cfg.Registry,
		Policy:          s.cfg.Policy,
		Trace:           recorder,
		Now:             s.now,
		SetTime:         s.setTime,
		Exec:            s.subexecCallback,
		Interact:        s.interactCallback,
	}
	var (
		runResult *shell.RunResult
		runErr    error
	)
	if len(req.Command) > 0 {
		runResult, runErr = shell.RunCommand(ctx, execReq)
	} else {
		runResult, runErr = shell.Run(ctx, execReq)
	}
	finished := time.Now().UTC()

	var events []trace.Event
	if traceBuffer != nil {
		events = traceBuffer.Snapshot()
	}

	result := &ExecutionResult{
		ExitCode:        shell.ExitCode(runErr),
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StartedAt:       started,
		FinishedAt:      finished,
		Duration:        finished.Sub(started),
		Events:          events,
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	if runResult != nil {
		result.FinalEnv = runResult.FinalEnv
		result.ShellExited = runResult.ShellExited
	}

	handled := classifyExecutionControlError(ctx, req.Timeout, runErr, stderr, result)
	logExecutionOutputs(ctx, s.cfg.Logger, &baseLogEvent, result)
	unexpectedRunErr := runErr != nil && !handled && !shell.IsExitStatus(runErr)
	logExecutionCompletion(ctx, s.cfg.Logger, &baseLogEvent, result, runErr, unexpectedRunErr)

	if handled {
		return result, nil
	}
	if runErr != nil && !shell.IsExitStatus(runErr) {
		return result, runErr
	}
	return result, nil
}

func (s *Session) loadExecutionScript(ctx context.Context, req *ExecutionRequest, workDir string, recorder trace.Recorder) (string, error) {
	if req == nil || req.Script != "" || req.ScriptPath == "" {
		if req == nil {
			return "", nil
		}
		return req.Script, nil
	}

	inv := commands.NewInvocation(&commands.InvocationOptions{
		Cwd:        workDir,
		FileSystem: s.fs,
		Policy:     s.cfg.Policy,
		Trace:      recorder,
	})
	data, err := inv.FS.ReadFile(ctx, req.ScriptPath)
	if err != nil {
		return "", executionScriptLoadError(req.ScriptPath, err)
	}
	if err := builtins.ValidateShellScriptFileData(req.ScriptPath, data); err != nil {
		return "", &commands.ExitError{Code: 126, Err: err}
	}
	return string(data), nil
}

func (s *Session) now() time.Time {
	if s == nil {
		return time.Now().UTC()
	}
	s.clockMu.RLock()
	defer s.clockMu.RUnlock()
	if s.currentTime.IsZero() {
		return time.Now().UTC()
	}
	if s.clockRealAt.IsZero() {
		return s.currentTime
	}
	return s.currentTime.Add(time.Since(s.clockRealAt))
}

func (s *Session) setTime(when time.Time) error {
	if s == nil {
		return errors.New("session unavailable")
	}
	s.clockMu.Lock()
	s.currentTime = when.UTC()
	s.clockRealAt = time.Now()
	s.clockMu.Unlock()
	return nil
}

func (s *Session) interact(ctx context.Context, req *InteractiveRequest) (*InteractiveResult, error) {
	if req == nil {
		req = &InteractiveRequest{}
	}
	processMeta, err := s.cfg.Host.ExecutionMeta(ctx)
	if err != nil {
		return nil, err
	}
	ctx = withHostExecutionMeta(ctx, processMeta)

	workDir := resolveWorkDir(s.cfg.FileSystem.WorkingDir, req.WorkDir)
	execReq := &ExecutionRequest{
		Env:        req.Env,
		WorkDir:    req.WorkDir,
		ReplaceEnv: req.ReplaceEnv,
	}
	execEnv := executionEnv(s.cfg.BaseEnv, execReq)
	projectPlatformEnv(execEnv, hostPlatform(s.cfg.Host))
	visiblePWD, hasVisiblePWD := execEnv["PWD"]
	execEnv["PWD"] = workDir
	if _, ok := execEnv["TTY"]; !ok {
		execEnv["TTY"] = "/dev/tty"
	}
	if !s.bootAt.IsZero() {
		execEnv["GBASH_SESSION_BOOT_AT"] = s.bootAt.Format(time.RFC3339)
	}

	if err := initializeSandboxLayout(ctx, s.fs, execEnv, workDir, s.cfg.Registry.Names()); err != nil {
		return nil, err
	}
	if err := s.fs.Chdir(workDir); err != nil {
		return nil, err
	}
	if hasVisiblePWD && !runtimeVisiblePWDMatchesCurrentDir(ctx, s.fs, visiblePWD) {
		visiblePWD = ""
		hasVisiblePWD = false
	}

	executionID := nextTraceID("exec")
	recorder, _ := newExecutionTraceRecorder(ctx, s.id, executionID, s.cfg.Tracing, false)
	if s.layout != nil {
		layoutRecorder := layoutMutationRecorder{layout: s.layout}
		if _, ok := recorder.(trace.NopRecorder); ok {
			recorder = layoutRecorder
		} else {
			recorder = trace.NewFanout(recorder, layoutRecorder)
		}
	}
	result, err := shell.Interact(ctx, &shell.Execution{
		Name:            defaultName(req.Name),
		Args:            req.Args,
		StartupOptions:  req.StartupOptions,
		Interactive:     true,
		Env:             execEnv,
		Dir:             workDir,
		VisiblePWD:      visiblePWD,
		HasVisiblePWD:   hasVisiblePWD,
		HostPlatform:    hostPlatform(s.cfg.Host),
		HostProcessMeta: processMeta,
		NewPipe:         s.cfg.Host.NewPipe,
		Stdin:           stdinOrEmpty(req.Stdin),
		Stdout:          writerOrDiscard(req.Stdout),
		Stderr:          writerOrDiscard(req.Stderr),
		FS:              s.fs,
		Network:         s.cfg.NetworkClient,
		Registry:        s.cfg.Registry,
		Policy:          s.cfg.Policy,
		Trace:           recorder,
		Now:             s.now,
		SetTime:         s.setTime,
		Exec:            s.subexecCallback,
		Interact:        s.interactCallback,
	})
	if err != nil {
		return normalizeInteractiveResult(result), err
	}
	return normalizeInteractiveResult(result), nil
}

func (s *Session) subexecCallback(ctx context.Context, req *commands.ExecutionRequest) (*commands.ExecutionResult, error) {
	result, err := s.exec(ctx, executionRequestFromCommand(req))
	return result.commandResult(), err
}

func (s *Session) interactCallback(ctx context.Context, req *commands.InteractiveRequest) (*commands.InteractiveResult, error) {
	result, err := s.interact(ctx, interactiveRequestFromCommand(req))
	return result.commandResult(), err
}

func (s *Session) FileSystem() gbfs.FileSystem {
	return s.fs
}

func resolveWorkDir(defaultDir, workDir string) string {
	if workDir == "" {
		return defaultDir
	}
	return gbfs.Resolve(defaultDir, workDir)
}

func executionEnv(baseEnv map[string]string, req *ExecutionRequest) map[string]string {
	if req == nil {
		return mergeEnv(baseEnv, nil)
	}
	if req.ReplaceEnv {
		return mergeEnv(nil, req.Env)
	}
	return mergeEnv(baseEnv, req.Env)
}

func executionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func withHostExecutionMeta(ctx context.Context, meta host.ExecutionMeta) context.Context {
	return shellstate.WithProcessGroup(ctx, meta.ProcessGroup)
}

func validateExecutionRequest(req *ExecutionRequest) error {
	if req == nil {
		return nil
	}
	if req.Script != "" && len(req.Command) > 0 {
		return errors.New("execution request cannot set both Script and Command")
	}
	if req.ScriptPath != "" && len(req.Command) > 0 {
		return errors.New("execution request cannot set both ScriptPath and Command")
	}
	return nil
}

func executionScriptLoadError(scriptPath string, err error) error {
	if executionScriptLoadIsNotExist(err) {
		return &commands.ExitError{
			Code: 127,
			Err:  fmt.Errorf("%s: No such file or directory", scriptPath),
		}
	}

	code := 1
	if exitCode, ok := commands.ExitCode(err); ok {
		code = exitCode
	}
	return &commands.ExitError{
		Code: code,
		Err:  fmt.Errorf("%s: %s", scriptPath, executionScriptLoadErrorText(err)),
	}
}

func executionScriptLoadErrorText(err error) string {
	switch {
	case executionScriptLoadIsNotExist(err):
		return "No such file or directory"
	case executionScriptLoadIsDirectory(err):
		return "Is a directory"
	default:
		return err.Error()
	}
}

func executionScriptLoadIsNotExist(err error) bool {
	return err != nil &&
		(os.IsNotExist(err) ||
			errors.Is(err, stdfs.ErrNotExist) ||
			strings.Contains(strings.ToLower(err.Error()), "no such file or directory") ||
			strings.Contains(strings.ToLower(err.Error()), "file does not exist"))
}

func executionScriptLoadIsDirectory(err error) bool {
	if err == nil {
		return false
	}
	var pathErr *stdfs.PathError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, stdfs.ErrInvalid) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "is a directory")
}

func executionLogName(req *ExecutionRequest) string {
	if req == nil {
		return defaultName("")
	}
	switch {
	case strings.TrimSpace(req.Name) != "":
		return defaultName(req.Name)
	case strings.TrimSpace(req.ScriptPath) != "":
		return req.ScriptPath
	default:
		return defaultName("")
	}
}

func runtimeVisiblePWDMatchesCurrentDir(ctx context.Context, fsys gbfs.FileSystem, candidate string) bool {
	if fsys == nil || !path.IsAbs(candidate) {
		return false
	}
	for piece := range strings.SplitSeq(candidate, "/") {
		if piece == "." || piece == ".." {
			return false
		}
	}

	candidateInfo, candidateErr := fsys.Stat(ctx, candidate)
	currentInfo, currentErr := fsys.Stat(ctx, ".")
	if candidateErr == nil && currentErr == nil && os.SameFile(candidateInfo, currentInfo) {
		return true
	}

	candidateReal, candidateRealErr := fsys.Realpath(ctx, candidate)
	currentReal, currentRealErr := fsys.Realpath(ctx, ".")
	if candidateRealErr != nil || currentRealErr != nil {
		return false
	}
	return candidateReal == currentReal
}

func classifyExecutionControlError(ctx context.Context, timeout time.Duration, runErr error, stderr *captureBuffer, result *ExecutionResult) bool {
	if result == nil || runErr == nil {
		return false
	}
	switch {
	case errors.Is(runErr, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		message := timeoutMessage(timeout)
		writeExecutionControlMessage(stderr, message)
		result.ExitCode = 124
		result.ControlStderr = message
		result.Stderr = stderr.String()
		result.StderrTruncated = stderr.Truncated()
		return true
	case errors.Is(runErr, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		message := "execution canceled"
		writeExecutionControlMessage(stderr, message)
		result.ExitCode = 130
		result.ControlStderr = message
		result.Stderr = stderr.String()
		result.StderrTruncated = stderr.Truncated()
		return true
	default:
		return false
	}
}

func writeExecutionControlMessage(stderr *captureBuffer, message string) {
	if stderr == nil || message == "" {
		return
	}
	_, _ = fmt.Fprintln(stderr, message)
}

func timeoutMessage(timeout time.Duration) string {
	if timeout <= 0 {
		return "execution timed out"
	}
	return fmt.Sprintf("execution timed out after %s", timeout)
}

type sessionExecContextKey struct{}

func withSessionCallContext(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, sessionExecContextKey{}, session)
}

func isReentrantSessionCall(ctx context.Context, session *Session) bool {
	if ctx == nil {
		return false
	}
	current, ok := ctx.Value(sessionExecContextKey{}).(*Session)
	return ok && current == session
}

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func normalizeInteractiveResult(result *shell.InteractiveResult) *InteractiveResult {
	if result == nil {
		return &InteractiveResult{}
	}
	return &InteractiveResult{ExitCode: result.ExitCode}
}
