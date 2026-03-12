//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ewhauser/jbgo/commands"
	"github.com/ewhauser/jbgo/internal/compatrun"
	"github.com/ewhauser/jbgo/internal/compatshims"
)

func runCompatInvocation(ctx context.Context, argv0 string, inv compatInvocation, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	registry := commands.DefaultRegistry()
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
	dir, err = os.MkdirTemp("", "jbgo-compat-bin-*")
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
