package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

func selectUtilities(programs []string, mf *manifest, raw string) ([]attributedUtility, error) {
	utilities := discoverAttributedUtilities(programs, mf)
	if strings.TrimSpace(raw) == "" {
		return utilities, nil
	}

	allowed := make(map[string]attributedUtility, len(utilities))
	for _, utility := range utilities {
		allowed[utility.Name] = utility
	}

	selected := parseList(raw)
	out := make([]attributedUtility, 0, len(selected))
	for _, name := range selected {
		utility, ok := allowed[name]
		if !ok {
			return nil, fmt.Errorf("unknown utility %q", name)
		}
		out = append(out, utility)
	}
	return out, nil
}

func discoverAttributedUtilities(programs []string, mf *manifest) []attributedUtility {
	aliases := manifestUtilityAliases(mf)
	overrideByName := make(map[string]utilityAttribution, len(mf.UtilityOverrides))
	for _, override := range mf.UtilityOverrides {
		overrideByName[override.Name] = override
	}

	utilities := make([]attributedUtility, 0, len(programs)+len(mf.UtilityOverrides))
	seen := make(map[string]struct{}, len(programs)+len(mf.UtilityOverrides))
	for _, program := range programs {
		name := utilityDisplayName(program, aliases)
		override, hasOverride := overrideByName[name]
		utility := attributedUtility{
			Name:     name,
			Patterns: []string{filepath.ToSlash(filepath.Join("tests", program, "*"))},
		}
		if hasOverride {
			utility.Patterns = append(utility.Patterns, override.Patterns...)
			utility.Skips = append(utility.Skips, override.Skips...)
		}
		utility.Patterns = uniqueSortedStrings(utility.Patterns)
		utilities = append(utilities, utility)
		seen[name] = struct{}{}
	}

	for _, override := range mf.UtilityOverrides {
		if _, ok := seen[override.Name]; ok {
			continue
		}
		utilities = append(utilities, attributedUtility{
			Name:     override.Name,
			Patterns: uniqueSortedStrings(append([]string(nil), override.Patterns...)),
			Skips:    uniqueSortedStrings(append([]string(nil), override.Skips...)),
		})
	}
	for _, rule := range mf.AttributionRules {
		for _, command := range rule.Commands {
			if _, ok := seen[command]; ok {
				continue
			}
			utilities = append(utilities, attributedUtility{Name: command})
			seen[command] = struct{}{}
		}
	}

	sort.Slice(utilities, func(i, j int) bool {
		return utilities[i].Name < utilities[j].Name
	})
	return utilities
}

func manifestUtilityAliases(mf *manifest) map[string]string {
	aliases := make(map[string]string, len(mf.UtilityDisplayNames))
	for _, alias := range mf.UtilityDisplayNames {
		aliases[alias.Name] = alias.Alias
	}
	return aliases
}

func utilityDisplayName(program string, aliases map[string]string) string {
	if alias, ok := aliases[program]; ok && strings.TrimSpace(alias) != "" {
		return alias
	}
	return program
}

func discoverRunnableTests(workDir string, globalSkips []skipPattern, explicitTests []string) (testsOut, skippedOut []string, err error) {
	authoritativeTests, err := discoverAuthoritativeTests(workDir)
	if err != nil {
		return nil, nil, err
	}

	if len(explicitTests) != 0 {
		available := make(map[string]struct{}, len(authoritativeTests))
		for _, test := range authoritativeTests {
			available[test] = struct{}{}
		}
		filtered := make([]string, 0, len(explicitTests))
		skipped := make([]string, 0)
		for _, test := range explicitTests {
			rel := filepath.ToSlash(test)
			if _, ok := available[rel]; !ok && !testScriptExists(workDir, rel) {
				return nil, nil, fmt.Errorf("unknown GNU test %q", rel)
			}
			if skip, reason, err := shouldSkipTest(rel, filepath.Join(workDir, test), globalSkips, nil); err != nil {
				return nil, nil, err
			} else if skip {
				skipped = append(skipped, rel+": "+reason)
			} else {
				filtered = append(filtered, rel)
			}
		}
		return uniqueSortedStrings(filtered), uniqueSortedStrings(skipped), nil
	}

	tests := make([]string, 0, len(authoritativeTests))
	skipped := make([]string, 0)
	for _, rel := range authoritativeTests {
		path := filepath.Join(workDir, filepath.FromSlash(rel))
		if skip, reason, err := shouldSkipTest(rel, path, globalSkips, nil); err != nil {
			return nil, nil, err
		} else if skip {
			skipped = append(skipped, rel+": "+reason)
		} else {
			tests = append(tests, rel)
		}
	}
	return uniqueSortedStrings(tests), uniqueSortedStrings(skipped), nil
}

