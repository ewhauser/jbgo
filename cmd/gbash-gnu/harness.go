package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	gbcommands "github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/compatshims"
)

func listGNUPrograms(ctx context.Context, workDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(workDir, "build-aux", "gen-lists-of-programs.sh"), "--list-progs")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list GNU programs: %w", err)
	}
	lines := strings.Fields(string(out))
	sort.Strings(lines)
	return lines, nil
}

func prepareProgramDir(workDir, gbashBin string, programs []string, supported map[string]struct{}) error {
	srcDir := filepath.Join(workDir, "src")
	supportedNames := make([]string, 0)
	unsupportedNames := make([]string, 0)
	for _, name := range programs {
		if err := os.RemoveAll(filepath.Join(srcDir, name)); err != nil {
			return err
		}
		if _, ok := supported[name]; ok {
			supportedNames = append(supportedNames, name)
		} else {
			unsupportedNames = append(unsupportedNames, name)
		}
	}
	if err := compatshims.SymlinkCommands(srcDir, gbashBin, supportedNames); err != nil {
		return err
	}
	if err := compatshims.WriteUnsupportedStubs(srcDir, unsupportedNames); err != nil {
		return err
	}
	helperShells := compatHelperShells(supported)
	if err := compatshims.SymlinkCommands(srcDir, gbashBin, helperShells); err != nil {
		return err
	}
	if _, ok := supported["install"]; ok {
		if err := compatshims.SymlinkCommands(srcDir, gbashBin, []string{"ginstall"}); err != nil {
			return err
		}
	} else {
		if err := compatshims.WriteUnsupportedStubs(srcDir, []string{"ginstall"}); err != nil {
			return err
		}
	}
	return writeGNUProgramList(workDir, programs)
}

func compatHelperShells(supported map[string]struct{}) []string {
	names := make([]string, 0, 2)
	for _, name := range []string{"bash", "sh"} {
		if _, ok := supported[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

func compatConfigShellPath(workDir string) (string, error) {
	path := filepath.Join(workDir, "src", "bash")
	if err := ensureExecutable(path); err != nil {
		return "", fmt.Errorf("prepare compat config shell: %w", err)
	}
	return path, nil
}

func implementedGNUProgramSet() map[string]struct{} {
	supported := make(map[string]struct{})
	for _, name := range gbcommands.DefaultRegistry().Names() {
		supported[name] = struct{}{}
	}
	return supported
}

func writeGNUProgramList(workDir string, programs []string) error {
	hookDir := filepath.Join(workDir, "build-aux", "gbash-harness")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	return writeLines(filepath.Join(hookDir, "gnu-programs.txt"), programs)
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

func writeLines(path string, lines []string) error {
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	data := strings.Join(sorted, "\n")
	if data != "" {
		data += "\n"
	}
	return os.WriteFile(path, []byte(data), 0o644)
}

func shellSingleQuoteForScript(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
