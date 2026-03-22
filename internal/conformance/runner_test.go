package conformance

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestDetermineCaseOutcome(t *testing.T) {
	t.Parallel()

	fileEntry := ManifestEntry{Mode: EntryModeXFail, Reason: "file xfail"}
	caseEntry := ManifestEntry{Mode: EntryModeXFail, Reason: "case xfail"}

	if got := DetermineCaseOutcome(fileEntry, true, ManifestEntry{}, false, false); got != CaseOutcomeExpectedFailure {
		t.Fatalf("file-level mismatch = %v, want expected failure", got)
	}
	if got := DetermineCaseOutcome(ManifestEntry{}, false, caseEntry, true, true); got != CaseOutcomeUnexpectedPass {
		t.Fatalf("case-level xpass = %v, want unexpected pass", got)
	}
	if got := DetermineCaseOutcome(ManifestEntry{}, false, ManifestEntry{}, false, false); got != CaseOutcomeFailure {
		t.Fatalf("plain mismatch = %v, want failure", got)
	}
}

func TestOracleCommandArgs(t *testing.T) {
	t.Parallel()

	if got, want := OracleCommandArgs(OracleBash, "echo hi"), []string{"--noprofile", "--norc", "-c", "echo hi"}; !equalStrings(got, want) {
		t.Fatalf("OracleCommandArgs(bash) = %#v, want %#v", got, want)
	}
}

func TestGbashEnvMatchesDefaultPathOrder(t *testing.T) {
	t.Parallel()

	want := "/usr/bin:/bin"
	if got := gbashEnv("")["PATH"]; got != want {
		t.Fatalf("gbashEnv()[PATH] = %q, want %q", got, want)
	}
}

func TestReadOnlyPathsFSRejectsWriteIntents(t *testing.T) {
	t.Parallel()

	fsys := newReadOnlyPathsFS(gbfs.NewMemory(), "/zz")
	if err := fsys.MkdirAll(context.Background(), "/tmp", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp) error = %v", err)
	}
	_, err := fsys.OpenFile(context.Background(), "/tmp/out", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(/tmp/out) error = %v", err)
	}
	_, err = fsys.OpenFile(context.Background(), "/zz", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		t.Fatal("OpenFile(/zz) error = nil, want read-only failure")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("OpenFile(/zz) error = %T, want *os.PathError", err)
	}
	if got, want := pathErr.Path, "/zz"; got != want {
		t.Fatalf("OpenFile(/zz) path = %q, want %q", got, want)
	}
	if !errors.Is(err, syscall.EROFS) {
		t.Fatalf("OpenFile(/zz) error = %v, want EROFS", err)
	}
}