func resolveUtilityRuns(workDir string, utilities []attributedUtility, globalSkips []skipPattern, explicitTests []string) ([]utilityRun, []string, error) {
	if len(explicitTests) != 0 {
		tests, skipped, err := discoverRunnableTests(workDir, globalSkips, explicitTests)
		if err != nil {
			return nil, nil, err
		}
		return []utilityRun{{
			Utility: attributedUtility{Name: "explicit-tests"},
			Tests:   tests,
			Skipped: skipped,
		}}, tests, nil
	}

	allTests, _, err := discoverRunnableTests(workDir, globalSkips, nil)
	if err != nil {
		return nil, nil, err
	}

	runs := make([]utilityRun, 0, len(utilities))
	for _, utility := range utilities {
		tests, skipped := attributeUtilityTests(allTests, utility)
		runs = append(runs, utilityRun{
			Utility: utility,
			Tests:   tests,
			Skipped: skipped,
		})
	}
	return runs, allTests, nil
}

func attributeUtilityTests(allTests []string, utility attributedUtility) (tests, skipped []string) {
	tests = make([]string, 0)
	skipped = make([]string, 0)
	for _, test := range allTests {
		if !utilityMatchesTestPath(utility, test) {
			continue
		}
		if skip, reason := shouldSkipUtilityTest(test, utility.Skips); skip {
			skipped = append(skipped, test+": "+reason)
			continue
		}
		tests = append(tests, test)
	}
	return uniqueSortedStrings(tests), uniqueSortedStrings(skipped)
}

func shouldSkipUtilityTest(rel string, utilitySkips []string) (skip bool, reason string) {
	for _, pattern := range utilitySkips {
		if matched, err := filepath.Match(pattern, rel); err == nil && matched {
			return true, "utility-specific skip"
		}
	}
	return false, ""
}

var makeVarRefPattern = regexp.MustCompile(`\$\(([^)]+)\)`)

func discoverAuthoritativeTests(workDir string) ([]string, error) {
	localMKPath := filepath.Join(workDir, "tests", "local.mk")
	data, err := os.ReadFile(localMKPath)
	if err != nil {
		return nil, err
	}
	vars := parseMakeVariables(string(data))
	expanded, err := expandMakeVariable("TESTS", vars, nil)
	if err != nil {
		return nil, fmt.Errorf("expand TESTS from %s: %w", localMKPath, err)
	}

	tests := make([]string, 0)
	for field := range strings.FieldsSeq(expanded) {
		field = filepath.ToSlash(strings.TrimSpace(field))
		if !strings.HasPrefix(field, "tests/") {
			continue
		}
		tests = append(tests, field)
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("no authoritative tests found in %s", localMKPath)
	}
	return uniqueSortedStrings(tests), nil
}

