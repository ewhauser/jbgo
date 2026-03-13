package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

type Command interface {
	Name() string
	Run(ctx context.Context, inv *Invocation) error
}

type CommandFunc func(ctx context.Context, inv *Invocation) error

type LookupCNAMEFunc func(context.Context, string) (string, error)

type ProcessAliveFunc func(context.Context, int) (bool, error)

type Invocation struct {
	Args                  []string
	Env                   map[string]string
	Cwd                   string
	Stdin                 io.Reader
	Stdout                io.Writer
	Stderr                io.Writer
	FS                    *CommandFS
	Fetch                 FetchFunc
	LookupCNAME           LookupCNAMEFunc
	ProcessAlive          ProcessAliveFunc
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

func stdinReader(inv *Invocation) io.Reader {
	if inv == nil || inv.Stdin == nil {
		return strings.NewReader("")
	}
	return inv.Stdin
}

func exitCodeForError(err error) int {
	if policy.IsDenied(err) {
		return 126
	}
	return 1
}

func allowPath(_ context.Context, inv *Invocation, _ policy.FileAction, name string) (string, error) {
	if inv == nil || inv.FS == nil {
		return gbfs.Clean(name), nil
	}
	return inv.FS.Resolve(name), nil
}

func openRead(ctx context.Context, inv *Invocation, name string) (gbfs.File, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionRead, name)
	if err != nil {
		return nil, "", err
	}
	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return file, abs, nil
}

func readDir(ctx context.Context, inv *Invocation, name string) ([]stdfs.DirEntry, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionReadDir, name)
	if err != nil {
		return nil, "", err
	}
	entries, err := inv.FS.ReadDir(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return entries, abs, nil
}

func statPath(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionStat, name)
	if err != nil {
		return nil, "", err
	}
	info, err := inv.FS.Stat(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return info, abs, nil
}

func lstatPath(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionLstat, name)
	if err != nil {
		return nil, "", err
	}
	info, err := inv.FS.Lstat(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return info, abs, nil
}

func statMaybe(ctx context.Context, inv *Invocation, action policy.FileAction, name string) (info stdfs.FileInfo, abs string, exists bool, err error) {
	abs, err = allowPath(ctx, inv, action, name)
	if err != nil {
		return nil, "", false, err
	}
	info, err = inv.FS.Stat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, abs, false, nil
		}
		return nil, "", false, err
	}
	return info, abs, true, nil
}

func lstatMaybe(ctx context.Context, inv *Invocation, action policy.FileAction, name string) (info stdfs.FileInfo, abs string, exists bool, err error) {
	abs, err = allowPath(ctx, inv, action, name)
	if err != nil {
		return nil, "", false, err
	}
	info, err = inv.FS.Lstat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, abs, false, nil
		}
		return nil, "", false, err
	}
	return info, abs, true, nil
}
