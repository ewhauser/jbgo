package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"maps"
	"os"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

type FetchFunc func(context.Context, *network.Request) (*network.Response, error)

type InvocationOptions struct {
	Args                  []string
	Env                   map[string]string
	Cwd                   string
	Stdin                 io.Reader
	Stdout                io.Writer
	Stderr                io.Writer
	FileSystem            gbfs.FileSystem
	Network               network.Client
	Policy                policy.Policy
	Trace                 trace.Recorder
	Exec                  func(context.Context, *ExecutionRequest) (*ExecutionResult, error)
	Interact              func(context.Context, *InteractiveRequest) (*InteractiveResult, error)
	GetRegisteredCommands func() []string
}

type CommandFS struct {
	cwd    string
	fsys   gbfs.FileSystem
	pol    policy.Policy
	trace  trace.Recorder
	stderr io.Writer
}

func NewInvocation(opts *InvocationOptions) *Invocation {
	if opts == nil {
		opts = &InvocationOptions{}
	}
	getCommands := opts.GetRegisteredCommands
	if getCommands == nil {
		getCommands = func() []string { return nil }
	}

	inv := &Invocation{
		Args:                  append([]string(nil), opts.Args...),
		Env:                   cloneEnv(opts.Env),
		Cwd:                   gbfs.Resolve("/", opts.Cwd),
		Stdin:                 opts.Stdin,
		Stdout:                opts.Stdout,
		Stderr:                opts.Stderr,
		Exec:                  opts.Exec,
		Interact:              opts.Interact,
		GetRegisteredCommands: getCommands,
		trace:                 opts.Trace,
	}
	if opts.Policy != nil {
		inv.Limits = opts.Policy.Limits()
	}
	inv.FS = &CommandFS{
		cwd:    inv.Cwd,
		fsys:   opts.FileSystem,
		pol:    opts.Policy,
		trace:  opts.Trace,
		stderr: opts.Stderr,
	}
	if opts.Network != nil {
		inv.Fetch = func(ctx context.Context, req *network.Request) (*network.Response, error) {
			return opts.Network.Do(ctx, req)
		}
	}
	return inv
}

func (fs *CommandFS) Resolve(name string) string {
	if fs == nil {
		return gbfs.Clean(name)
	}
	return gbfs.Resolve(fs.cwd, name)
}

func (fs *CommandFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	abs, err := fs.prepare(ctx, policy.FileActionRead, name)
	if err != nil {
		return nil, err
	}
	file, err := fs.raw().Open(ctx, abs)
	if err != nil {
		return nil, wrapCommandError(err)
	}
	return file, nil
}

func (fs *CommandFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	abs := fs.Resolve(name)
	if canRead := flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY; canRead {
		if err := fs.check(ctx, policy.FileActionRead, abs); err != nil {
			return nil, err
		}
	}
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		if err := fs.check(ctx, policy.FileActionWrite, abs); err != nil {
			return nil, err
		}
	}
	file, err := fs.raw().OpenFile(ctx, abs, flag, perm)
	if err != nil {
		return nil, wrapCommandError(err)
	}
	return file, nil
}

func (fs *CommandFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs, err := fs.prepare(ctx, policy.FileActionStat, name)
	if err != nil {
		return nil, err
	}
	info, err := fs.raw().Stat(ctx, abs)
	if err != nil {
		return nil, wrapCommandError(err)
	}
	return info, nil
}

func (fs *CommandFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	abs, err := fs.prepare(ctx, policy.FileActionLstat, name)
	if err != nil {
		return nil, err
	}
	info, err := fs.raw().Lstat(ctx, abs)
	if err != nil {
		return nil, wrapCommandError(err)
	}
	return info, nil
}

func (fs *CommandFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	abs, err := fs.prepare(ctx, policy.FileActionReadDir, name)
	if err != nil {
		return nil, err
	}
	entries, err := fs.raw().ReadDir(ctx, abs)
	if err != nil {
		return nil, wrapCommandError(err)
	}
	return entries, nil
}

func (fs *CommandFS) Readlink(ctx context.Context, name string) (string, error) {
	abs, err := fs.prepare(ctx, policy.FileActionReadlink, name)
	if err != nil {
		return "", err
	}
	target, err := fs.raw().Readlink(ctx, abs)
	if err != nil {
		return "", wrapCommandError(err)
	}
	return target, nil
}

func (fs *CommandFS) Realpath(ctx context.Context, name string) (string, error) {
	abs, err := fs.prepare(ctx, policy.FileActionStat, name)
	if err != nil {
		return "", err
	}
	resolved, err := fs.raw().Realpath(ctx, abs)
	if err != nil {
		return "", wrapCommandError(err)
	}
	return resolved, nil
}

