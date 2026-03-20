package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	gbruntime "github.com/ewhauser/gbash/internal/runtime"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/internal/testutil"
	"github.com/ewhauser/gbash/policy"
)

var bashLinePrefixPattern = regexp.MustCompile(`(?m)^(?:[^:\n]+/)?\w+:(?:[^:\n]+:)* line \d+: `)
var bashShellPrefixPattern = regexp.MustCompile(`^(?:[^:\n]+/)?[A-Za-z0-9_-]*sh: `)
var bashAnsiCQuotedCommandNotFoundPattern = regexp.MustCompile(`\$'((?:[^'\\]|\\.)*)': command not found`)
var bashQuotedNoSuchFilePattern = regexp.MustCompile(`(?m)^([-.[:alnum:]_]+): '([^']+)': No such file or directory$`)
var bashCannotOpenNoSuchFilePattern = regexp.MustCompile(`(?m)^([-.[:alnum:]_]+): cannot (?:open|remove) '([^']+)'(?: for reading)?: No such file or directory$`)

var (
	conformanceLocaleOnce sync.Once
	conformanceLocaleName string
)

const (
	isolatedGBashWorkspaceRoot = "/work"
	conformanceVirtualHomeDir  = "/home/agent"
)

func resolvedSuiteConfig(cfg *SuiteConfig) SuiteConfig {
	resolved := *cfg
	resolved.SpecDir = packageRelativePath(resolved.SpecDir)
	resolved.BinDir = packageRelativePath(resolved.BinDir)
	resolved.ManifestPath = packageRelativePath(resolved.ManifestPath)
	if len(resolved.FixtureDirs) > 0 {
		fixtures := make([]string, 0, len(resolved.FixtureDirs))
		for _, dir := range resolved.FixtureDirs {
			fixtures = append(fixtures, packageRelativePath(dir))
		}
		resolved.FixtureDirs = fixtures
	}
	return resolved
}

func packageRelativePath(relPath string) string {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" || filepath.IsAbs(relPath) {
		return relPath
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return relPath
	}
	return filepath.Join(filepath.Dir(file), relPath)
}

func RunSuite(t *testing.T, cfg *SuiteConfig) {
	t.Helper()
	resolvedCfg := resolvedSuiteConfig(cfg)
	cfg = &resolvedCfg

	bashPath := testutil.RequireNixBash(t)

	manifest, err := LoadManifest(cfg.ManifestPath)
	if err != nil {
		t.Fatalf("LoadManifest(%q) error = %v", cfg.ManifestPath, err)
	}
	specFiles, err := LoadSpecFiles(cfg.SpecDir, cfg.SpecFiles)
	if err != nil {
		t.Fatalf("LoadSpecFiles(%q) error = %v", cfg.SpecDir, err)
	}

	for _, specFile := range specFiles {
		t.Run(specFile.Path, func(t *testing.T) {
			t.Parallel()

			fileEntry, hasFileEntry := manifest.LookupFile(cfg.Name, specFile.Path)
			if hasFileEntry && fileEntry.Mode == EntryModeSkip {
				t.Skipf("manifest skip: %s", fileEntry.Reason)
			}

			var fileXFailed atomic.Bool
			if hasFileEntry && fileEntry.Mode == EntryModeXFail {
				t.Cleanup(func() {
					if !fileXFailed.Load() {
						t.Fatalf("unexpected pass for manifest xfail file: %s", fileEntry.Reason)
					}
				})
			}
			for _, specCase := range specFile.Cases {
				t.Run(specCase.Name, func(t *testing.T) {
					t.Parallel()

					caseEntry, hasCaseEntry := manifest.LookupCase(cfg.Name, specFile.Path, specCase.Name)
					if hasCaseEntry && caseEntry.Mode == EntryModeSkip {
						t.Skipf("manifest skip: %s", caseEntry.Reason)
					}

					result, err := RunCase(t.Context(), cfg, bashPath, specFile.Path, specCase)
					if err != nil {
						t.Fatalf("RunCase() error = %v", err)
					}
					matched := result.GBash == result.Bash

					switch DetermineCaseOutcome(fileEntry, hasFileEntry, caseEntry, hasCaseEntry, matched) {
					case CaseOutcomePass:
						return
					case CaseOutcomeSkip:
						t.Skip("manifest skip")
					case CaseOutcomeExpectedFailure:
						fileXFailed.Store(true)
						t.Logf("expected failure: %s", expectedFailureReason(fileEntry, hasFileEntry, caseEntry, hasCaseEntry))
						t.Logf("gbash:\n%s", formatExecutionResult(result.GBash))
						t.Logf("bash:\n%s", formatExecutionResult(result.Bash))
					case CaseOutcomeUnexpectedPass:
						t.Fatalf("unexpected pass: %s", expectedFailureReason(fileEntry, hasFileEntry, caseEntry, hasCaseEntry))
					case CaseOutcomeFailure:
						t.Fatalf("bash mismatch\ngbash:\n%s\n\nbash:\n%s", formatExecutionResult(result.GBash), formatExecutionResult(result.Bash))
					}
				})
			}
		})
	}
}

