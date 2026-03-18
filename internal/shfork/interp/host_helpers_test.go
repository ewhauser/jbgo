// Copyright (c) 2026, OpenAI
// See LICENSE for licensing information

package interp_test

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/internal/shfork/expand"
	"github.com/ewhauser/gbash/internal/shfork/interp"
)

const hostTestExecKillTimeout = 2 * time.Second

func hostTestRunnerOptions(dir string, killTimeout time.Duration, opts ...interp.RunnerOption) []interp.RunnerOption {
	if dir == "" {
		dir = hostTestWorkingDir()
	}
	all := []interp.RunnerOption{
		interp.Env(hostTestEnviron(dir)),
		interp.Dir(dir),
		interp.OpenHandler(hostTestOpenHandler),
		interp.ReadDirHandler2(hostTestReadDirHandler),
		interp.StatHandler(hostTestStatHandler),
		interp.RealpathHandler(hostTestRealpathHandler),
	}
	all = append(all, opts...)
	all = append(all, interp.ExecHandlers(hostTestExecMiddleware(killTimeout)))
	return all
}

func newHostTestRunner(tb testing.TB, opts ...interp.RunnerOption) *interp.Runner {
	tb.Helper()
	runner, err := interp.New(hostTestRunnerOptions("", hostTestExecKillTimeout, opts...)...)
	if err != nil {
		tb.Fatal(err)
	}
	return runner
}

func hostTestEnviron(dir string, extra ...string) expand.Environ {
	if dir == "" {
		dir = hostTestWorkingDir()
	}
	pairs := make([]string, 0, len(os.Environ())+7+len(extra))
	for _, pair := range os.Environ() {
		switch {
		case strings.HasPrefix(pair, "HOME="):
			continue
		case strings.HasPrefix(pair, "PWD="):
			continue
		case strings.HasPrefix(pair, "TMPDIR="):
			continue
		case strings.HasPrefix(pair, "UID="):
			continue
		case strings.HasPrefix(pair, "EUID="):
			continue
		case strings.HasPrefix(pair, "GID="):
			continue
		case strings.HasPrefix(pair, "EGID="):
			continue
		default:
			pairs = append(pairs, pair)
		}
	}
	pairs = append(pairs,
		"HOME="+dir,
		"PWD="+dir,
		"TMPDIR="+os.TempDir(),
	)
	if uid, gid, ok := hostTestUserIDs(); ok {
		pairs = append(pairs,
			"UID="+strconv.Itoa(uid),
			"EUID="+strconv.Itoa(uid),
			"GID="+strconv.Itoa(gid),
			"EGID="+strconv.Itoa(gid),
		)
	}
	pairs = append(pairs, extra...)
	return expand.ListEnviron(pairs...)
}

func hostTestUserIDs() (uid, gid int, ok bool) {
	current, err := user.Current()
	if err != nil {
		return 0, 0, false
	}
	uid, err = strconv.Atoi(current.Uid)
	if err != nil {
		return 0, 0, false
	}
	gid, err = strconv.Atoi(current.Gid)
	if err != nil {
		return 0, 0, false
	}
	return uid, gid, true
}

func hostTestWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}

func hostTestExecMiddleware(killTimeout time.Duration) func(interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		_ = next
		return func(ctx context.Context, args []string) error {
			hc := interp.HandlerCtx(ctx)
			path, err := hostTestLookPathDir(hc.Dir, hc.Env, args[0])
			if err != nil {
				fmt.Fprintln(hc.Stderr, err)
				return interp.ExitStatus(127)
			}
			cmd := exec.Cmd{
				Path:   path,
				Args:   args,
				Env:    hostTestExecEnv(hc.Env),
				Dir:    hostTestResolvePath(hc.Dir, "."),
				Stdin:  hc.Stdin,
				Stdout: hc.Stdout,
				Stderr: hc.Stderr,
			}
			err = cmd.Start()
			if err == nil {
				stopf := context.AfterFunc(ctx, func() {
					if killTimeout <= 0 || runtime.GOOS == "windows" {
						_ = cmd.Process.Signal(os.Kill)
						return
					}
					_ = cmd.Process.Signal(os.Interrupt)
					time.Sleep(killTimeout)
					_ = cmd.Process.Signal(os.Kill)
				})
				defer stopf()
				err = cmd.Wait()
			}
			switch err := err.(type) {
			case *exec.ExitError:
				if status, ok := err.Sys().(hostWaitStatus); ok && status.Signaled() {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return interp.ExitStatus(128 + status.Signal())
				}
				return interp.ExitStatus(err.ExitCode())
			case *exec.Error:
				fmt.Fprintln(hc.Stderr, err)
				return interp.ExitStatus(127)
			default:
				return err
			}
		}
	}
}

