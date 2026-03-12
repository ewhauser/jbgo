package compatrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/compatfs"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/shell"
	"github.com/ewhauser/gbash/trace"
)

const (
	defaultMaxStdoutBytes = 64 << 20
	defaultMaxStderrBytes = 64 << 20
	defaultMaxFileBytes   = 256 << 20
)

type Config struct {
	FS                gbfs.FileSystem
	Registry          commands.CommandRegistry
	Policy            policy.Policy
	Engine            shell.Engine
	BaseEnv           map[string]string
	DefaultDir        string
	BuiltinCommandDir string
}

type Runner struct {
	cfg Config
	mu  sync.Mutex
}

func New(cfg *Config) (*Runner, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	resolved := *cfg
	if resolved.FS == nil {
		fsys, err := compatfs.New()
		if err != nil {
			return nil, err
		}
		resolved.FS = fsys
	}
	if resolved.Registry == nil {
		resolved.Registry = commands.DefaultRegistry()
	}
	if resolved.Engine == nil {
		resolved.Engine = shell.New()
	}
	if resolved.DefaultDir == "" {
		resolved.DefaultDir = resolved.FS.Getwd()
	}
	if resolved.Policy == nil {
		resolved.Policy = policy.NewStatic(&policy.Config{
			AllowedCommands: resolved.Registry.Names(),
			ReadRoots:       []string{"/"},
			WriteRoots:      []string{"/"},
			Limits: policy.Limits{
				MaxCommandCount:      10000,
				MaxGlobOperations:    100000,
				MaxLoopIterations:    10000,
				MaxSubstitutionDepth: 50,
				MaxStdoutBytes:       defaultMaxStdoutBytes,
				MaxStderrBytes:       defaultMaxStderrBytes,
				MaxFileBytes:         defaultMaxFileBytes,
			},
			NetworkMode: policy.NetworkDisabled,
			SymlinkMode: policy.SymlinkFollow,
		})
	}
	resolved.BaseEnv = cloneEnv(resolved.BaseEnv)
	return &Runner{cfg: resolved}, nil
}

func (r *Runner) Exec(ctx context.Context, req *commands.ExecutionRequest) (*commands.ExecutionResult, error) {
	return r.execWithOutputs(ctx, req, nil, nil)
}

