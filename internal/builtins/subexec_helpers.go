package builtins

import (
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

type commandResolution struct {
	Name string
	Path string
}

func executeCommand(ctx context.Context, inv *Invocation, opts *executeCommandOptions) (*ExecutionResult, error) {
	if opts == nil {
		opts = &executeCommandOptions{}
	}
	if inv.Exec == nil {
		return nil, fmt.Errorf("subexec callback missing")
	}
	if len(opts.Argv) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	searchEnv := opts.SearchEnv
	if searchEnv == nil {
		searchEnv = inv.Env
	}
	env := opts.Env
	if env == nil {
		env = inv.Env
	}
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = inv.Cwd
	}

	resolved, ok, err := resolveCommand(ctx, inv, searchEnv, workDir, opts.Argv[0])
	if err != nil {
		return nil, err
	}
	if !ok {
		return &ExecutionResult{ExitCode: 127}, nil
	}

	argv := append([]string{resolved.Path}, opts.Argv[1:]...)
	return inv.Exec(ctx, &ExecutionRequest{
		Command:    argv,
		Env:        env,
		WorkDir:    workDir,
		ReplaceEnv: opts.ReplaceEnv,
		Stdin:      opts.Stdin,
		Stdout:     opts.Stdout,
		Stderr:     opts.Stderr,
		Timeout:    opts.Timeout,
	})
}

type executeCommandOptions struct {
	Argv       []string
	Env        map[string]string
	SearchEnv  map[string]string
	WorkDir    string
	ReplaceEnv bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Timeout    time.Duration
}

func resolveCommand(ctx context.Context, inv *Invocation, env map[string]string, dir, name string) (*commandResolution, bool, error) {
	if strings.Contains(name, "/") {
		info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, name)
		if err != nil {
			return nil, false, err
		}
		if !exists || info.IsDir() {
			return nil, false, nil
		}
		return &commandResolution{Name: path.Base(abs), Path: abs}, true, nil
	}

	for _, pathDir := range commandSearchDirs(env, dir) {
		candidate := gbfs.Resolve(pathDir, name)
		info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, candidate)
		if err != nil {
			return nil, false, err
		}
		if !exists || info.IsDir() {
			continue
		}
		return &commandResolution{Name: path.Base(abs), Path: abs}, true, nil
	}
	return nil, false, nil
}

func resolveAllCommands(ctx context.Context, inv *Invocation, env map[string]string, dir, name string) ([]string, error) {
	if strings.Contains(name, "/") {
		info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, name)
		if err != nil {
			return nil, err
		}
		if !exists || info.IsDir() {
			return nil, nil
		}
		return []string{abs}, nil
	}

	matches := make([]string, 0)
	seen := make(map[string]struct{})
	for _, pathDir := range commandSearchDirs(env, dir) {
		candidate := gbfs.Resolve(pathDir, name)
		info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, candidate)
		if err != nil {
			return nil, err
		}
		if !exists || info.IsDir() {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		matches = append(matches, abs)
	}
	return matches, nil
}

func commandSearchDirs(env map[string]string, dir string) []string {
	pathValue := strings.TrimSpace(env["PATH"])
	if pathValue == "" {
		return nil
	}
	dirs := make([]string, 0, strings.Count(pathValue, ":")+1)
	seen := make(map[string]struct{})
	for entry := range strings.SplitSeq(pathValue, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			entry = "."
		}
		resolved := gbfs.Resolve(dir, entry)
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		dirs = append(dirs, resolved)
	}
	return dirs
}

func shellJoinArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellSingleQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeExecutionOutputs(inv *Invocation, result *ExecutionResult) error {
	if result == nil {
		return nil
	}
	if result.Stdout != "" {
		if _, err := fmt.Fprint(inv.Stdout, result.Stdout); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if result.Stderr != "" {
		if _, err := fmt.Fprint(inv.Stderr, result.Stderr); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func exitForExecutionResult(result *ExecutionResult) error {
	if result == nil || result.ExitCode == 0 {
		return nil
	}
	return &ExitError{Code: result.ExitCode}
}

func sortedEnvPairs(env map[string]string) []string {
	pairs := make([]string, 0, len(env))
	for key, value := range env {
		pairs = append(pairs, key+"="+value)
	}
	slices.Sort(pairs)
	return pairs
}