func hostTestExecEnv(env expand.Environ) []string {
	var pairs []string
	env.Each(func(name string, vr expand.Variable) bool {
		if vr.Exported && vr.Kind == expand.String && vr.IsSet() {
			pairs = append(pairs, name+"="+vr.String())
		}
		return true
	})
	return pairs
}

func hostTestLookPathDir(cwd string, env expand.Environ, file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("%q: executable file not found in $PATH", file)
	}
	exts := hostTestPathExts(env)
	if strings.ContainsAny(file, hostTestPathSeparators()) {
		return hostTestFindExecutable(cwd, file, exts)
	}
	pathVar := env.Get("PATH")
	pathList := filepath.SplitList(pathVar.String())
	if !pathVar.Declared() {
		pathList = filepath.SplitList(os.Getenv("PATH"))
	}
	if len(pathList) == 0 {
		pathList = []string{""}
	}
	for _, elem := range pathList {
		candidate := file
		switch elem {
		case "", ".":
			candidate = "." + string(filepath.Separator) + file
		default:
			candidate = filepath.Join(elem, file)
		}
		if found, err := hostTestFindExecutable(cwd, candidate, exts); err == nil {
			return found, nil
		}
	}
	return "", fmt.Errorf("%q: executable file not found in $PATH", file)
}

func hostTestFindExecutable(dir, file string, exts []string) (string, error) {
	if len(exts) == 0 {
		return hostTestCheckStat(dir, file, true)
	}
	if hostTestWinHasExt(file) {
		if resolved, err := hostTestCheckStat(dir, file, true); err == nil {
			return resolved, nil
		}
	}
	for _, ext := range exts {
		if resolved, err := hostTestCheckStat(dir, file+ext, true); err == nil {
			return resolved, nil
		}
	}
	return "", fs.ErrNotExist
}

func hostTestCheckStat(dir, file string, checkExec bool) (string, error) {
	resolved := hostTestResolvePath(dir, file)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("is a directory")
	}
	if checkExec && runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("permission denied")
	}
	return resolved, nil
}

func hostTestWinHasExt(file string) bool {
	i := strings.LastIndex(file, ".")
	if i < 0 {
		return false
	}
	return strings.LastIndexAny(file, `:\/`) < i
}

func hostTestPathExts(env expand.Environ) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	pathext := env.Get("PATHEXT").String()
	if pathext == "" {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	var exts []string
	for ext := range strings.SplitSeq(strings.ToLower(pathext), ";") {
		if ext == "" {
			continue
		}
		if ext[0] != '.' {
			ext = "." + ext
		}
		exts = append(exts, ext)
	}
	return exts
}

func hostTestPathSeparators() string {
	if runtime.GOOS == "windows" {
		return `:\/`
	}
	return `/`
}

func hostTestOpenHandler(ctx context.Context, name string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if runtime.GOOS == "windows" && name == "/dev/null" {
		name = os.DevNull
		flag &^= os.O_TRUNC
	} else {
		name = hostTestResolveHandlerPath(ctx, name)
	}
	return os.OpenFile(name, flag, perm)
}

func hostTestReadDirHandler(ctx context.Context, name string) ([]fs.DirEntry, error) {
	return os.ReadDir(hostTestResolveHandlerPath(ctx, name))
}

func hostTestStatHandler(ctx context.Context, name string, followSymlinks bool) (fs.FileInfo, error) {
	resolved := hostTestResolveHandlerPath(ctx, name)
	if followSymlinks {
		return os.Stat(resolved)
	}
	return os.Lstat(resolved)
}

func hostTestRealpathHandler(ctx context.Context, name string) (string, error) {
	resolved := hostTestResolveHandlerPath(ctx, name)
	return filepath.EvalSymlinks(resolved)
}

func hostTestResolveHandlerPath(ctx context.Context, name string) string {
	if hc, ok := hostTestHandlerCtx(ctx); ok {
		return hostTestResolvePath(hc.Dir, name)
	}
	return hostTestResolvePath(hostTestWorkingDir(), name)
}

func hostTestResolvePath(dir, name string) string {
	if runtime.GOOS == "windows" && name == "/dev/null" {
		return os.DevNull
	}
	if name == "" {
		return filepath.Clean(filepath.FromSlash(dir))
	}
	resolved := filepath.FromSlash(name)
	if !filepath.IsAbs(resolved) {
		base := filepath.Clean(filepath.FromSlash(dir))
		resolved = filepath.Join(base, resolved)
	}
	return filepath.Clean(resolved)
}

func hostTestHandlerCtx(ctx context.Context) (_ interp.HandlerContext, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return interp.HandlerCtx(ctx), true
}
