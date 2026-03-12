//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/compatrun"
	"github.com/ewhauser/gbash/internal/compatshims"
	"github.com/ewhauser/gbash/shell"
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
	resolverMode, reservedCommands, hostExecutor, err := compatResolverConfig(env)
	if err != nil {
		return 1, err
	}
	runner, err := compatrun.New(&compatrun.Config{
		Registry:          registry,
		BaseEnv:           env,
		DefaultDir:        filepath.ToSlash(cwd),
		BuiltinCommandDir: commandDir,
		ResolverMode:      resolverMode,
		ReservedCommands:  reservedCommands,
		HostExecutor:      hostExecutor,
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

func compatResolverConfig(env map[string]string) (shell.ResolverMode, map[string]struct{}, shell.HostExecutor, error) {
	mode := shell.ResolverMode(strings.TrimSpace(env["GBASH_COMPAT_RESOLVER_MODE"]))
	if mode == "" {
		return shell.ResolverRegistryOnly, nil, nil, nil
	}
	if mode != shell.ResolverRegistryThenHostFallback {
		return "", nil, nil, fmt.Errorf("unsupported compat resolver mode %q", mode)
	}

	reservedCommands, err := readCompatReservedCommands(strings.TrimSpace(env["GBASH_COMPAT_RESERVED_COMMANDS_FILE"]))
	if err != nil {
		return "", nil, nil, err
	}
	return mode, reservedCommands, shell.NewOSHostExecutor(), nil
}

func readCompatReservedCommands(path string) (map[string]struct{}, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compat reserved commands: %w", err)
	}

	out := make(map[string]struct{})
	for line := range strings.SplitSeq(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out, nil
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