func (fs *CommandFS) Symlink(ctx context.Context, target, linkName string) error {
	linkAbs, err := fs.prepare(ctx, policy.FileActionWrite, linkName)
	if err != nil {
		return err
	}
	if err := fs.raw().Symlink(ctx, target, linkAbs); err != nil {
		return wrapCommandError(err)
	}
	recordFileMutation(fs.trace, "symlink", linkAbs, target, linkAbs)
	return nil
}

func (fs *CommandFS) Link(ctx context.Context, oldName, newName string) error {
	oldAbs, err := fs.prepare(ctx, policy.FileActionRead, oldName)
	if err != nil {
		return err
	}
	newAbs, err := fs.prepare(ctx, policy.FileActionWrite, newName)
	if err != nil {
		return err
	}
	if err := fs.raw().Link(ctx, oldAbs, newAbs); err != nil {
		return wrapCommandError(err)
	}
	recordFileMutation(fs.trace, "link", newAbs, oldAbs, newAbs)
	return nil
}

func (fs *CommandFS) Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error {
	abs, err := fs.prepare(ctx, policy.FileActionWrite, name)
	if err != nil {
		return err
	}
	if err := fs.raw().Chown(ctx, abs, uid, gid, follow); err != nil {
		return wrapCommandError(err)
	}
	recordFileMutation(fs.trace, "chown", abs, abs, abs)
	return nil
}

func (fs *CommandFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	abs, err := fs.prepare(ctx, policy.FileActionWrite, name)
	if err != nil {
		return err
	}
	if err := fs.raw().Chmod(ctx, abs, mode); err != nil {
		return wrapCommandError(err)
	}
	recordFileMutation(fs.trace, "chmod", abs, abs, abs)
	return nil
}

func (fs *CommandFS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	abs, err := fs.prepare(ctx, policy.FileActionWrite, name)
	if err != nil {
		return err
	}
	if err := fs.raw().Chtimes(ctx, abs, atime, mtime); err != nil {
		return wrapCommandError(err)
	}
	return nil
}

func (fs *CommandFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	abs, err := fs.prepare(ctx, policy.FileActionMkdir, name)
	if err != nil {
		return err
	}
	if err := fs.raw().MkdirAll(ctx, abs, perm); err != nil {
		return wrapCommandError(err)
	}
	recordFileMutation(fs.trace, "mkdir", abs, "", "")
	return nil
}

func (fs *CommandFS) Remove(ctx context.Context, name string, recursive bool) error {
	abs, err := fs.prepare(ctx, policy.FileActionRemove, name)
	if err != nil {
		return err
	}
	if err := fs.raw().Remove(ctx, abs, recursive); err != nil {
		return wrapCommandError(err)
	}
	recordFileMutation(fs.trace, "remove", abs, abs, "")
	return nil
}

func (fs *CommandFS) Rename(ctx context.Context, oldName, newName string) error {
	oldAbs, err := fs.prepare(ctx, policy.FileActionRename, oldName)
	if err != nil {
		return err
	}
	newAbs, err := fs.prepare(ctx, policy.FileActionRename, newName)
	if err != nil {
		return err
	}
	if err := fs.raw().Rename(ctx, oldAbs, newAbs); err != nil {
		return wrapCommandError(err)
	}
	recordFileMutation(fs.trace, "rename", newAbs, oldAbs, newAbs)
	return nil
}

func (fs *CommandFS) Getwd() string {
	if fs == nil {
		return "/"
	}
	return fs.cwd
}

func (fs *CommandFS) Chdir(name string) error {
	fs.cwd = fs.Resolve(name)
	return nil
}

func (fs *CommandFS) raw() gbfs.FileSystem {
	return fs.fsys
}

func (fs *CommandFS) prepare(ctx context.Context, action policy.FileAction, name string) (string, error) {
	abs := fs.Resolve(name)
	if err := fs.check(ctx, action, abs); err != nil {
		return "", err
	}
	return abs, nil
}

func (fs *CommandFS) check(ctx context.Context, action policy.FileAction, abs string) error {
	if fs == nil || fs.fsys == nil {
		return &ExitError{Code: 1, Err: errors.New("command filesystem not available")}
	}
	if err := policy.CheckPath(ctx, fs.pol, fs.fsys, action, abs); err != nil {
		recordPolicyDenied(fs.trace, err, action, abs, "", exitCodeForError(err))
		if fs.stderr != nil {
			_, _ = fmt.Fprintln(fs.stderr, err)
		}
		return &ExitError{Code: exitCodeForError(err), Err: err}
	}
	if fs.trace != nil {
		fs.trace.Record(&trace.Event{
			Kind: trace.EventFileAccess,
			File: &trace.FileEvent{
				Action: string(action),
				Path:   abs,
			},
		})
	}
	return nil
}

func wrapCommandError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return &ExitError{Code: 1, Err: err}
}

func cloneEnv(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}
