package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func selectUtilities(mf *manifest, raw string) ([]utilityManifest, error) {
	selected := parseList(raw)
	if len(selected) == 0 {
		return append([]utilityManifest(nil), mf.Utilities...), nil
	}
	allowed := make(map[string]utilityManifest, len(mf.Utilities))
	for _, utility := range mf.Utilities {
		allowed[utility.Name] = utility
	}
	out := make([]utilityManifest, 0, len(selected))
	for _, name := range selected {
		utility, ok := allowed[name]
		if !ok {
			return nil, fmt.Errorf("unknown utility %q", name)
		}
		out = append(out, utility)
	}
	return out, nil
}

func resolveUtilityTests(workDir string, utility utilityManifest, globalSkips []skipPattern, explicitTests []string) (testsOut, skippedOut []string, err error) {
	if len(explicitTests) != 0 {
		filtered := make([]string, 0, len(explicitTests))
		for _, test := range explicitTests {
			if skip, _, err := shouldSkipTest(filepath.ToSlash(test), filepath.Join(workDir, test), globalSkips, utility.Skips); err != nil {
				return nil, nil, err
			} else if skip {
				continue
			} else {
				filtered = append(filtered, test)
			}
		}
		sort.Strings(filtered)
		return filtered, nil, nil
	}

	tests := make(map[string]struct{})
	skipped := make([]string, 0)
	for _, pattern := range utility.Patterns {
		matches, err := filepath.Glob(filepath.Join(workDir, pattern))
		if err != nil {
			return nil, nil, err
		}
		sort.Strings(matches)
		for _, match := range matches {
			rel, err := filepath.Rel(workDir, match)
			if err != nil {
				return nil, nil, err
			}
			rel = filepath.ToSlash(rel)
			if skip, reason, err := shouldSkipTest(rel, match, globalSkips, utility.Skips); err != nil {
				return nil, nil, err
			} else if skip {
				skipped = append(skipped, rel+": "+reason)
				continue
			}
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			if !isRunnableTestFile(rel, info) {
				continue
			}
			tests[rel] = struct{}{}
		}
	}
	out := make([]string, 0, len(tests))
	for test := range tests {
		out = append(out, test)
	}
	sort.Strings(out)
	sort.Strings(skipped)
	return out, skipped, nil
}

func isRunnableTestFile(rel string, info os.FileInfo) bool {
	switch filepath.Ext(rel) {
	case ".log", ".trs":
		return false
	case ".sh", ".pl", ".xpl":
		return true
	default:
		return info.Mode()&0o111 != 0
	}
}

func shouldSkipTest(rel, path string, globalSkips []skipPattern, utilitySkips []string) (skip bool, reason string, err error) {
	for _, skip := range globalSkips {
		if matched, err := filepath.Match(skip.Pattern, rel); err == nil && matched {
			return true, skip.Reason, nil
		}
	}
	for _, pattern := range utilitySkips {
		if matched, err := filepath.Match(pattern, rel); err == nil && matched {
			return true, "utility-specific skip", nil
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", err
	}
	contents := string(data)
	switch {
	case strings.Contains(contents, "require_controlling_input_terminal"):
		return true, "controlling TTY tests are skipped in v1", nil
	case strings.Contains(contents, "require_root_"):
		return true, "root-required tests are skipped in v1", nil
	case strings.Contains(contents, "require_selinux_"):
		return true, "SELinux tests are skipped in v1", nil
	case strings.Contains(rel, "help-version"):
		return true, "help/version tests are skipped in v1", nil
	default:
		return false, "", nil
	}
}

func runMakeCheck(ctx context.Context, makeBin, workDir, configShell string, tests []string, logPath string) (makeCheckResult, error) {
	args := []string{
		"check",
		"SUBDIRS=.",
		"VERBOSE=no",
		"RUN_EXPENSIVE_TESTS=yes",
		"RUN_VERY_EXPENSIVE_TESTS=yes",
		"srcdir=" + workDir,
		"TESTS=" + strings.Join(tests, " "),
	}
	cmd := exec.CommandContext(ctx, makeBin, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"CONFIG_SHELL="+configShell,
		"GBASH_COMPAT_RESOLVER_MODE=registry-then-host-fallback",
		"GBASH_COMPAT_RESERVED_COMMANDS_FILE="+filepath.Join(workDir, "build-aux", "gbash-harness", "gnu-programs.txt"),
	)
	output, err := cmd.CombinedOutput()
	if writeErr := os.WriteFile(logPath, output, 0o644); writeErr != nil {
		return makeCheckResult{}, writeErr
	}
	if err == nil {
		return makeCheckResult{Output: output}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return makeCheckResult{ExitCode: exitErr.ExitCode(), Output: output}, nil
	}
	return makeCheckResult{}, err
}