func (r *Runner) execWithOutputs(ctx context.Context, req *commands.ExecutionRequest, liveStdout, liveStderr io.Writer) (*commands.ExecutionResult, error) {
	if isReentrantExec(ctx, r) {
		return r.exec(ctx, req, liveStdout, liveStderr)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exec(withExecContext(ctx, r), req, liveStdout, liveStderr)
}

func (r *Runner) RunUtility(ctx context.Context, name string, args []string, stdin io.Reader) (*commands.ExecutionResult, error) {
	return r.RunUtilityStreaming(ctx, name, args, stdin, nil, nil)
}

func (r *Runner) RunUtilityStreaming(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (*commands.ExecutionResult, error) {
	if err := validateUtilityName(name); err != nil {
		return nil, err
	}
	return r.execWithOutputs(ctx, &commands.ExecutionRequest{
		Name:       name,
		Script:     name + " \"$@\"\n",
		Args:       append([]string(nil), args...),
		Env:        cloneEnv(r.cfg.BaseEnv),
		ReplaceEnv: true,
		WorkDir:    r.cfg.DefaultDir,
		Stdin:      stdin,
	}, stdout, stderr)
}

func (r *Runner) exec(ctx context.Context, req *commands.ExecutionRequest, liveStdout, liveStderr io.Writer) (*commands.ExecutionResult, error) {
	if req == nil {
		req = &commands.ExecutionRequest{}
	}
	ctx, cancel := executionContext(ctx, req.Timeout)
	defer cancel()

	workDir := resolveWorkDir(r.cfg.DefaultDir, req.WorkDir)
	execEnv := executionEnv(r.cfg.BaseEnv, req)
	execEnv["PWD"] = workDir

	if err := r.cfg.FS.Chdir(workDir); err != nil {
		return nil, err
	}

	limits := r.cfg.Policy.Limits()
	stdout := newCaptureBuffer(limits.MaxStdoutBytes)
	stderr := newCaptureBuffer(limits.MaxStderrBytes)
	stdoutWriter := io.Writer(stdout)
	if liveStdout != nil {
		stdoutWriter = io.MultiWriter(stdout, liveStdout)
	}
	stderrWriter := io.Writer(stderr)
	if liveStderr != nil {
		stderrWriter = io.MultiWriter(stderr, liveStderr)
	}
	recorder := trace.NewBuffer()
	started := time.Now().UTC()
	runResult, runErr := r.cfg.Engine.Run(ctx, &shell.Execution{
		Name:              defaultName(req.Name),
		Script:            req.Script,
		Args:              req.Args,
		Env:               execEnv,
		Dir:               workDir,
		BuiltinCommandDir: r.cfg.BuiltinCommandDir,
		Stdin:             stdinOrEmpty(req.Stdin),
		Stdout:            stdoutWriter,
		Stderr:            stderrWriter,
		FS:                r.cfg.FS,
		Registry:          r.cfg.Registry,
		Policy:            r.cfg.Policy,
		Trace:             recorder,
		Exec:              r.subexecCallback,
	})
	finished := time.Now().UTC()

	result := &commands.ExecutionResult{
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
	if handled := classifyExecutionControlError(ctx, req.Timeout, runErr, stderr, liveStderr, result); handled {
		return result, nil
	}
	if runErr != nil && !shell.IsExitStatus(runErr) {
		return result, runErr
	}
	return result, nil
}

func (r *Runner) subexecCallback(ctx context.Context, req *commands.ExecutionRequest) (*commands.ExecutionResult, error) {
	return r.exec(ctx, req, nil, nil)
}

func validateUtilityName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("missing utility name")
	case strings.Contains(name, "/"):
		return fmt.Errorf("utility name must not contain '/'")
	case strings.ContainsAny(name, " \t\r\n"):
		return fmt.Errorf("utility name must not contain whitespace")
	default:
		return nil
	}
}

func defaultName(name string) string {
	if name == "" {
		return "stdin"
	}
	return name
}

func resolveWorkDir(defaultDir, workDir string) string {
	if workDir == "" {
		return defaultDir
	}
	return gbfs.Resolve(defaultDir, workDir)
}

func executionEnv(baseEnv map[string]string, req *commands.ExecutionRequest) map[string]string {
	if req == nil {
		return cloneEnv(baseEnv)
	}
	if req.ReplaceEnv {
		env := cloneEnv(req.Env)
		for _, key := range []string{"HOME", "UID", "EUID", "GID"} {
			if _, ok := env[key]; !ok {
				env[key] = ""
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

func classifyExecutionControlError(ctx context.Context, timeout time.Duration, runErr error, stderr *captureBuffer, liveStderr io.Writer, result *commands.ExecutionResult) bool {
	if result == nil || runErr == nil {
		return false
	}
	switch {
	case errors.Is(runErr, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		writeExecutionControlMessage(stderr, liveStderr, timeoutMessage(timeout))
		result.ExitCode = 124
		result.Stderr = stderr.String()
		result.StderrTruncated = stderr.Truncated()
		return true
	case errors.Is(runErr, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		writeExecutionControlMessage(stderr, liveStderr, "execution canceled")
		result.ExitCode = 130
		result.Stderr = stderr.String()
		result.StderrTruncated = stderr.Truncated()
		return true
	default:
		return false
	}
}

func writeExecutionControlMessage(stderr *captureBuffer, liveStderr io.Writer, message string) {
	if message == "" {
		return
	}
	if stderr != nil {
		_, _ = fmt.Fprintln(stderr, message)
	}
	if liveStderr != nil {
		_, _ = fmt.Fprintln(liveStderr, message)
	}
}

func timeoutMessage(timeout time.Duration) string {
	if timeout <= 0 {
		return "execution timed out"
	}
	return fmt.Sprintf("execution timed out after %s", timeout)
}

func stdinOrEmpty(reader io.Reader) io.Reader {
	if reader == nil {
		return strings.NewReader("")
	}
	return reader
}

func cloneEnv(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	maps.Copy(out, src)
	return out
}

func mergeEnv(base, override map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(override))
	maps.Copy(out, base)
	maps.Copy(out, override)
	return out
}

type captureBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func newCaptureBuffer(limit int64) *captureBuffer {
	return &captureBuffer{limit: limit}
}

func (b *captureBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	remaining := int(b.limit) - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) <= remaining {
		return b.buf.Write(p)
	}
	b.truncated = true
	_, _ = b.buf.Write(p[:remaining])
	return len(p), nil
}

func (b *captureBuffer) String() string {
	return b.buf.String()
}

func (b *captureBuffer) Truncated() bool {
	return b.truncated
}

type execContextKey struct{}

func withExecContext(ctx context.Context, runner *Runner) context.Context {
	return context.WithValue(ctx, execContextKey{}, runner)
}

func isReentrantExec(ctx context.Context, runner *Runner) bool {
	if ctx == nil {
		return false
	}
	current, ok := ctx.Value(execContextKey{}).(*Runner)
	return ok && current == runner
}