func parseMakeVariables(contents string) map[string]string {
	vars := make(map[string]string)
	var logical strings.Builder

	flush := func() {
		line := strings.TrimSpace(logical.String())
		logical.Reset()
		if line == "" || strings.HasPrefix(line, "#") {
			return
		}

		var (
			name        string
			value       string
			appendValue bool
		)
		switch {
		case strings.Contains(line, "+="):
			parts := strings.SplitN(line, "+=", 2)
			name = strings.TrimSpace(parts[0])
			value = strings.TrimSpace(parts[1])
			appendValue = true
		case strings.Contains(line, "="):
			parts := strings.SplitN(line, "=", 2)
			name = strings.TrimSpace(parts[0])
			value = strings.TrimSpace(parts[1])
		default:
			return
		}
		if name == "" || strings.ContainsAny(name, " \t:") {
			return
		}
		if appendValue && vars[name] != "" && value != "" {
			vars[name] = vars[name] + " " + value
			return
		}
		if appendValue {
			vars[name] += value
			return
		}
		vars[name] = value
	}

	for rawLine := range strings.SplitSeq(contents, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if logical.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		continued := strings.HasSuffix(strings.TrimRight(line, " \t"), "\\")
		if continued {
			line = strings.TrimRight(strings.TrimRight(line, " \t"), "\\")
		}
		if logical.Len() > 0 {
			logical.WriteByte(' ')
		}
		logical.WriteString(strings.TrimSpace(line))
		if continued {
			continue
		}
		flush()
	}
	flush()
	return vars
}

func expandMakeVariable(name string, vars map[string]string, stack map[string]bool) (string, error) {
	if stack == nil {
		stack = make(map[string]bool)
	}
	if stack[name] {
		return "", fmt.Errorf("recursive make variable reference %q", name)
	}
	stack[name] = true
	defer delete(stack, name)

	value, ok := vars[name]
	if !ok {
		return "", nil
	}

	var out strings.Builder
	last := 0
	for _, loc := range makeVarRefPattern.FindAllStringSubmatchIndex(value, -1) {
		out.WriteString(value[last:loc[0]])
		refName := value[loc[2]:loc[3]]
		refValue, err := expandMakeVariable(refName, vars, stack)
		if err != nil {
			return "", err
		}
		out.WriteString(refValue)
		last = loc[1]
	}
	out.WriteString(value[last:])
	return out.String(), nil
}

func testScriptExists(workDir, rel string) bool {
	path := filepath.Join(workDir, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if ext := filepath.Ext(path); ext != "" {
		return false
	}
	for _, candidate := range []string{path + ".sh", path + ".pl", path + ".xpl"} {
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
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
	if strings.Contains(rel, "help-version") {
		return true, "help/version tests are skipped in v1", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Some GNU tests are generated on-demand by make rules, so they are
			// authoritative suite entries even when the script file is not present yet.
			return false, "", nil
		}
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
		"srcdir=" + workDir,
		"TESTS=" + strings.Join(tests, " "),
	}
	cmd := exec.Command(makeBin, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"CONFIG_SHELL="+configShell,
	)
	configureIsolatedProcessGroup(cmd)

	logFile, err := os.Create(logPath)
	if err != nil {
		return makeCheckResult{}, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	stream := &captureTeeWriter{dest: io.MultiWriter(os.Stdout, logFile)}
	cmd.Stdout = stream
	cmd.Stderr = stream

	fmt.Printf("gbash-gnu: running make check for %d test(s)\n", len(tests))
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return makeCheckResult{}, err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			output := stream.Bytes()
			if err == nil {
				fmt.Printf("gbash-gnu: make check finished in %s\n", time.Since(startedAt).Round(time.Second))
				return makeCheckResult{Output: output}, nil
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				fmt.Printf("gbash-gnu: make check finished in %s with exit code %d\n", time.Since(startedAt).Round(time.Second), exitErr.ExitCode())
				return makeCheckResult{ExitCode: exitErr.ExitCode(), Output: output}, nil
			}
			return makeCheckResult{}, err
		case <-ctx.Done():
			_ = terminateIsolatedProcessGroup(cmd)
			<-done
			return makeCheckResult{}, ctx.Err()
		case <-ticker.C:
			fmt.Printf("gbash-gnu: still running make check after %s (%d test(s))\n", time.Since(startedAt).Round(time.Second), len(tests))
		}
	}
}

type captureTeeWriter struct {
	mu   sync.Mutex
	dest io.Writer
	buf  bytes.Buffer
}

func (w *captureTeeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.buf.Write(p); err != nil {
		return 0, err
	}
	if _, err := w.dest.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *captureTeeWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

func uniqueSortedStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
