package runtime

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"slices"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

const (
	defaultHomeDir = "/home/agent"
	defaultTempDir = "/tmp"
	defaultPath    = "/usr/bin:/bin"
	defaultUser    = "agent"
	defaultUID     = "1000"
	defaultGID     = "1000"
)

func defaultBaseEnv() map[string]string {
	return map[string]string{
		"HOME":        defaultHomeDir,
		"PATH":        defaultPath,
		"USER":        defaultUser,
		"LOGNAME":     defaultUser,
		"GROUP":       defaultUser,
		"GROUPS":      defaultGID,
		"UID":         defaultUID,
		"EUID":        defaultUID,
		"GID":         defaultGID,
		"EGID":        defaultGID,
		"SHELL":       "/bin/sh",
		"GBASH_ARCH":  defaultArchMachine(),
		"GBASH_UMASK": "0022",
	}
}

func initializeSandboxLayout(ctx context.Context, fsys gbfs.FileSystem, env map[string]string, workDir string, commands []string) error {
	for _, dir := range layoutDirectories(env, workDir) {
		if err := fsys.MkdirAll(ctx, dir, 0o755); err != nil {
			return err
		}
	}

	for _, dir := range commandDirectories(env) {
		for _, name := range publicCommandNames(commands) {
			if err := ensureCommandStub(ctx, fsys, dir, name); err != nil {
				return err
			}
		}
	}

	return nil
}

func layoutDirectories(env map[string]string, workDir string) []string {
	dirs := []string{
		defaultTempDir,
		workDir,
	}

	if home := strings.TrimSpace(env["HOME"]); home != "" {
		dirs = append(dirs, gbfs.Clean(home))
	}

	dirs = append(dirs, commandDirectories(env)...)
	return uniqueSortedPaths(dirs)
}

func commandDirectories(env map[string]string) []string {
	pathValue := strings.TrimSpace(env["PATH"])
	if pathValue == "" {
		return nil
	}

	dirs := make([]string, 0, strings.Count(pathValue, ":")+1)
	for dir := range strings.SplitSeq(pathValue, ":") {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dirs = append(dirs, gbfs.Clean(dir))
	}
	return uniqueSortedPaths(dirs)
}

func ensureCommandStub(ctx context.Context, fsys gbfs.FileSystem, dir, name string) error {
	fullPath := gbfs.Resolve(dir, name)

	_, err := fsys.Stat(ctx, fullPath)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, stdfs.ErrNotExist):
		return err
	}

	file, err := fsys.OpenFile(ctx, fullPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		if errors.Is(err, stdfs.ErrExist) {
			return nil
		}
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = io.WriteString(file, "# gbash virtual command stub: "+name+"\n")
	return err
}

func uniqueSortedPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, dir := range paths {
		dir = gbfs.Clean(dir)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	slices.Sort(out)
	return out
}

func publicCommandNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, "__jb_") {
			continue
		}
		out = append(out, name)
	}
	return out
}
