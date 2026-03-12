package compatshims

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func PublicCommandNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, "__jb_") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func SymlinkCommands(dir, target string, names []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		if err := replaceWithSymlink(filepath.Join(dir, name), target); err != nil {
			return err
		}
	}
	return nil
}

func WriteUnsupportedStubs(dir string, names []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(stubScript(name)), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func replaceWithSymlink(path, target string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.Symlink(target, path)
}

func stubScript(name string) string {
	message := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(name + ": unsupported in gbash GNU compatibility harness")
	return fmt.Sprintf("#!/bin/sh\nprintf \"%s\\n\" >&2\nexit 127\n", message)
}
