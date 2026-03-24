package gbash

import (
	"io"
	"time"

	internalruntime "github.com/ewhauser/gbash/internal/runtime"
	"github.com/ewhauser/gbash/trace"
)

// ExecutionRequest describes a single shell execution.
//
// Callers usually populate [ExecutionRequest.Script] directly or set
// [ExecutionRequest.ScriptPath] to execute a script that already exists in the
// sandbox filesystem, then optionally provide stdin, environment overrides, or
// a working directory override.
type ExecutionRequest struct {
	Name            string
	Interpreter     string
	PassthroughArgs []string
	ScriptPath      string
	Script          string
	Args            []string
	StartupOptions  []string
	Env             map[string]string
	WorkDir         string
	Timeout         time.Duration
	ReplaceEnv      bool
	Interactive     bool
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
}

// ExecutionResult captures the output, exit status, timing, and optional trace
// events produced by a shell execution.
type ExecutionResult struct {
	ExitCode      int
	ShellExited   bool
	Stdout        string
	Stderr        string
	ControlStderr string
	FinalEnv      map[string]string
	StartedAt     time.Time
	FinishedAt    time.Time
	Duration      time.Duration
	// Events contains structured execution events when tracing is enabled on the
	// runtime. It is empty by default.
	Events          []trace.Event
	StdoutTruncated bool
	StderrTruncated bool
}

// InteractiveRequest describes an interactive shell session.
type InteractiveRequest struct {
	Name           string
	Args           []string
	StartupOptions []string
	Env            map[string]string
	WorkDir        string
	ReplaceEnv     bool
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

// InteractiveResult captures the final exit status from an interactive shell session.
type InteractiveResult struct {
	ExitCode int
}

func (req *ExecutionRequest) runtimeRequest() *internalruntime.ExecutionRequest {
	if req == nil {
		return &internalruntime.ExecutionRequest{}
	}
	return &internalruntime.ExecutionRequest{
		Name:            req.Name,
		Interpreter:     req.Interpreter,
		PassthroughArgs: cloneStrings(req.PassthroughArgs),
		ScriptPath:      req.ScriptPath,
		Script:          req.Script,
		Args:            cloneStrings(req.Args),
		StartupOptions:  cloneStrings(req.StartupOptions),
		Env:             copyStringMap(req.Env),
		WorkDir:         req.WorkDir,
		Timeout:         req.Timeout,
		ReplaceEnv:      req.ReplaceEnv,
		Interactive:     req.Interactive,
		Stdin:           req.Stdin,
		Stdout:          req.Stdout,
		Stderr:          req.Stderr,
	}
}

func executionResultFromRuntime(result *internalruntime.ExecutionResult) *ExecutionResult {
	if result == nil {
		return nil
	}
	return &ExecutionResult{
		ExitCode:        result.ExitCode,
		ShellExited:     result.ShellExited,
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		ControlStderr:   result.ControlStderr,
		FinalEnv:        copyStringMap(result.FinalEnv),
		StartedAt:       result.StartedAt,
		FinishedAt:      result.FinishedAt,
		Duration:        result.Duration,
		Events:          cloneTraceEvents(result.Events),
		StdoutTruncated: result.StdoutTruncated,
		StderrTruncated: result.StderrTruncated,
	}
}

func (req *InteractiveRequest) runtimeRequest() *internalruntime.InteractiveRequest {
	if req == nil {
		return &internalruntime.InteractiveRequest{}
	}
	return &internalruntime.InteractiveRequest{
		Name:           req.Name,
		Args:           cloneStrings(req.Args),
		StartupOptions: cloneStrings(req.StartupOptions),
		Env:            copyStringMap(req.Env),
		WorkDir:        req.WorkDir,
		ReplaceEnv:     req.ReplaceEnv,
		Stdin:          req.Stdin,
		Stdout:         req.Stdout,
		Stderr:         req.Stderr,
	}
}

func interactiveResultFromRuntime(result *internalruntime.InteractiveResult) *InteractiveResult {
	if result == nil {
		return nil
	}
	return &InteractiveResult{ExitCode: result.ExitCode}
}

func cloneStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	return append([]string(nil), src...)
}

func cloneTraceEvents(src []trace.Event) []trace.Event {
	if len(src) == 0 {
		return nil
	}
	out := make([]trace.Event, len(src))
	for i := range src {
		out[i] = cloneTraceEvent(&src[i])
	}
	return out
}

func cloneTraceEvent(event *trace.Event) trace.Event {
	if event == nil {
		return trace.Event{}
	}
	out := *event
	if event.Command != nil {
		command := *event.Command
		command.Argv = cloneStrings(command.Argv)
		out.Command = &command
	}
	if event.File != nil {
		file := *event.File
		out.File = &file
	}
	if event.Policy != nil {
		policy := *event.Policy
		out.Policy = &policy
	}
	return out
}
