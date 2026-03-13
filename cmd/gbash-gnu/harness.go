package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ewhauser/gbash/internal/compatshims"
)

func prepareProgramDir(workDir, gbashBin string, programs []string) error {
	srcDir := filepath.Join(workDir, "src")
	for _, name := range programs {
		if err := os.RemoveAll(filepath.Join(srcDir, name)); err != nil {
			return err
		}
	}
	if err := compatshims.SymlinkCommands(srcDir, gbashBin, programs); err != nil {
		return err
	}
	return nil
}

func compatHelperShells() []string {
	return []string{"bash", "sh"}
}

func compatConfigShellPath(workDir string) (string, error) {
	path := filepath.Join(workDir, "src", "bash")
	if err := ensureExecutable(path); err != nil {
		return "", fmt.Errorf("prepare compat config shell: %w", err)
	}
	return path, nil
}

func disableCheckRebuild(workDir string) error {
	makefilePath := filepath.Join(workDir, "Makefile")
	data, err := os.ReadFile(makefilePath)
	if err != nil {
		return err
	}
	updated := strings.Replace(string(data), "check-am: all-am", "check-am:", 1)
	if updated == string(data) {
		return nil
	}
	return os.WriteFile(makefilePath, []byte(updated), 0o644)
}

func utilityShimNames(utilities []utilityManifest) []string {
	seen := make(map[string]struct{}, len(utilities)+3)
	out := make([]string, 0, len(utilities)+3)

	for _, name := range append(compatHelperShells(), "ginstall") {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, utility := range utilities {
		name := strings.TrimSpace(utility.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func shellSingleQuoteForScript(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
