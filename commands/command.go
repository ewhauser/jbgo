package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

// Command is the runtime contract for a custom gbash command.
//
// Implementations should use the capabilities exposed by [Invocation] instead
// of reaching out to host-global APIs directly.
type Command interface {
	Name() string
	Run(ctx context.Context, inv *Invocation) error
}

// CommandFunc adapts a function into a [Command].
type CommandFunc func(ctx context.Context, inv *Invocation) error

// Invocation describes the capabilities available to a single command run.
//
// Commands should treat it as the boundary of what they are allowed to do:
// filesystem access goes through [Invocation.FS], network access through
// [Invocation.Fetch], nested execution through [Invocation.Exec], and whole-input
// reads through [ReadAll] or [ReadAllStdin].
type Invocation struct {
	Args                  []string
	Env                   map[string]string
	Cwd                   string
	Stdin                 io.Reader
	Stdout                io.Writer
	Stderr                io.Writer
	FS                    *CommandFS
	Fetch                 FetchFunc
	Exec                  func(context.Context, *ExecutionRequest) (*ExecutionResult, error)
	Interact              func(context.Context, *InteractiveRequest) (*InteractiveResult, error)
	Limits                policy.Limits
	GetRegisteredCommands func() []string

	trace   trace.Recorder
	now     func() time.Time
	setTime func(time.Time) error
}

type definedCommand struct {
	name string
	fn   CommandFunc
}

// DefineCommand wraps fn as a named [Command].
func DefineCommand(name string, fn CommandFunc) Command {
	return &definedCommand{name: name, fn: fn}
}

func (c *definedCommand) Name() string {
	return c.name
}

func (c *definedCommand) Run(ctx context.Context, inv *Invocation) error {
	if c.fn == nil {
		return nil
	}
	return c.fn(ctx, inv)
}

// TraceRecorder returns the runtime-owned trace recorder when tracing is enabled.
//
// Most command authors should not need this; prefer the higher-level
// capabilities on [Invocation] unless you are extending tracing itself.
func (inv *Invocation) TraceRecorder() trace.Recorder {
	if inv == nil {
		return nil
	}
	return inv.trace
}

// Now returns the wall clock visible to the current command invocation.
//
// When the runtime does not provide an explicit clock, this falls back to the
// process wall clock.
func (inv *Invocation) Now() time.Time {
	if inv != nil && inv.now != nil {
		return inv.now()
	}
	return time.Now()
}

// SetTime updates the wall clock visible to commands in the current runtime
// session.
//
// Runtimes that do not expose a mutable wall clock return an unsupported error.
func (inv *Invocation) SetTime(when time.Time) error {
	if inv != nil && inv.setTime != nil {
		return inv.setTime(when)
	}
	return fmt.Errorf("wall clock mutation is unavailable")
}

// ExitError reports a command exit status.
//
// Return one of these when a command needs to control its shell-visible exit
// code without panicking or mutating process-global state.
type ExitError struct {
	Code int
	Err  error
}

type stderrDiagnostic interface {
	error
	StderrDiagnostic() string
}

type diagnosticError struct {
	message string
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func (e *diagnosticError) Error() string {
	return e.message
}

func (e *diagnosticError) StderrDiagnostic() string {
	return e.message
}

// ExitCode extracts the exit status from err when err or one of its wrapped
// errors is an [ExitError].
func ExitCode(err error) (int, bool) {
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	return exitErr.Code, true
}

// Diagnosticf builds an error whose message is intended for stderr output.
//
// Wrap the returned error in an [ExitError] when you want the shell layer to
// surface the diagnostic while preserving an explicit exit status.
func Diagnosticf(format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	return &diagnosticError{message: message}
}

// DiagnosticMessage extracts a stderr-oriented diagnostic string from err.
func DiagnosticMessage(err error) (string, bool) {
	var diagnostic stderrDiagnostic
	if !errors.As(err, &diagnostic) {
		return "", false
	}
	message := strings.TrimSpace(diagnostic.StderrDiagnostic())
	return message, message != ""
}

// Exitf writes a formatted message to inv.Stderr and returns an [ExitError]
// carrying code.
func Exitf(inv *Invocation, code int, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if inv != nil && inv.Stderr != nil {
		_, _ = fmt.Fprintln(inv.Stderr, msg)
	}
	return &ExitError{
		Code: code,
		Err:  errors.New(msg),
	}
}

func exitf(inv *Invocation, code int, format string, args ...any) error {
	return Exitf(inv, code, format, args...)
}

func exitCodeForError(err error) int {
	if policy.IsDenied(err) {
		return 126
	}
	return 1
}
