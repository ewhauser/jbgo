//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/compatrun"
	"github.com/ewhauser/gbash/internal/compatshims"
	"mvdan.cc/sh/v3/interp"
)

func runCompatInvocation(ctx context.Context, argv0 string, inv compatInvocation, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	registry := compatrun.DefaultRegistry()
	commandDir := inv.commandDir
	cleanup := func() {}
	if commandDir == "" {
		var err error
		commandDir, cleanup, err = makeCompatCommandDir(registry)
		if err != nil {
			return 1, err
		}
	}
	defer cleanup()

	cwd, err := os.Getwd()
	if err != nil {
		return 1, fmt.Errorf("getwd: %w", err)
	}

	env := environMap(os.Environ())
	env["PATH"] = prependCommandDir(commandDir, env["PATH"])
	runner, err := compatrun.New(&compatrun.Config{
		Registry:          registry,
		BaseEnv:           env,
		DefaultDir:        filepath.ToSlash(cwd),
		BuiltinCommandDir: commandDir,
		HostBash:          runHostBash,
		ProcessAlive:      hostProcessAlive,
	})
	if err != nil {
		return 1, err
	}

	result, err := runner.RunUtilityStreaming(ctx, inv.utility, inv.args, stdin, stdout, stderr)
	if err != nil {
		return 1, err
	}
	if result == nil {
		return 1, fmt.Errorf("compat runner returned no result")
	}
	return result.ExitCode, nil
}

func makeCompatCommandDir(registry commands.CommandRegistry) (dir string, cleanup func(), err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("resolve executable: %w", err)
	}
	dir, err = os.MkdirTemp("", "gbash-compat-bin-*")
	if err != nil {
		return "", nil, fmt.Errorf("create compat command dir: %w", err)
	}
	names := compatshims.PublicCommandNames(registry.Names())
	if err := compatshims.SymlinkCommands(dir, exe, names); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("populate compat command dir: %w", err)
	}
	return filepath.ToSlash(dir), func() { _ = os.RemoveAll(dir) }, nil
}

func environMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, pair := range env {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

func prependCommandDir(dir, current string) string {
	dir = strings.TrimSpace(dir)
	current = strings.TrimSpace(current)
	switch {
	case dir == "":
		return current
	case current == "":
		return dir
	case strings.HasPrefix(current, dir+":") || current == dir:
		return current
	default:
		return dir + ":" + current
	}
}

func hostProcessAlive(_ context.Context, pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM, nil
}

func runHostBash(ctx context.Context, req *commands.ExecutionRequest) error {
	shellPath, err := osexec.LookPath("bash")
	if err != nil {
		return err
	}
	if req == nil {
		req = &commands.ExecutionRequest{}
	}

	args := append([]string(nil), req.PassthroughArgs...)
	if len(args) == 0 {
		args = []string{"-s"}
	}

	cmd := osexec.CommandContext(ctx, shellPath, args...)
	cmd.Dir = req.WorkDir
	cmd.Env = sortedEnvPairs(req.Env)
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *osexec.ExitError
		if errors.As(err, &exitErr) {
			return interp.ExitStatus(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func sortedEnvPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
