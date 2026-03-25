package commands

import (
	"io"
	"time"

	"github.com/ewhauser/gbash/trace"
)

// ExecutionRequest describes a non-interactive nested shell or command
// execution launched through [Invocation.Exec].
type ExecutionRequest struct {
	Name            string
	Interpreter     string
	PassthroughArgs []string
	ScriptPath      string
	Script          string
	// Command runs an already-tokenized command argv without shell parsing.
	// Script and Command are mutually exclusive.
	Command []string
	// CommandPath optionally overrides the executable looked up for Command[0]
	// while preserving Command[0] as the presented argv0.
	CommandPath string
	// CommandName optionally overrides the resolved command name used for
	// policy checks and tracing when CommandPath is set.
	CommandName    string
	Args           []string
	StartupOptions []string
	Env            map[string]string
	WorkDir        string
	Timeout        time.Duration
	ReplaceEnv     bool
	Interactive    bool
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

// ExecutionResult reports the outcome of an [ExecutionRequest].
type ExecutionResult struct {
	ExitCode      int
	ShellExited   bool
	Stdout        string
	Stderr        string
	ControlStderr string
	// CommandNotFound reports that nested command resolution failed before any
	// child process or builtin was invoked.
	CommandNotFound bool
	FinalEnv        map[string]string
	StartedAt       time.Time
	FinishedAt      time.Time
	Duration        time.Duration
	// Events contains structured execution events when tracing is enabled on the
	// parent runtime. It is empty by default.
	Events          []trace.Event
	StdoutTruncated bool
	StderrTruncated bool
}

// InteractiveRequest describes an interactive nested shell launched through
// [Invocation.Interact].
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

// InteractiveResult reports the outcome of an [InteractiveRequest].
type InteractiveResult struct {
	ExitCode int
}
