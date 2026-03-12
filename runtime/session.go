package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/shell"
	"github.com/ewhauser/gbash/trace"
)

func (s *Session) Exec(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	if isReentrantSessionExec(ctx, s) {
		return s.exec(ctx, req)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.exec(withSessionExecContext(ctx, s), req)
}

func (s *Session) exec(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	if req == nil {
		req = &ExecutionRequest{}
	}
	ctx, cancel := executionContext(ctx, req.Timeout)
	defer cancel()

	workDir := resolveWorkDir(s.cfg.FileSystem.WorkingDir, req.WorkDir)
	execEnv := executionEnv(s.cfg.BaseEnv, req)
	execEnv["PWD"] = workDir

	if err := initializeSandboxLayout(ctx, s.fs, execEnv, workDir, s.cfg.Registry.Names()); err != nil {
		return nil, err
	}
	if err := s.fs.Chdir(workDir); err != nil {
		return nil, err
	}

	limits := s.cfg.Policy.Limits()
	stdout := newCaptureBuffer(limits.MaxStdoutBytes)
	stderr := newCaptureBuffer(limits.MaxStderrBytes)
	executionID := nextTraceID("exec")
	recorder := trace.NewBuffer(
		trace.WithSessionID(s.id),
		trace.WithExecutionID(executionID),
	)

	started := time.Now().UTC()
	runResult, runErr := s.cfg.Engine.Run(ctx, &shell.Execution{
		Name:     defaultName(req.Name),
		Script:   req.Script,
		Args:     req.Args,
		Env:      execEnv,
		Dir:      workDir,
		Stdin:    stdinOrEmpty(req.Stdin),
		Stdout:   stdout,
		Stderr:   stderr,
		FS:       s.fs,
		Network:  s.cfg.NetworkClient,
		Registry: s.cfg.Registry,
		Policy:   s.cfg.Policy,
		Trace:    recorder,
		Exec:     s.subexecCallback,
	})
	finished := time.Now().UTC()

	result := &ExecutionResult{
		ExitCode:        shell.ExitCode(runErr),
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StartedAt:       started,
		FinishedAt:      finished,
		Duration:        finished.Sub(started),
		Events:          recorder.Snapshot(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	if runResult != nil {
		result.FinalEnv = runResult.FinalEnv
		result.ShellExited = runResult.ShellExited
	}

	if handled := classifyExecutionControlError(ctx, req.Timeout, runErr, stderr, result); handled {
		return result, nil
	}
	if runErr != nil && !shell.IsExitStatus(runErr) {
		return result, runErr
	}
	return result, nil
}

func (s *Session) subexecCallback(ctx context.Context, req *commands.ExecutionRequest) (*commands.ExecutionResult, error) {
	return s.exec(ctx, req)
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
		env := mergeEnv(nil, req.Env)
		for key, value := range defaultBaseEnv() {
			if _, ok := env[key]; !ok {
				env[key] = value
			}
		}
		return env
	}
	return mergeEnv(baseEnv, req.Env)
}

func executionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func classifyExecutionControlError(ctx context.Context, timeout time.Duration, runErr error, stderr *captureBuffer, result *ExecutionResult) bool {
	if result == nil || runErr == nil {
		return false
	}
	switch {
	case errors.Is(runErr, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		writeExecutionControlMessage(stderr, timeoutMessage(timeout))
		result.ExitCode = 124
		result.Stderr = stderr.String()
		result.StderrTruncated = stderr.Truncated()
		return true
	case errors.Is(runErr, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		writeExecutionControlMessage(stderr, "execution canceled")
		result.ExitCode = 130
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

func withSessionExecContext(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, sessionExecContextKey{}, session)
}

func isReentrantSessionExec(ctx context.Context, session *Session) bool {
	if ctx == nil {
		return false
	}
	current, ok := ctx.Value(sessionExecContextKey{}).(*Session)
	return ok && current == session
}
