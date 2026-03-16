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
	Script          string
	// Command runs an already-tokenized command argv without shell parsing.
	// Script and Command are mutually exclusive.
	Command        []string
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
	FinalEnv      map[string]string
	StartedAt     time.Time
	FinishedAt    time.Time
	Duration      time.Duration
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
