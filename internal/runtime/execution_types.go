package runtime

import (
	"io"
	"maps"
	"time"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/trace"
)

type ExecutionRequest struct {
	Name            string
	Interpreter     string
	PassthroughArgs []string
	ScriptPath      string
	Script          string
	Command         []string
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

type ExecutionResult struct {
	ExitCode        int
	ShellExited     bool
	Stdout          string
	Stderr          string
	ControlStderr   string
	FinalEnv        map[string]string
	StartedAt       time.Time
	FinishedAt      time.Time
	Duration        time.Duration
	Events          []trace.Event
	StdoutTruncated bool
	StderrTruncated bool
}

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

type InteractiveResult struct {
	ExitCode int
}

func executionRequestFromCommand(req *commands.ExecutionRequest) *ExecutionRequest {
	if req == nil {
		return &ExecutionRequest{}
	}
	return &ExecutionRequest{
		Name:            req.Name,
		Interpreter:     req.Interpreter,
		PassthroughArgs: cloneStrings(req.PassthroughArgs),
		ScriptPath:      req.ScriptPath,
		Script:          req.Script,
		Command:         cloneStrings(req.Command),
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

func (result *ExecutionResult) commandResult() *commands.ExecutionResult {
	if result == nil {
		return nil
	}
	return &commands.ExecutionResult{
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

func interactiveRequestFromCommand(req *commands.InteractiveRequest) *InteractiveRequest {
	if req == nil {
		return &InteractiveRequest{}
	}
	return &InteractiveRequest{
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

func (result *InteractiveResult) commandResult() *commands.InteractiveResult {
	if result == nil {
		return nil
	}
	return &commands.InteractiveResult{ExitCode: result.ExitCode}
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	maps.Copy(out, src)
	return out
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
