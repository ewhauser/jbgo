package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type OSHostExecutor struct{}

func NewOSHostExecutor() *OSHostExecutor {
	return &OSHostExecutor{}
}

func (e *OSHostExecutor) Run(ctx context.Context, req *HostExecutionRequest) (*HostExecutionResult, error) {
	if req == nil {
		req = &HostExecutionRequest{}
	}
	resolvedPath, err := resolveHostExecutable(req.Path, req.Env, req.Dir)
	if err != nil {
		return nil, err
	}

	argv := req.Args
	if len(argv) == 0 {
		argv = []string{req.Path}
	}
	cmd := osexec.CommandContext(ctx, resolvedPath, argv[1:]...)
	cmd.Dir = req.Dir
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	cmd.Env = sortedEnvPairs(req.Env)

	err = cmd.Run()
	if err == nil {
		return &HostExecutionResult{ExitCode: 0, ResolvedPath: filepath.ToSlash(resolvedPath)}, nil
	}

	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) {
		return &HostExecutionResult{
			ExitCode:     exitErr.ExitCode(),
			ResolvedPath: filepath.ToSlash(resolvedPath),
		}, nil
	}
	return nil, fmt.Errorf("run host command %q: %w", req.Path, err)
}

func resolveHostExecutable(name string, env map[string]string, dir string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", ErrHostCommandNotFound
	}
	if strings.ContainsRune(trimmed, os.PathSeparator) {
		return resolveHostExplicitPath(trimmed, dir)
	}

	for _, entry := range filepath.SplitList(env["PATH"]) {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		candidate := entry
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(dir, candidate)
		}
		resolved, ok := hostExecutableAtPath(filepath.Join(candidate, trimmed))
		if ok {
			return resolved, nil
		}
	}
	return "", ErrHostCommandNotFound
}

func resolveHostExplicitPath(name, dir string) (string, error) {
	candidate := name
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dir, candidate)
	}
	resolved, ok := hostExecutableAtPath(candidate)
	if !ok {
		return "", ErrHostCommandNotFound
	}
	return resolved, nil
}

func hostExecutableAtPath(candidate string) (string, bool) {
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	if info.Mode()&0o111 == 0 {
		return "", false
	}
	return filepath.Clean(candidate), true
}

func sortedEnvPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+env[key])
	}
	return pairs
}
