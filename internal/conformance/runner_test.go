package conformance

import (
	"os"
	"path/filepath"
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
	if got, want := OracleCommandArgs(OracleBashPosix, "echo hi"), []string{"--posix", "--noprofile", "--norc", "-c", "echo hi"}; !equalStrings(got, want) {
		t.Fatalf("OracleCommandArgs(posix) = %#v, want %#v", got, want)
	}
}

func TestNormalizeOutputAndBashStderr(t *testing.T) {
	t.Parallel()

	workspace := "/tmp/gbash-conformance-123"
	if got, want := normalizeOutput("/tmp/gbash-conformance-123/bin/tool\n", workspace), "/bin/tool\n"; got != want {
		t.Fatalf("normalizeOutput() = %q, want %q", got, want)
	}
	if got, want := normalizeBashStderr("/tmp/x/bash: line 2: parse error\n"), "parse error\n"; got != want {
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