func TestNormalizeOutputAndBashStderr(t *testing.T) {
	t.Parallel()

	workspace := "/tmp/gbash-conformance-123"
	if got, want := normalizeOutput("/tmp/gbash-conformance-123/bin/tool\n", workspace, "/"), "/bin/tool\n"; got != want {
		t.Fatalf("normalizeOutput() = %q, want %q", got, want)
	}
	privateTmpWant := "/private/tmp/demo\n"
	if runtime.GOOS == "darwin" {
		privateTmpWant = "/tmp/demo\n"
	}
	if got := normalizeOutput("/private/tmp/demo\n", workspace, "/"); got != privateTmpWant {
		t.Fatalf("normalizeOutput(/private/tmp) = %q, want %q", got, privateTmpWant)
	}
	if got, want := normalizeOutput("/home/agent/project\n", workspace, "/"), "/project\n"; got != want {
		t.Fatalf("normalizeOutput(/home/agent) = %q, want %q", got, want)
	}
	if got, want := normalizeOutput("/work/spec/testdata/echo.sz\n", workspace, isolatedGBashWorkspaceRoot), "/spec/testdata/echo.sz\n"; got != want {
		t.Fatalf("normalizeOutput(/work) = %q, want %q", got, want)
	}
	if got, want := normalizeOutput("/proc/12345/fd\n", workspace, "/"), "/proc/PID/fd\n"; got != want {
		t.Fatalf("normalizeOutput(/proc fd) = %q, want %q", got, want)
	}
	if got, want := normalizeGBashStderr("/zz: open /zz: read-only file system\n"), "/zz: Read-only file system\n"; got != want {
		t.Fatalf("normalizeGBashStderr() = %q, want %q", got, want)
	}
	if got, want := normalizeTrapErrRedirectStderr("/zz: Read-only file system\n"), "/zz: Permission denied\n"; got != want {
		t.Fatalf("normalizeTrapErrRedirectStderr(read-only) = %q, want %q", got, want)
	}
	if got, want := normalizeTrapErrRedirectStderr("/zz: Permission denied\n"), "/zz: Permission denied\n"; got != want {
		t.Fatalf("normalizeTrapErrRedirectStderr(permission denied) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("/tmp/x/bash: line 2: parse error\n"), "parse error\n"; got != want {
		t.Fatalf("normalizeBashStderr() = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("bash: a + 42x: value too great for base (error token is \"42x\")\n"), "a + 42x: value too great for base (error token is \"42x\")\n"; got != want {
		t.Fatalf("normalizeBashStderr() = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("bash: illegal option -- Z\n"), "illegal option -- Z\n"; got != want {
		t.Fatalf("normalizeBashStderr(illegal option) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("bash: option requires an argument -- a\n"), "option requires an argument -- a\n"; got != want {
		t.Fatalf("normalizeBashStderr(missing argument) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("0x1X: value too great for base (error token is \"0x1X\")\n"), "0x1X: value too great for base (error token is \"0x1X\")\n"; got != want {
		t.Fatalf("normalizeBashStderr() = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("shopt: usage: shopt [-pqsu] [-o long-option] optname [optname...]\n"), "shopt: usage: shopt [-pqsu] [-o] [optname ...]\n"; got != want {
		t.Fatalf("normalizeBashStderr() = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("$'echo\\rTEST': command not found\n"), "echo\rTEST: command not found\n"; got != want {
		t.Fatalf("normalizeBashStderr() = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("/: Is a directory\n"), "/: redirect target is a directory\n"; got != want {
		t.Fatalf("normalizeBashStderr(directory) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("/: File exists\n"), "/: redirect target is a directory\n"; got != want {
		t.Fatalf("normalizeBashStderr(file exists) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("gzip: .gz: No such file or directory\n3: Bad file descriptor\ngzip: .gz: No such file or directory\n"), "3: Bad file descriptor\ngzip: .gz: No such file or directory\ngzip: .gz: No such file or directory\n"; got != want {
		t.Fatalf("normalizeBashStderr(bad fd interleave) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("gzip: .gz: No such file or directory\ngzip: .gz: No such file or directory\n3: Bad file descriptor\n"), "3: Bad file descriptor\ngzip: .gz: No such file or directory\ngzip: .gz: No such file or directory\n"; got != want {
		t.Fatalf("normalizeBashStderr(bad fd trailing) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("3: Bad file descriptor\n.gz: No such file or directory\ngzip: .gz: No such file or directory\n"), "3: Bad file descriptor\ngzip: .gz: No such file or directory\ngzip: .gz: No such file or directory\n"; got != want {
		t.Fatalf("normalizeBashStderr(mixed bad fd triplet) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("bash: user payload\nbash: line 1: syntax error in expression\n"), "bash: user payload\nsyntax error in expression\n"; got != want {
		t.Fatalf("normalizeBashStderr(user payload) = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("bash: echo 1\necho 2\n(( x ))\n: 0\necho 3\n: syntax error in expression (error token is \"1\necho 2\n(( x ))\n: 0\necho 3\n\")\n"), "echo 1\necho 2\n(( x ))\n: 0\necho 3\n: syntax error in expression (error token is \"1\necho 2\n(( x ))\n: 0\necho 3\n\")\n"; got != want {
		t.Fatalf("normalizeBashStderr(multiline context) = %q, want %q", got, want)
	}
}

func TestResolvedSuiteConfigUsesPackagePaths(t *testing.T) {
	t.Parallel()

	cfg := resolvedSuiteConfig(&SuiteConfig{
		SpecDir:      "oils",
		BinDir:       "bin",
		FixtureDirs:  []string{"fixtures"},
		ManifestPath: "manifest.json",
	})

	for _, got := range []string{cfg.SpecDir, cfg.BinDir, cfg.FixtureDirs[0], cfg.ManifestPath} {
		if !filepath.IsAbs(got) {
			t.Fatalf("resolved path %q is not absolute", got)
		}
	}
}

func TestApplyOracleOverridesUsesGenericExpectationForAnnotatedFields(t *testing.T) {
	t.Parallel()

	wantStdout := "expected\n"
	actualStdout := "buggy\n"
	specCase := SpecCase{
		Expectation: ExpectedResult{Stdout: &wantStdout},
		OracleOverrides: map[OracleMode]OracleOverride{
			OracleBash: {
				Kind:   OracleOverrideBug,
				Stdout: &actualStdout,
			},
		},
	}
	got := applyOracleOverrides(OracleBash, specCase, ExecutionResult{Stdout: actualStdout, ExitCode: 0})
	if got.Stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", got.Stdout, wantStdout)
	}
}

func TestNormalizeOracleResultScopesOverridesToTargetedSpecs(t *testing.T) {
	t.Parallel()

	wantStdout := "expected\n"
	actualStdout := "buggy\n"
	specCase := SpecCase{
		Expectation: ExpectedResult{Stdout: &wantStdout},
		OracleOverrides: map[OracleMode]OracleOverride{
			OracleBash: {
				Kind:   OracleOverrideBug,
				Stdout: &actualStdout,
			},
		},
	}

	got := normalizeOracleResult(OracleBash, "oils/builtin-completion.test.sh", specCase, ExecutionResult{Stdout: actualStdout})
	if got.Stdout != actualStdout {
		t.Fatalf("stdout for untargeted spec = %q, want %q", got.Stdout, actualStdout)
	}

	got = normalizeOracleResult(OracleBash, "oils/redirect-multi.test.sh", specCase, ExecutionResult{Stdout: actualStdout})
	if got.Stdout != wantStdout {
		t.Fatalf("stdout for targeted spec = %q, want %q", got.Stdout, wantStdout)
	}

	got = normalizeOracleResult(OracleBash, "oils/builtin-trap-err.test.sh", specCase, ExecutionResult{Stdout: actualStdout})
	if got.Stdout != wantStdout {
		t.Fatalf("stdout for builtin-trap-err targeted spec = %q, want %q", got.Stdout, wantStdout)
	}

	got = normalizeOracleResult(OracleBash, "oils/builtin-getopts.test.sh", specCase, ExecutionResult{Stdout: actualStdout})
	if got.Stdout != wantStdout {
		t.Fatalf("stdout for builtin-getopts targeted spec = %q, want %q", got.Stdout, wantStdout)
	}

	got = normalizeOracleResult(OracleBash, "oils/tilde.test.sh", specCase, ExecutionResult{Stdout: actualStdout})
	if got.Stdout != wantStdout {
		t.Fatalf("stdout for tilde targeted spec = %q, want %q", got.Stdout, wantStdout)
	}
}

func TestNormalizeOracleResultNormalizesBuiltinBracketTouchOracleAcrossPlatforms(t *testing.T) {
	t.Parallel()

	specCase := SpecCase{Name: "-ot and -nt"}
	got := normalizeOracleResult(OracleBash, "oils/builtin-bracket.test.sh", specCase, ExecutionResult{
		Stderr: "touch: out of range or illegal time specification: YYYY-MM-DDThh:mm:SS[.frac][tz]\ntouch: out of range or illegal time specification: YYYY-MM-DDThh:mm:SS[.frac][tz]\n",
	})
	if want := "touch: missing file operand\nTry 'touch --help' for more information.\n"; got.Stderr != want {
		t.Fatalf("stderr = %q, want %q", got.Stderr, want)
	}
}

func TestNormalizeOracleResultNormalizesTildeRootFallbackAcrossPlatforms(t *testing.T) {
	t.Parallel()

	got := normalizeOracleResult(OracleBash, "oils/tilde.test.sh", SpecCase{Name: "${x//~/~root}"}, ExecutionResult{
		Stdout: "/root\n/root\n[/root]\n",
	})
	want := "/root\n/root\n[/root]\n"
	if runtime.GOOS == "darwin" {
		want = "/var/root\n/var/root\n[/var/root]\n"
	}
	if got.Stdout != want {
		t.Fatalf("stdout = %q, want %q", got.Stdout, want)
	}
}

func TestNormalizeCaseResultTrimsWcPaddingForTildeRedirect(t *testing.T) {
	t.Parallel()

	got := normalizeCaseResult("oils/tilde.test.sh", SpecCase{Name: "tilde expansion of word after redirect"}, ExecutionResult{
		Stdout: "       3\n",
	})
	if want := "3\n"; got.Stdout != want {
		t.Fatalf("stdout = %q, want %q", got.Stdout, want)
	}
}

func TestLoadWorkspaceIntoMemoryPreservesFixturesAndMutability(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "bin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "empty"), 0o700); err != nil {
		t.Fatalf("MkdirAll(empty) error = %v", err)
	}
	toolPath := filepath.Join(workspace, "bin", "tool")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(tool) error = %v", err)
	}

	fsys, err := loadWorkspaceIntoMemory(context.Background(), workspace, "/")
	if err != nil {
		t.Fatalf("loadWorkspaceIntoMemory() error = %v", err)
	}

	info, err := fsys.Stat(context.Background(), "/empty")
	if err != nil {
		t.Fatalf("Stat(/empty) error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat(/empty).IsDir() = false, want true")
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o700); got != want {
		t.Fatalf("Stat(/empty).Mode().Perm() = %v, want %v", got, want)
	}

	info, err = fsys.Stat(context.Background(), "/bin/tool")
	if err != nil {
		t.Fatalf("Stat(/bin/tool) error = %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o755); got != want {
		t.Fatalf("Stat(/bin/tool).Mode().Perm() = %v, want %v", got, want)
	}

	file, err := fsys.Open(context.Background(), "/bin/tool")
	if err != nil {
		t.Fatalf("Open(/bin/tool) error = %v", err)
	}
	data, err := io.ReadAll(file)
	closeIgnoringError(file)
	if err != nil {
		t.Fatalf("ReadAll(/bin/tool) error = %v", err)
	}
	if got, want := string(data), "#!/bin/sh\necho hi\n"; got != want {
		t.Fatalf("ReadAll(/bin/tool) = %q, want %q", got, want)
	}

	out, err := fsys.OpenFile(context.Background(), "/new.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(/new.txt) error = %v", err)
	}
	if _, err := out.Write([]byte("mutable\n")); err != nil {
		t.Fatalf("Write(/new.txt) error = %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("Close(/new.txt) error = %v", err)
	}

	if _, err := fsys.Stat(context.Background(), "/new.txt"); err != nil {
		t.Fatalf("Stat(/new.txt) error = %v", err)
	}
}

func TestLoadWorkspaceIntoMemorySupportsIsolatedWorkspaceRoot(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "bin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "spec", "testdata"), 0o755); err != nil {
		t.Fatalf("MkdirAll(spec/testdata) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "bin", "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(tool) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "spec", "testdata", "echo.sz"), []byte("helper\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(echo.sz) error = %v", err)
	}

	fsys, err := loadWorkspaceIntoMemory(context.Background(), workspace, isolatedGBashWorkspaceRoot)
	if err != nil {
		t.Fatalf("loadWorkspaceIntoMemory() error = %v", err)
	}

	if _, err := fsys.Stat(context.Background(), isolatedGBashWorkspaceRoot+"/spec/testdata/echo.sz"); err != nil {
		t.Fatalf("Stat(/work/spec/testdata/echo.sz) error = %v", err)
	}
	if _, err := fsys.Stat(context.Background(), "/bin/tool"); err != nil {
		t.Fatalf("Stat(/bin/tool) error = %v", err)
	}
}

func TestPrepareWorkspaceUsesScopedFixtureBaseDirForGlobSpecs(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	binSrc := filepath.Join(srcRoot, "bin")
	fixtureSrc := filepath.Join(srcRoot, "spec")
	if err := os.MkdirAll(binSrc, 0o755); err != nil {
		t.Fatalf("MkdirAll(binSrc) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(fixtureSrc, "testdata"), 0o755); err != nil {
		t.Fatalf("MkdirAll(spec/testdata) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(binSrc, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(tool) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixtureSrc, "testdata", "echo.sz"), []byte("helper\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(echo.sz) error = %v", err)
	}
	bashPath := filepath.Join(srcRoot, "fake-bash")
	if err := os.WriteFile(bashPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake-bash) error = %v", err)
	}

	cfg := &SuiteConfig{BinDir: binSrc, FixtureDirs: []string{fixtureSrc}}

	globWorkspace, err := prepareWorkspace(cfg, "oils/glob.test.sh", bashPath)
	if err != nil {
		t.Fatalf("prepareWorkspace(glob) error = %v", err)
	}
	defer removeAll(globWorkspace)
	if _, err := os.Stat(filepath.Join(globWorkspace, "spec", "testdata", "echo.sz")); err != nil {
		t.Fatalf("Stat(glob spec/testdata/echo.sz) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(globWorkspace, "bin", "bash")); !os.IsNotExist(err) {
		t.Fatalf("Stat(glob bin/bash) error = %v, want not exist", err)
	}

	repoRootWorkspace, err := prepareWorkspace(cfg, "oils/assign-extended.test.sh", bashPath)
	if err != nil {
		t.Fatalf("prepareWorkspace(assign-extended) error = %v", err)
	}
	defer removeAll(repoRootWorkspace)
	if _, err := os.Stat(filepath.Join(repoRootWorkspace, "spec", "testdata", "echo.sz")); err != nil {
		t.Fatalf("Stat(assign-extended spec/testdata/echo.sz) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRootWorkspace, "testdata", "echo.sz")); !os.IsNotExist(err) {
		t.Fatalf("Stat(assign-extended testdata/echo.sz) error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(repoRootWorkspace, "bin", "bash")); !os.IsNotExist(err) {
		t.Fatalf("Stat(assign-extended bin/bash) error = %v, want not exist", err)
	}

	trapWorkspace, err := prepareWorkspace(cfg, "oils/builtin-trap.test.sh", bashPath)
	if err != nil {
		t.Fatalf("prepareWorkspace(builtin-trap) error = %v", err)
	}
	defer removeAll(trapWorkspace)
	if _, err := os.Stat(filepath.Join(trapWorkspace, "spec", "testdata", "echo.sz")); err != nil {
		t.Fatalf("Stat(builtin-trap spec/testdata/echo.sz) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(trapWorkspace, "testdata", "echo.sz")); !os.IsNotExist(err) {
		t.Fatalf("Stat(builtin-trap testdata/echo.sz) error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(trapWorkspace, "bin", "bash")); !os.IsNotExist(err) {
		t.Fatalf("Stat(builtin-trap bin/bash) error = %v, want not exist", err)
	}

	defaultWorkspace, err := prepareWorkspace(cfg, "oils/serialize.test.sh", bashPath)
	if err != nil {
		t.Fatalf("prepareWorkspace(default) error = %v", err)
	}
	defer removeAll(defaultWorkspace)
	if _, err := os.Stat(filepath.Join(defaultWorkspace, "testdata", "echo.sz")); err != nil {
		t.Fatalf("Stat(default testdata/echo.sz) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(defaultWorkspace, "spec", "testdata", "echo.sz")); !os.IsNotExist(err) {
		t.Fatalf("Stat(default spec/testdata/echo.sz) error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(defaultWorkspace, "bin", "bash")); !os.IsNotExist(err) {
		t.Fatalf("Stat(default bin/bash) error = %v, want not exist", err)
	}

	varOpWorkspace, err := prepareWorkspace(cfg, "oils/var-op-bash.test.sh", bashPath)
	if err != nil {
		t.Fatalf("prepareWorkspace(var-op-bash) error = %v", err)
	}
	defer removeAll(varOpWorkspace)
	if info, err := os.Stat(filepath.Join(varOpWorkspace, "bin", "bash")); err != nil {
		t.Fatalf("Stat(var-op-bash bin/bash) error = %v", err)
	} else if got, want := info.Mode().Perm(), os.FileMode(0o755); got != want {
		t.Fatalf("Stat(var-op-bash bin/bash).Mode().Perm() = %v, want %v", got, want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func writeTempFile(t *testing.T, contents string) string {
	t.Helper()
	path := t.TempDir() + "/manifest.json"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
