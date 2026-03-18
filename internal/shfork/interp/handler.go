// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/ewhauser/gbash/internal/shfork/expand"
	"github.com/ewhauser/gbash/internal/shfork/syntax"
)

// HandlerCtx returns the [HandlerContext] value stored in ctx,
// which is used when calling handler functions.
// It panics if ctx has no HandlerContext stored.
func HandlerCtx(ctx context.Context) HandlerContext {
	hc, ok := ctx.Value(handlerCtxKey{}).(HandlerContext)
	if !ok {
		panic("interp.HandlerCtx: no HandlerContext in ctx")
	}
	return hc
}

type handlerCtxKey struct{}

type handlerKind int

const (
	_                    handlerKind = iota
	handlerKindExec                  // [ExecHandlerFunc]
	handlerKindCall                  // [CallHandlerFunc]
	handlerKindOpen                  // [OpenHandlerFunc]
	handlerKindReadDir               // [ReadDirHandlerFunc2]
	handlerKindRealpath              // [RealpathHandlerFunc]
	handlerKindProcSubst             // [ProcSubstHandlerFunc]
)

// HandlerContext is the data passed to all the handler functions via [context.WithValue].
// It contains some of the current state of the [Runner].
type HandlerContext struct {
	runner *Runner // for internal use only, e.g. [HandlerContext.Builtin]

	// kind records which type of handler this context was built for.
	kind handlerKind

	// Env is a read-only version of the interpreter's environment,
	// including environment variables, global variables, and local function
	// variables.
	Env expand.Environ

	// Dir is the interpreter's current directory.
	Dir string

	// ExecFile is the file currently executing in the runner's active frame stack.
	// It is empty for inline code that is not executing from a file-backed context.
	ExecFile string

	// Internal reports whether this handler invocation is executing trusted
	// interpreter bootstrap code rather than user-supplied shell code.
	Internal bool

	// Pos is the source position which relates to the operation,
	// such as a [syntax.CallExpr] when calling an [ExecHandlerFunc].
	// It may be invalid if the operation has no relevant position information.
	Pos syntax.Pos

	// TODO(v4): use an os.File for stdin below directly.

	// Stdin is the interpreter's current standard input reader.
	// It is always an [*os.File], but the type here remains an [io.Reader]
	// due to backwards compatibility.
	Stdin io.Reader
	// Stdout is the interpreter's current standard output writer.
	Stdout io.Writer
	// Stderr is the interpreter's current standard error writer.
	Stderr io.Writer
}

// CallHandlerFunc is a handler which runs on every [syntax.CallExpr].
// It is called once variable assignments and field expansion have occurred.
// The context includes a [HandlerContext] value.
//
// The call's arguments are replaced by what the handler returns,
// and then the call is executed by the Runner as usual.
// The args slice is never empty.
// At this time, returning an empty slice without an error is not supported.
//
// This handler is similar to [ExecHandlerFunc], but has two major differences:
//
// First, it runs for all simple commands, including function calls and builtins.
//
// Second, it is not expected to execute the simple command, but instead to
// allow running custom code which allows replacing the argument list.
// Shell builtins touch on many internals of the Runner, after all.
//
// Returning a non-nil error will halt the [Runner] and will be returned via the API.
type CallHandlerFunc func(ctx context.Context, args []string) ([]string, error)

// TODO: consistently treat handler errors as non-fatal by default,
// but have an interface or API to specify fatal errors which should make
// the shell exit with a particular status code.

// ExecHandlerFunc is a handler which executes simple commands.
// It is called for all [syntax.CallExpr] nodes
// where the first argument is neither a declared function nor a builtin.
// The args slice is never empty.
// The context includes a [HandlerContext] value.
//
// Returning a nil error means a zero exit status.
// Other exit statuses can be set by returning or wrapping a [NewExitStatus] error,
// and such an error is returned via the API if it is the last statement executed.
// Any other error will halt the [Runner] and will be returned via the API.
type ExecHandlerFunc func(ctx context.Context, args []string) error

const defaultExecPath = "/usr/bin:/bin"

func closedExecHandler() ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hc := HandlerCtx(ctx)
		path, err := hc.runner.lookPath(ctx, hc.Dir, hc.Env, args[0], true, false)
		if err != nil {
			fmt.Fprintln(hc.Stderr, err)
			if isLookupNotFound(err, args[0]) {
				return ExitStatus(127)
			}
			return ExitStatus(126)
		}
		fmt.Fprintf(hc.Stderr, "%s: command execution unavailable\n", path)
		return ExitStatus(126)
	}
}

func winHasExt(file string) bool {
	i := strings.LastIndex(file, ".")
	if i < 0 {
		return false
	}
	return strings.LastIndexAny(file, `:\/`) < i
}

func pathExts(env expand.Environ) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	pathext := env.Get("PATHEXT").String()
	if pathext == "" {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	var exts []string
	for e := range strings.SplitSeq(strings.ToLower(pathext), `;`) {
		if e == "" {
			continue
		}
		if e[0] != '.' {
			e = "." + e
		}
		exts = append(exts, e)
	}
	return exts
}

