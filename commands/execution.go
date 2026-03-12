package commands

import (
	"io"
	"time"

	"github.com/ewhauser/gbash/trace"
)

type ExecutionRequest struct {
	Name       string
	Script     string
	Args       []string
	Env        map[string]string
	WorkDir    string
	Timeout    time.Duration
	ReplaceEnv bool
	Stdin      io.Reader
}

type ExecutionResult struct {
	ExitCode        int
	ShellExited     bool
	Stdout          string
	Stderr          string
	FinalEnv        map[string]string
	StartedAt       time.Time
	FinishedAt      time.Time
	Duration        time.Duration
	Events          []trace.Event
	StdoutTruncated bool
	StderrTruncated bool
}
