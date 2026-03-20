package conformance

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
	if got, want := normalizeBashStderr("/tmp/x/bash: line 2: parse error\n"), "parse error\n"; got != want {
		t.Fatalf("normalizeBashStderr() = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("bash: a + 42x: value too great for base (error token is \"42x\")\n"), "a + 42x: value too great for base (error token is \"42x\")\n"; got != want {
		t.Fatalf("normalizeBashStderr() = %q, want %q", got, want)
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

	cfg := &SuiteConfig{BinDir: binSrc, FixtureDirs: []string{fixtureSrc}}

	globWorkspace, err := prepareWorkspace(cfg, "oils/glob.test.sh")
	if err != nil {
		t.Fatalf("prepareWorkspace(glob) error = %v", err)
	}
	defer removeAll(globWorkspace)
	if _, err := os.Stat(filepath.Join(globWorkspace, "spec", "testdata", "echo.sz")); err != nil {
		t.Fatalf("Stat(glob spec/testdata/echo.sz) error = %v", err)
	}

	defaultWorkspace, err := prepareWorkspace(cfg, "oils/assign-extended.test.sh")
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