func RunCase(ctx context.Context, cfg *SuiteConfig, bashPath, specPath string, specCase SpecCase) (ComparisonResult, error) {
	resolvedCfg := resolvedSuiteConfig(cfg)
	cfg = &resolvedCfg

	bashWorkspace, err := prepareWorkspace(cfg, specPath)
	if err != nil {
		return ComparisonResult{}, err
	}
	defer removeAll(bashWorkspace)

	gbashWorkspace, err := prepareWorkspace(cfg, specPath)
	if err != nil {
		return ComparisonResult{}, err
	}
	defer removeAll(gbashWorkspace)

	script := ensureTrailingNewline(specCase.Script)

	bashResult, err := runBash(ctx, cfg, bashPath, specPath, bashWorkspace, script)
	if err != nil {
		return ComparisonResult{}, err
	}
	gbashResult, err := runGBash(ctx, specPath, gbashWorkspace, script)
	if err != nil {
		return ComparisonResult{}, err
	}
	return ComparisonResult{
		GBash: normalizeExecutionResult(gbashResult, gbashWorkspace, gbashWorkspaceRoot(specPath)),
		Bash:  normalizeOracleResult(cfg.OracleMode, specPath, specCase, normalizeExecutionResult(bashResult, bashWorkspace, "/")),
	}, nil
}

//nolint:forbidigo // The conformance harness builds isolated host temp workspaces per case.
func prepareWorkspace(cfg *SuiteConfig, specPath string) (string, error) {
	workspace, err := os.MkdirTemp("", "gbash-conformance-*")
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil && resolved != "" {
		workspace = resolved
	}

	for _, relDir := range append([]string{cfg.BinDir}, cfg.FixtureDirs...) {
		if strings.TrimSpace(relDir) == "" {
			continue
		}
		src := relDir
		dst := workspace
		switch filepath.Base(relDir) {
		case "bin":
			dst = filepath.Join(workspace, "bin")
		default:
			if useScopedGlobWorkspace(specPath) {
				dst = filepath.Join(workspace, filepath.Base(relDir))
			}
		}
		if err := copyTree(src, dst); err != nil {
			removeAll(workspace)
			return "", err
		}
	}

	if err := os.MkdirAll(filepath.Join(workspace, "tmp"), 0o755); err != nil {
		removeAll(workspace)
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(workspace, "_tmp"), 0o755); err != nil {
		removeAll(workspace)
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(workspace, "_tmp", "spec-tmp"), 0o755); err != nil {
		removeAll(workspace)
		return "", err
	}
	return workspace, nil
}

//nolint:forbidigo // The conformance harness copies vendored helper trees into a host temp workspace.
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		if filepath.Base(src) == "bin" {
			return os.Chmod(target, 0o755)
		}
		return nil
	})
}

//nolint:forbidigo // The conformance harness copies vendored fixtures into a host temp workspace.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer closeIgnoringError(in)

	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	outClosed := false
	defer func() {
		if !outClosed {
			closeIgnoringError(out)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	outClosed = true
	return nil
}

func runGBash(ctx context.Context, specPath, workspace, script string) (ExecutionResult, error) {
	env := gbashEnv(specPath)
	opts := []gbruntime.Option{gbruntime.WithFileSystem(virtualWorkspaceFileSystem(workspace, gbashWorkspaceRoot(specPath)))}
	if useScopedGlobWorkspace(specPath) {
		opts = append([]gbruntime.Option{gbruntime.WithBaseEnv(env)}, opts...)
	}
	if specPath == "oils/builtin-cd.test.sh" {
		opts = append(opts, gbruntime.WithPolicy(policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits: policy.Limits{
				MaxCommandCount:      10000,
				MaxGlobOperations:    100000,
				MaxLoopIterations:    10000,
				MaxSubstitutionDepth: 50,
				MaxStdoutBytes:       1 << 20,
				MaxStderrBytes:       1 << 20,
				MaxFileBytes:         8 << 20,
			},
			SymlinkMode: policy.SymlinkFollow,
		})))
	}
	rt, err := gbruntime.New(opts...)
	if err != nil {
		return ExecutionResult{}, err
	}
	session, err := rt.NewSession(ctx)
	if err != nil {
		return ExecutionResult{}, err
	}
	result, err := session.Exec(ctx, &gbruntime.ExecutionRequest{
		Script:     script,
		WorkDir:    gbashWorkspaceRoot(specPath),
		ReplaceEnv: true,
		Env:        env,
	})
	if err != nil {
		errMsg := err.Error()
		var parseErr syntax.ParseError
		if errors.As(err, &parseErr) {
			if parseErr.SourceLine == "" && parseErr.WantsSourceLine() {
				parseErr.SourceLine = extractSourceLine(script, parseErr.Pos.Line())
			}
			errMsg = parseErr.BashError()
		}
		return ExecutionResult{ //nolint:nilerr // non-ExitError is mapped to exit code 2
			ExitCode: 2,
			Stderr:   errMsg + "\n",
		}, nil
	}
	return ExecutionResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}, nil
}

