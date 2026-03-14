package commands

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

type Command interface {
	Name() string
	Run(ctx context.Context, inv *Invocation) error
}

type CommandFunc func(ctx context.Context, inv *Invocation) error

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

	trace trace.Recorder
}

type definedCommand struct {
	name string
	fn   CommandFunc
}

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

func (inv *Invocation) TraceRecorder() trace.Recorder {
	if inv == nil {
		return nil
	}
	return inv.trace
}

type ExitError struct {
	Code int
	Err  error
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

func ExitCode(err error) (int, bool) {
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	return exitErr.Code, true
}

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
