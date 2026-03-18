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
	"sync/atomic"
	"testing"

	"github.com/ewhauser/gbash"
	gbfs "github.com/ewhauser/gbash/fs"
)

var bashLinePrefixPattern = regexp.MustCompile(`(?m)^(?:[^:\n]+/)?\w+: line \d+: `)
var bashAnsiCQuotedCommandNotFoundPattern = regexp.MustCompile(`\$'((?:[^'\\]|\\.)*)': command not found`)

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

	bashPath := os.Getenv("GBASH_CONFORMANCE_BASH") //nolint:forbidigo // Test harness reads host env to locate the oracle bash binary.
	if bashPath == "" {
		t.Fatal("GBASH_CONFORMANCE_BASH is not set.\n\nRun conformance tests via:\n  make conformance-test\n\nTo run a single test file:\n  make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/append.test.sh'")
	}
	out, err := exec.CommandContext(t.Context(), bashPath, "--version").Output() //nolint:forbidigo // Validate oracle version matches pinned Nix bash.
	if err != nil {
		t.Fatalf("failed to get bash version: %v", err)
	}
	firstLine, _, _ := strings.Cut(string(out), "\n")
	if !strings.Contains(firstLine, "version 5.3.9") {
		t.Fatalf("conformance tests require bash 5.3.9 (pinned via Nix), got: %s\n\nRun conformance tests via:\n  make conformance-test\n\nTo run a single test file:\n  make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/append.test.sh'", firstLine)
	}
	t.Logf("bash oracle: %s (%s)", firstLine, bashPath)

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

					result, err := RunCase(t.Context(), cfg, bashPath, specCase)
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

func RunCase(ctx context.Context, cfg *SuiteConfig, bashPath string, specCase SpecCase) (ComparisonResult, error) {
	resolvedCfg := resolvedSuiteConfig(cfg)
	cfg = &resolvedCfg

	bashWorkspace, err := prepareWorkspace(cfg)
	if err != nil {
		return ComparisonResult{}, err
	}
	defer removeAll(bashWorkspace)

	gbashWorkspace, err := prepareWorkspace(cfg)
	if err != nil {
		return ComparisonResult{}, err
	}
	defer removeAll(gbashWorkspace)

	script := ensureTrailingNewline(specCase.Script)

	bashResult, err := runBash(ctx, cfg, bashPath, bashWorkspace, script)
	if err != nil {
		return ComparisonResult{}, err
	}
	gbashResult, err := runGBash(ctx, cfg, gbashWorkspace, script)
	if err != nil {
		return ComparisonResult{}, err
	}
	return ComparisonResult{
		GBash: normalizeExecutionResult(gbashResult, gbashWorkspace),
		Bash:  normalizeExecutionResult(bashResult, bashWorkspace),
	}, nil
}

