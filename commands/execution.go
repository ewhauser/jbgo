package commands

import (
	"io"
	"time"

	"github.com/ewhauser/gbash/trace"
)

type ExecutionRequest struct {
	Name            string
	Interpreter     string
	PassthroughArgs []string
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