func virtualWorkspaceFileSystem(workspace, sandboxRoot string) gbruntime.FileSystemConfig {
	return gbruntime.CustomFileSystem(gbfs.FactoryFunc(func(ctx context.Context) (gbfs.FileSystem, error) {
		return loadWorkspaceIntoMemory(ctx, workspace, sandboxRoot)
	}), "/")
}

func loadWorkspaceIntoMemory(ctx context.Context, workspace, sandboxRoot string) (gbfs.FileSystem, error) {
	mem := gbfs.NewMemory()
	if err := copyWorkspaceToSandbox(ctx, mem, workspace, sandboxRoot); err != nil {
		return nil, err
	}
	if sandboxRoot != "/" {
		if err := copyWorkspaceToSandbox(ctx, mem, filepath.Join(workspace, "bin"), "/bin"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return mem, nil
}

//nolint:forbidigo // The harness mirrors a host temp workspace into a virtual in-memory filesystem.
func copyWorkspaceToSandbox(ctx context.Context, dst gbfs.FileSystem, workspace, sandboxRoot string) error {
	info, err := os.Stat(workspace)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", workspace)
	}
	return filepath.WalkDir(workspace, func(hostPath string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(workspace, hostPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		sandboxPath := path.Join(sandboxRoot, filepath.ToSlash(rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			if err := dst.MkdirAll(ctx, sandboxPath, mode.Perm()); err != nil {
				return err
			}
			if err := dst.Chmod(ctx, sandboxPath, mode.Perm()); err != nil {
				return err
			}
			return dst.Chtimes(ctx, sandboxPath, info.ModTime(), info.ModTime())
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(hostPath)
			if err != nil {
				return err
			}
			return dst.Symlink(ctx, filepath.ToSlash(target), sandboxPath)
		case mode.IsRegular():
			return copyWorkspaceFileToSandbox(ctx, dst, hostPath, sandboxPath, info)
		default:
			return fmt.Errorf("unsupported fixture type %q (%s)", hostPath, mode.Type())
		}
	})
}

//nolint:forbidigo // The harness mirrors host fixture files into a virtual in-memory filesystem.
func copyWorkspaceFileToSandbox(ctx context.Context, dst gbfs.FileSystem, hostPath, sandboxPath string, info stdfs.FileInfo) error {
	if err := dst.MkdirAll(ctx, path.Dir(sandboxPath), 0o755); err != nil {
		return err
	}
	in, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer closeIgnoringError(in)

	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := dst.OpenFile(ctx, sandboxPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	outClosed := false
	defer func() {
		if !outClosed {
			closeIgnoringError(out)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	outClosed = true
	if err := dst.Chmod(ctx, sandboxPath, mode); err != nil {
		return err
	}
	return dst.Chtimes(ctx, sandboxPath, info.ModTime(), info.ModTime())
}

//nolint:forbidigo // The oracle side of the harness intentionally executes the real host bash binary.
func runBash(ctx context.Context, cfg *SuiteConfig, bashPath, specPath, workspace, script string) (ExecutionResult, error) {
	args := OracleCommandArgs(cfg.OracleMode, script)
	cmd := exec.CommandContext(ctx, bashPath, args...)
	cmd.Dir = workspace
	cmd.Env = bashEnv(workspace, specPath)

	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return ExecutionResult{}, err
		}
		exitCode = exitErr.ExitCode()
	}
	return ExecutionResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

func OracleCommandArgs(mode OracleMode, script string) []string {
	switch mode {
	case OracleBash:
		return []string{"--noprofile", "--norc", "-c", script}
	default:
		return []string{"--noprofile", "--norc", "-c", script}
	}
}

func normalizeExecutionResult(result ExecutionResult, workspace, sandboxRoot string) ExecutionResult {
	result.Stdout = normalizeOutput(result.Stdout, workspace, sandboxRoot)
	result.Stderr = normalizeBashStderr(normalizeOutput(result.Stderr, workspace, sandboxRoot))
	return result
}

func normalizeOutput(value, workspace, sandboxRoot string) string {
	value = filepath.ToSlash(value)
	workspace = filepath.ToSlash(workspace)
	value = strings.ReplaceAll(value, workspace+"/", "/")
	value = strings.ReplaceAll(value, workspace, "/")
	value = strings.ReplaceAll(value, conformanceVirtualHomeDir+"/", "/")
	value = strings.ReplaceAll(value, conformanceVirtualHomeDir, "/")
	if sandboxRoot != "/" {
		value = strings.ReplaceAll(value, sandboxRoot+"/", "/")
		value = strings.ReplaceAll(value, sandboxRoot, "/")
	}
	if runtime.GOOS == "darwin" {
		value = strings.ReplaceAll(value, "/private/tmp/", "/tmp/")
		value = strings.ReplaceAll(value, "/private/tmp\n", "/tmp\n")
	}
	return value
}

func normalizeBashStderr(value string) string {
	value = bashLinePrefixPattern.ReplaceAllString(filepath.ToSlash(value), "")
	value = normalizeNestedShellPrefixes(value)
	value = strings.ReplaceAll(value, "shopt: usage: shopt [-pqsu] [-o long-option] optname [optname...]\n", "shopt: usage: shopt [-pqsu] [-o] [optname ...]\n")
	value = bashCannotOpenNoSuchFilePattern.ReplaceAllString(value, "$1: $2: No such file or directory")
	value = bashQuotedNoSuchFilePattern.ReplaceAllString(value, "$1: $2: No such file or directory")
	return bashAnsiCQuotedCommandNotFoundPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := bashAnsiCQuotedCommandNotFoundPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		unquoted, err := strconv.Unquote(`"` + parts[1] + `"`)
		if err != nil {
			return match
		}
		return unquoted + ": command not found"
	})
}

func normalizeNestedShellPrefixes(value string) string {
	lines := strings.SplitAfter(value, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		if trimmed == "" {
			continue
		}
		match := bashShellPrefixPattern.FindString(trimmed)
		if match == "" {
			continue
		}
		rest := strings.TrimPrefix(trimmed, match)
		if !isNestedShellDiagnostic(rest) {
			continue
		}
		lines[i] = rest + line[len(trimmed):]
	}
	return strings.Join(lines, "")
}

func isNestedShellDiagnostic(line string) bool {
	switch {
	case strings.Contains(line, "unbound variable"):
		return true
	case strings.Contains(line, "bad substitution"):
		return true
	case strings.Contains(line, "value too great for base"):
		return true
	case strings.Contains(line, "invalid number"):
		return true
	case strings.Contains(line, "arithmetic syntax error"):
		return true
	case strings.Contains(line, "division by 0"):
		return true
	case strings.HasSuffix(line, ": command not found"):
		return true
	default:
		return false
	}
}

//nolint:forbidigo // The isolated conformance harness mirrors numeric IDs for bash parity.
func gbashEnv(specPath string) map[string]string {
	locale := conformanceLocale()
	env := map[string]string{
		"LANG":                  locale,
		"LC_ALL":                locale,
		"SH":                    "bash",
		"TZ":                    "UTC",
		"GBASH_CONFORMANCE_SED": "sed",
	}
	if useScopedGlobWorkspace(specPath) {
		env["HOME"] = isolatedGBashWorkspaceRoot
		env["PATH"] = "/bin"
		env["PWD"] = isolatedGBashWorkspaceRoot
		env["TMP"] = isolatedGBashWorkspaceRoot + "/tmp"
		env["TMPDIR"] = isolatedGBashWorkspaceRoot + "/tmp"
		env["UID"] = strconv.Itoa(os.Getuid())
		env["EUID"] = strconv.Itoa(os.Geteuid())
		env["GID"] = strconv.Itoa(os.Getgid())
		env["EGID"] = strconv.Itoa(os.Getegid())
		return env
	}
	env["HOME"] = conformanceVirtualHomeDir
	env["PATH"] = "/bin:/usr/bin"
	env["PWD"] = "/"
	env["TMP"] = "/tmp"
	env["TMPDIR"] = "/tmp"
	if needsRepoRootEnv(specPath) {
		env["REPO_ROOT"] = gbashWorkspaceRoot(specPath)
	}
	return env
}

func bashEnv(workspace, specPath string) []string {
	locale := conformanceLocale()
	values := []string{
		"HOME=" + workspace,
		"PWD=" + workspace,
		"PATH=" + filepath.Join(workspace, "bin") + ":/usr/bin:/bin",
		"LANG=" + locale,
		"LC_ALL=" + locale,
		"SH=bash",
		"TZ=UTC",
		"TMP=" + filepath.Join(workspace, "tmp"),
		"TMPDIR=" + filepath.Join(workspace, "tmp"),
		"GBASH_CONFORMANCE_SED=" + conformanceToolPath("sed"),
	}
	if needsRepoRootEnv(specPath) {
		values = append(values, "REPO_ROOT="+workspace)
	}
	slices.Sort(values)
	return values
}

func conformanceToolPath(name string) string {
	toolPath, err := exec.LookPath(name)
	if err != nil {
		return name
	}
	return toolPath
}

//nolint:forbidigo // The conformance harness needs a host-level escape hatch for locale selection.
func conformanceLocale() string {
	conformanceLocaleOnce.Do(func() {
		conformanceLocaleName = "C.UTF-8"
		if override := strings.TrimSpace(os.Getenv("GBASH_CONFORMANCE_LOCALE")); override != "" {
			conformanceLocaleName = override
			return
		}
	})
	return conformanceLocaleName
}

func normalizeOracleResult(mode OracleMode, specPath string, specCase SpecCase, result ExecutionResult) ExecutionResult {
	if !shouldApplyOracleOverrides(specPath) {
		return result
	}
	return applyOracleOverrides(mode, specCase, result)
}

func gbashWorkspaceRoot(specPath string) string {
	if useScopedGlobWorkspace(specPath) {
		return isolatedGBashWorkspaceRoot
	}
	return "/"
}

func needsRepoRootEnv(specPath string) bool {
	return useScopedGlobWorkspace(specPath) || specPath == "oils/builtin-completion.test.sh"
}

func useScopedGlobWorkspace(specPath string) bool {
	switch specPath {
	case "oils/extglob-files.test.sh",
		"oils/extglob-match.test.sh",
		"oils/glob-bash.test.sh",
		"oils/glob.test.sh",
		"oils/globignore.test.sh",
		"oils/globstar.test.sh",
		"oils/redirect-multi.test.sh":
		return true
	default:
		return false
	}
}

func shouldApplyOracleOverrides(specPath string) bool {
	switch specPath {
	case "oils/dbracket.test.sh",
		"oils/globignore.test.sh",
		"oils/globstar.test.sh",
		"oils/redirect-multi.test.sh":
		return true
	default:
		return false
	}
}

func applyOracleOverrides(mode OracleMode, specCase SpecCase, result ExecutionResult) ExecutionResult {
	override, ok := specCase.OracleOverrides[mode]
	if !ok {
		return result
	}
	if override.Status != nil {
		if specCase.Expectation.Status != nil {
			result.ExitCode = *specCase.Expectation.Status
		} else {
			result.ExitCode = *override.Status
		}
	}
	if override.Stdout != nil {
		if specCase.Expectation.Stdout != nil {
			result.Stdout = *specCase.Expectation.Stdout
		} else {
			result.Stdout = *override.Stdout
		}
	}
	if override.Stderr != nil {
		if specCase.Expectation.Stderr != nil {
			result.Stderr = *specCase.Expectation.Stderr
		} else {
			result.Stderr = *override.Stderr
		}
	}
	return result
}

func expectedFailureReason(fileEntry ManifestEntry, hasFileEntry bool, caseEntry ManifestEntry, hasCaseEntry bool) string {
	if hasCaseEntry {
		return caseEntry.Reason
	}
	if hasFileEntry {
		return fileEntry.Reason
	}
	return "expected failure"
}

func formatExecutionResult(result ExecutionResult) string {
	return fmt.Sprintf("exit_code: %d\nstdout: %q\nstderr: %q", result.ExitCode, result.Stdout, result.Stderr)
}

func ensureTrailingNewline(script string) string {
	if script == "" || strings.HasSuffix(script, "\n") {
		return script
	}
	return script + "\n"
}

//nolint:forbidigo // Host temp workspaces are cleaned up after each conformance case.
func removeAll(target string) {
	_ = os.RemoveAll(target)
}

func closeIgnoringError(closer io.Closer) {
	_ = closer.Close()
}

// extractSourceLine returns the content of the given line number (1-indexed) from the script.
func extractSourceLine(script string, lineNum uint) string {
	if lineNum == 0 {
		return ""
	}
	lines := strings.Split(script, "\n")
	idx := int(lineNum) - 1
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
}