func (r *Runner) lookPath(ctx context.Context, cwd string, env expand.Environ, file string, requireExec, useDefaultPath bool) (string, error) {
	if file == "" {
		return "", fmt.Errorf("%q: executable file not found in $PATH", file)
	}
	exts := pathExts(env)
	if strings.ContainsRune(file, '/') {
		return r.findPathCandidate(ctx, cwd, file, exts, requireExec)
	}
	pathValue := defaultExecPath
	if !useDefaultPath {
		pathValue = env.Get("PATH").String()
	}
	if pathValue == "" {
		return "", fmt.Errorf("%q: executable file not found in $PATH", file)
	}
	for _, elem := range strings.Split(pathValue, ":") {
		candidate := file
		switch elem {
		case "", ".":
			candidate = "./" + file
		default:
			candidate = path.Join(elem, file)
		}
		if found, err := r.findPathCandidate(ctx, cwd, candidate, exts, requireExec); err == nil {
			return found, nil
		}
	}
	return "", fmt.Errorf("%q: executable file not found in $PATH", file)
}

func (r *Runner) findPathCandidate(ctx context.Context, cwd, file string, exts []string, requireExec bool) (string, error) {
	base := file
	if !path.IsAbs(base) {
		base = path.Join(cwd, base)
	}
	base = path.Clean(base)
	tryExts := exts
	if len(tryExts) == 0 || winHasExt(base) {
		tryExts = []string{""}
	}
	for _, ext := range tryExts {
		candidate := base + ext
		info, err := r.statHandler(ctx, candidate, true)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return "", fmt.Errorf("is a directory")
		}
		if requireExec && runtime.GOOS != "windows" {
			if err := r.access(ctx, candidate, access_X_OK); err != nil {
				return "", fmt.Errorf("permission denied")
			}
		}
		return candidate, nil
	}
	return "", fs.ErrNotExist
}

func isLookupNotFound(err error, file string) bool {
	return err != nil && err.Error() == fmt.Sprintf("%q: executable file not found in $PATH", file)
}

// OpenHandlerFunc is a handler which opens files.
// It is called for all files that are opened directly by the shell,
// such as in redirects, except for named pipes created by process substitutions.
// The context includes a [HandlerContext] value.
// Files opened by executed programs are not included.
//
// The path parameter may be relative to the current directory,
// which can be fetched via [HandlerCtx].
//
// Use a return error of type [*os.PathError] to have the error printed to
// stderr and the exit status set to 1.
// Any other error will halt the [Runner] and will be returned via the API.
//
// Note that implementations which do not return [os.File] will cause
// extra files and goroutines for input redirections; see [StdIO].
type OpenHandlerFunc func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error)

// ProcSubstEndpoint describes the shell-visible path and I/O endpoints used to
// execute a process substitution.
//
// Path is the shell-visible path to expand the process substitution to.
// Reader is used as the subprocess stdin for `>(cmd)` substitutions.
// Writer is used as the subprocess stdout for `<(cmd)` substitutions.
// Cleanup is called after the subprocess I/O endpoints are closed.
type ProcSubstEndpoint struct {
	Path    string
	Reader  io.ReadCloser
	Writer  io.WriteCloser
	Cleanup func() error
}

// ProcSubstHandlerFunc provisions the shell-visible path and I/O endpoints used
// to execute a process substitution.
//
// The context includes a [HandlerContext] value.
type ProcSubstHandlerFunc func(ctx context.Context, ps *syntax.ProcSubst) (*ProcSubstEndpoint, error)

// TODO: paths passed to [OpenHandlerFunc] should be cleaned.

func closedOpenHandler() OpenHandlerFunc {
	return func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
}

// TODO(v4): if this is kept in v4, it most likely needs to use [io/fs.DirEntry] for efficiency

// ReadDirHandlerFunc is a handler which reads directories. It is called during
// shell globbing, if enabled.
//
// Deprecated: use [ReadDirHandlerFunc2], which uses [fs.DirEntry].
type ReadDirHandlerFunc func(ctx context.Context, path string) ([]fs.FileInfo, error)

// ReadDirHandlerFunc2 is a handler which reads directories. It is called during
// shell globbing, if enabled.
// The context includes a [HandlerContext] value.
type ReadDirHandlerFunc2 func(ctx context.Context, path string) ([]fs.DirEntry, error)

func closedReadDirHandler() ReadDirHandlerFunc2 {
	return func(ctx context.Context, path string) ([]fs.DirEntry, error) {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: fs.ErrNotExist}
	}
}

// StatHandlerFunc is a handler which gets a file's information.
// The context includes a [HandlerContext] value.
type StatHandlerFunc func(ctx context.Context, name string, followSymlinks bool) (fs.FileInfo, error)

func closedStatHandler() StatHandlerFunc {
	return func(ctx context.Context, path string, followSymlinks bool) (fs.FileInfo, error) {
		op := "stat"
		if !followSymlinks {
			op = "lstat"
		}
		return nil, &os.PathError{Op: op, Path: path, Err: fs.ErrNotExist}
	}
}

// RealpathHandlerFunc is a handler which resolves a physical path.
// The context includes a [HandlerContext] value.
type RealpathHandlerFunc func(ctx context.Context, name string) (string, error)

func defaultRealpathHandler() RealpathHandlerFunc {
	return func(ctx context.Context, name string) (string, error) {
		hc := HandlerCtx(ctx)
		return absPath(hc.Dir, name), nil
	}
}
