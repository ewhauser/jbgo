package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"

	jbfs "github.com/cadencerpm/just-bash-go/fs"
	"github.com/cadencerpm/just-bash-go/network"
	"github.com/cadencerpm/just-bash-go/policy"
	"github.com/cadencerpm/just-bash-go/trace"
)

type Command interface {
	Name() string
	Run(ctx context.Context, inv *Invocation) error
}

type Invocation struct {
	Args   []string
	Env    map[string]string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	FS     jbfs.FileSystem
	Net    network.Client
	Policy policy.Policy
	Trace  trace.Recorder
	// Exec runs a nested shell execution inside the same sandbox session.
	// It inherits the current command's environment and working directory by default.
	// Commands should prefer direct filesystem access for data-plane operations and
	// reserve Exec for orchestration-style behavior such as subcommands or shell snippets.
	Exec func(context.Context, *ExecutionRequest) (*ExecutionResult, error)
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

func exitf(inv *Invocation, code int, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if inv.Stderr != nil {
		_, _ = fmt.Fprintln(inv.Stderr, msg)
	}
	return &ExitError{
		Code: code,
		Err:  errors.New(msg),
	}
}

func exitCodeForError(err error) int {
	if policy.IsDenied(err) {
		return 126
	}
	return 1
}

func allowPath(ctx context.Context, inv *Invocation, action policy.FileAction, name string) (string, error) {
	abs := jbfs.Resolve(inv.Dir, name)
	if inv.Policy != nil {
		if err := policy.CheckPath(ctx, inv.Policy, inv.FS, action, abs); err != nil {
			recordPolicyDenied(inv.Trace, err, action, abs, "", exitCodeForError(err))
			return "", exitf(inv, exitCodeForError(err), "%v", err)
		}
	}
	if inv.Trace != nil {
		inv.Trace.Record(&trace.Event{
			Kind: trace.EventFileAccess,
			File: &trace.FileEvent{
				Action: string(action),
				Path:   abs,
			},
		})
	}
	return abs, nil
}

func openRead(ctx context.Context, inv *Invocation, name string) (jbfs.File, string, error) {
	abs, err := allowPath(ctx, inv, policy.FileActionRead, name)
	if err != nil {
		return nil, "", err
	}
	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		return nil, "", &ExitError{Code: 1, Err: err}
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
		return nil, "", &ExitError{Code: 1, Err: err}
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
		return nil, "", &ExitError{Code: 1, Err: err}
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
		return nil, "", &ExitError{Code: 1, Err: err}
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
		return nil, "", false, &ExitError{Code: 1, Err: err}
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
		return nil, "", false, &ExitError{Code: 1, Err: err}
	}
	return info, abs, true, nil
}