//nolint:forbidigo // The conformance harness builds isolated host temp workspaces per case.
func prepareWorkspace(cfg *SuiteConfig) (string, error) {
	workspace, err := os.MkdirTemp("", "gbash-conformance-*")
	if err != nil {
		return "", err
	}

	for _, relDir := range append([]string{cfg.BinDir}, cfg.FixtureDirs...) {
		if strings.TrimSpace(relDir) == "" {
			continue
		}
		src := relDir
		dst := workspace
		if filepath.Base(relDir) == "bin" {
			dst = filepath.Join(workspace, "bin")
		}
		if filepath.Base(relDir) != "bin" {
			dst = workspace
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

func runGBash(ctx context.Context, cfg *SuiteConfig, workspace, script string) (ExecutionResult, error) {
	rt, err := gbash.New(gbash.WithFileSystem(virtualWorkspaceFileSystem(workspace)))
	if err != nil {
		return ExecutionResult{}, err
	}
	session, err := rt.NewSession(ctx)
	if err != nil {
		return ExecutionResult{}, err
	}
	result, err := session.Exec(ctx, &gbash.ExecutionRequest{
		Script:     script,
		WorkDir:    "/",
		ReplaceEnv: true,
		Env:        gbashEnv(cfg),
	})
	if err != nil {
		return ExecutionResult{ //nolint:nilerr // non-ExitError is mapped to exit code 2
			ExitCode: 2,
			Stderr:   err.Error() + "\n",
		}, nil
	}
	return ExecutionResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}, nil
}

func virtualWorkspaceFileSystem(workspace string) gbash.FileSystemConfig {
	return gbash.CustomFileSystem(gbfs.FactoryFunc(func(ctx context.Context) (gbfs.FileSystem, error) {
		return loadWorkspaceIntoMemory(ctx, workspace)
	}), "/")
}

func loadWorkspaceIntoMemory(ctx context.Context, workspace string) (gbfs.FileSystem, error) {
	mem := gbfs.NewMemory()
	if err := copyWorkspaceToSandbox(ctx, mem, workspace); err != nil {
		return nil, err
	}
	return mem, nil
}

//nolint:forbidigo // The harness mirrors a host temp workspace into a virtual in-memory filesystem.
func copyWorkspaceToSandbox(ctx context.Context, dst gbfs.FileSystem, workspace string) error {
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
		sandboxPath := "/" + filepath.ToSlash(rel)
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
func runBash(ctx context.Context, cfg *SuiteConfig, bashPath, workspace, script string) (ExecutionResult, error) {
	args := OracleCommandArgs(cfg.OracleMode, script)
	cmd := exec.CommandContext(ctx, bashPath, args...)
	cmd.Dir = workspace
	cmd.Env = bashEnv(cfg, workspace)

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
	case OracleBashPosix:
		return []string{"--posix", "--noprofile", "--norc", "-c", script}
	default:
		return []string{"--noprofile", "--norc", "-c", script}
	}
}

func normalizeExecutionResult(result ExecutionResult, workspace string) ExecutionResult {
	result.Stdout = normalizeOutput(result.Stdout, workspace)
	result.Stderr = normalizeBashStderr(normalizeOutput(result.Stderr, workspace))
	return result
}

func normalizeOutput(value, workspace string) string {
	value = filepath.ToSlash(value)
	workspace = filepath.ToSlash(workspace)
	value = strings.ReplaceAll(value, workspace+"/", "/")
	value = strings.ReplaceAll(value, workspace, "/")
	return value
}

func normalizeBashStderr(value string) string {
	value = bashLinePrefixPattern.ReplaceAllString(filepath.ToSlash(value), "")
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

func gbashEnv(cfg *SuiteConfig) map[string]string {
	env := map[string]string{
		"HOME":                  "/",
		"PATH":                  "/bin:/usr/bin",
		"LANG":                  "C",
		"LC_ALL":                "C",
		"PWD":                   "/",
		"SH":                    "bash",
		"TZ":                    "UTC",
		"TMP":                   "/tmp",
		"TMPDIR":                "/tmp",
		"GBASH_CONFORMANCE_SED": "sed",
	}
	if cfg.OracleMode == OracleBashPosix {
		env["POSIXLY_CORRECT"] = "1"
	}
	return env
}

func bashEnv(cfg *SuiteConfig, workspace string) []string {
	values := []string{
		"HOME=" + workspace,
		"PWD=" + workspace,
		"PATH=" + filepath.Join(workspace, "bin") + ":/usr/bin:/bin",
		"LANG=C",
		"LC_ALL=C",
		"SH=bash",
		"TZ=UTC",
		"TMP=" + filepath.Join(workspace, "tmp"),
		"TMPDIR=" + filepath.Join(workspace, "tmp"),
		"GBASH_CONFORMANCE_SED=" + conformanceToolPath("sed"),
	}
	if cfg.OracleMode == OracleBashPosix {
		values = append(values, "POSIXLY_CORRECT=1")
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
