package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseReportedTestResultsNormalizesAliasesAndDeduplicates(t *testing.T) {
	selected := []string{
		"tests/misc/dirname.pl",
		"tests/wc/wc-cpu.sh",
		"tests/wc/wc-files0-from.pl",
	}
	logData := []byte(`
FAIL: tests/misc/dirname.pl
FAIL: tests/misc/dirname
SKIP: tests/wc/wc-cpu.sh
SKIP: tests/wc/wc-cpu
FAIL: tests/wc/wc-files0-from
`)

	got, extras := parseReportedTestResults(logData, selected)
	want := []testResult{
		{
			Name:       "tests/misc/dirname.pl",
			Status:     "fail",
			ReportedAs: []string{"tests/misc/dirname.pl", "tests/misc/dirname"},
		},
		{
			Name:       "tests/wc/wc-cpu.sh",
			Status:     "skip",
			ReportedAs: []string{"tests/wc/wc-cpu.sh", "tests/wc/wc-cpu"},
		},
		{
			Name:       "tests/wc/wc-files0-from.pl",
			Status:     "fail",
			ReportedAs: []string{"tests/wc/wc-files0-from"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseReportedTestResults() = %#v, want %#v", got, want)
	}
	if len(extras) != 0 {
		t.Fatalf("extra results = %#v, want none", extras)
	}
}

func TestParseReportedTestResultsMarksUnreportedAndKeepsUnexpectedResults(t *testing.T) {
	selected := []string{
		"tests/misc/basename.pl",
		"tests/misc/dirname.pl",
	}
	logData := []byte(`
PASS: tests/misc/basename.pl
FAIL: tests/extra/unexpected.sh
`)

	got, extras := parseReportedTestResults(logData, selected)
	want := []testResult{
		{
			Name:       "tests/misc/basename.pl",
			Status:     "pass",
			ReportedAs: []string{"tests/misc/basename.pl"},
		},
		{
			Name:   "tests/misc/dirname.pl",
			Status: "unreported",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseReportedTestResults() = %#v, want %#v", got, want)
	}

	wantExtras := []testResult{
		{
			Name:       "tests/extra/unexpected.sh",
			Status:     "fail",
			ReportedAs: []string{"tests/extra/unexpected.sh"},
		},
	}
	if !reflect.DeepEqual(extras, wantExtras) {
		t.Fatalf("extra results = %#v, want %#v", extras, wantExtras)
	}
}

func TestSummariesComputePercentagesAndRollups(t *testing.T) {
	first := utilityResult{
		Name: "cat",
		Summary: summarizeTestResults([]testResult{
			{Name: "tests/cat/a.sh", Status: "pass"},
			{Name: "tests/cat/b.sh", Status: "fail"},
			{Name: "tests/cat/c.sh", Status: "skip"},
			{Name: "tests/cat/d.sh", Status: "unreported"},
		}, 2, 1),
		Passed: false,
	}
	second := utilityResult{
		Name: "basename",
		Summary: summarizeTestResults([]testResult{
			{Name: "tests/misc/basename.pl", Status: "pass"},
		}, 0, 0),
		Passed: true,
	}
	third := utilityResult{
		Name:    "printenv",
		Summary: summarizeTestResults(nil, 3, 0),
		Passed:  false,
	}
	fourth := utilityResult{
		Name: "cp",
		Summary: summarizeTestResults([]testResult{
			{Name: "tests/cp/basic.sh", Status: "skip"},
		}, 0, 0),
		Passed: true,
	}

	if got, want := first.Summary.PassPctSelected, 25.0; got != want {
		t.Fatalf("first pass_pct_selected = %v, want %v", got, want)
	}
	if got, want := first.Summary.PassPctRunnable, 50.0; got != want {
		t.Fatalf("first pass_pct_runnable = %v, want %v", got, want)
	}

	overall := summarizeOverall([]utilityResult{first, second, third, fourth})
	if got, want := overall.SelectedTotal, 6; got != want {
		t.Fatalf("overall selected_total = %d, want %d", got, want)
	}
	if got, want := overall.FilteredSkipTotal, 5; got != want {
		t.Fatalf("overall filtered_skip_total = %d, want %d", got, want)
	}
	if got, want := overall.ReportedExtraTotal, 1; got != want {
		t.Fatalf("overall reported_extra_total = %d, want %d", got, want)
	}
	if got, want := overall.Pass, 2; got != want {
		t.Fatalf("overall pass = %d, want %d", got, want)
	}
	if got, want := overall.Fail, 1; got != want {
		t.Fatalf("overall fail = %d, want %d", got, want)
	}
	if got, want := overall.Skip, 2; got != want {
		t.Fatalf("overall skip = %d, want %d", got, want)
	}
	if got, want := overall.Unreported, 1; got != want {
		t.Fatalf("overall unreported = %d, want %d", got, want)
	}
	if got, want := overall.RunnableTotal, 3; got != want {
		t.Fatalf("overall runnable_total = %d, want %d", got, want)
	}
	if got, want := overall.PassPctSelected, 33.33; got != want {
		t.Fatalf("overall pass_pct_selected = %v, want %v", got, want)
	}
	if got, want := overall.PassPctRunnable, 66.67; got != want {
		t.Fatalf("overall pass_pct_runnable = %v, want %v", got, want)
	}

	totals := summarizeUtilityTotals([]utilityResult{first, second, third, fourth})
	if got, want := totals.Total, 4; got != want {
		t.Fatalf("utility total = %d, want %d", got, want)
	}
	if got, want := totals.Passed, 1; got != want {
		t.Fatalf("utility passed = %d, want %d", got, want)
	}
	if got, want := totals.Failed, 1; got != want {
		t.Fatalf("utility failed = %d, want %d", got, want)
	}
	if got, want := totals.NoRunnableTests, 2; got != want {
		t.Fatalf("utility no_runnable_tests = %d, want %d", got, want)
	}
	if got, want := totals.PassPctTotal, 25.0; got != want {
		t.Fatalf("utility pass_pct_total = %v, want %v", got, want)
	}
	if got, want := totals.PassPctRunnable, 50.0; got != want {
		t.Fatalf("utility pass_pct_runnable = %v, want %v", got, want)
	}
}

func TestCompleteUtilityResultsAddsInactivePlaceholders(t *testing.T) {
	results := []utilityResult{
		{
			Name:   "basename",
			Passed: true,
			Summary: testSummary{
				SelectedTotal:   1,
				Pass:            1,
				RunnableTotal:   1,
				PassPctSelected: 100,
				PassPctRunnable: 100,
			},
		},
	}
	programs := []string{"base32", "basename", "expr"}
	manifestUtilities := []utilityManifest{{Name: "basename"}, {Name: "cat"}}
	selectedUtilities := []utilityManifest{{Name: "basename"}}
	supportedSet := map[string]struct{}{
		"base32":   {},
		"basename": {},
	}

	got := completeUtilityResults(results, programs, manifestUtilities, selectedUtilities, supportedSet)
	if len(got) != 3 {
		t.Fatalf("completeUtilityResults() len = %d, want 3", len(got))
	}
	if got[0].Name != "base32" || !got[0].Inactive {
		t.Fatalf("base32 row = %#v, want inactive placeholder", got[0])
	}
	if got[0].Reason != "implemented in gbash, but not included in the compatibility manifest" {
		t.Fatalf("base32 reason = %q", got[0].Reason)
	}
	if got[1].Name != "basename" || got[1].Inactive {
		t.Fatalf("basename row = %#v, want active result", got[1])
	}
	if got[2].Name != "expr" || !got[2].Inactive {
		t.Fatalf("expr row = %#v, want inactive placeholder", got[2])
	}
	if got[2].Reason != "not currently covered by the compatibility manifest" {
		t.Fatalf("expr reason = %q", got[2].Reason)
	}
}

func TestLoadManifestIncludesExpandedCompatibilityCoverage(t *testing.T) {
	mf, err := loadManifest()
	if err != nil {
		t.Fatalf("loadManifest() error = %v", err)
	}

	gotPatterns := make(map[string][]string, len(mf.Utilities))
	for _, utility := range mf.Utilities {
		gotPatterns[utility.Name] = utility.Patterns
	}

	for name, want := range map[string][]string{
		"[":         {"tests/test/*", "tests/misc/invalid-opt.pl"},
		"arch":      {"tests/misc/arch.sh"},
		"basenc":    {"tests/basenc/basenc.pl", "tests/basenc/bounded-memory.sh", "tests/basenc/large-input.sh"},
		"cksum":     {"tests/cksum/cksum*"},
		"dd":        {"tests/dd/*"},
		"ginstall":  {"tests/install/*"},
		"head":      {"tests/head/*"},
		"md5sum":    {"tests/cksum/md5sum*"},
		"sha1sum":   {"tests/cksum/sha1sum*"},
		"sha224sum": {"tests/cksum/sha224sum*"},
		"stdbuf":    {"tests/misc/stdbuf.sh"},
		"stty":      {"tests/stty/*"},
		"truncate":  {"tests/truncate/*"},
	} {
		got, ok := gotPatterns[name]
		if !ok {
			t.Fatalf("manifest missing %q utility", name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s patterns = %#v, want %#v", name, got, want)
		}
	}

	for _, skip := range mf.SkipPatterns {
		if skip.Pattern == "tests/head/*" {
			t.Fatalf("manifest still contains head skip: %#v", skip)
		}
	}
}

func TestCombinedTestsForRunsDeduplicatesSharedTests(t *testing.T) {
	runs := []utilityRun{
		{
			Utility: utilityManifest{Name: "dir"},
			Tests:   []string{"tests/misc/invalid-opt.pl"},
		},
		{
			Utility: utilityManifest{Name: "link"},
			Tests:   []string{"tests/misc/invalid-opt.pl"},
		},
		{
			Utility: utilityManifest{Name: "test"},
			Tests:   []string{"tests/misc/invalid-opt.pl"},
		},
	}

	got := combinedTestsForRuns(runs)
	want := []string{"tests/misc/invalid-opt.pl"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("combinedTestsForRuns() = %#v, want %#v", got, want)
	}
}

func TestBuildBatchedUtilityResultsSharesLogAndSynthesizesExitCodes(t *testing.T) {
	runs := []utilityRun{
		{
			Utility: utilityManifest{Name: "basename", Patterns: []string{"tests/misc/basename*"}},
			Tests:   []string{"tests/misc/basename.pl"},
		},
		{
			Utility: utilityManifest{Name: "dirname", Patterns: []string{"tests/misc/dirname*"}},
			Tests:   []string{"tests/misc/dirname.pl"},
		},
	}

	got := buildBatchedUtilityResults(runs, makeCheckResult{
		ExitCode: 1,
		Output: []byte(`
PASS: tests/misc/basename.pl
FAIL: tests/misc/dirname.pl
`),
	}, "compat.log", "/tmp/compat.log")

	if len(got) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(got))
	}
	if !got[0].Passed || got[0].ExitCode != 0 {
		t.Fatalf("basename result = %#v, want passed with exit 0", got[0])
	}
	if got[0].LogFile != "compat.log" || got[0].LogPath != "/tmp/compat.log" {
		t.Fatalf("basename log = (%q, %q), want shared compat.log", got[0].LogFile, got[0].LogPath)
	}
	if got[1].Passed || got[1].ExitCode != 1 {
		t.Fatalf("dirname result = %#v, want failed with exit 1", got[1])
	}
	if got[1].Summary.Fail != 1 {
		t.Fatalf("dirname summary = %#v, want one failure", got[1].Summary)
	}
}

func TestBuildBatchedUtilityResultsReusesSharedManifestTestAcrossUtilities(t *testing.T) {
	runs := []utilityRun{
		{
			Utility: utilityManifest{Name: "dir", Patterns: []string{"tests/misc/invalid-opt.pl"}},
			Tests:   []string{"tests/misc/invalid-opt.pl"},
		},
		{
			Utility: utilityManifest{Name: "link", Patterns: []string{"tests/misc/invalid-opt.pl"}},
			Tests:   []string{"tests/misc/invalid-opt.pl"},
		},
		{
			Utility: utilityManifest{Name: "test", Patterns: []string{"tests/misc/invalid-opt.pl"}},
			Tests:   []string{"tests/misc/invalid-opt.pl"},
		},
	}

	got := buildBatchedUtilityResults(runs, makeCheckResult{
		ExitCode: 0,
		Output:   []byte("PASS: tests/misc/invalid-opt.pl\n"),
	}, "compat.log", "/tmp/compat.log")

	if len(got) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(got))
	}
	for _, result := range got {
		if !result.Passed || result.ExitCode != 0 {
			t.Fatalf("%s result = %#v, want passed with exit 0", result.Name, result)
		}
		if len(result.TestResults) != 1 || result.TestResults[0].Status != "pass" {
			t.Fatalf("%s test results = %#v, want shared pass", result.Name, result.TestResults)
		}
	}
}

func TestBuildBatchedUtilityResultsAttributesMatchingExtras(t *testing.T) {
	runs := []utilityRun{
		{
			Utility: utilityManifest{Name: "basename", Patterns: []string{"tests/misc/basename*"}},
			Tests:   []string{"tests/misc/basename.pl"},
		},
	}

	got := buildBatchedUtilityResults(runs, makeCheckResult{
		ExitCode: 1,
		Output: []byte(`
PASS: tests/misc/basename.pl
FAIL: tests/misc/basename-extra.pl
`),
	}, "compat.log", "/tmp/compat.log")

	if len(got) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(got))
	}
	if len(got[0].ExtraResults) != 1 || got[0].ExtraResults[0].Name != "tests/misc/basename-extra.pl" {
		t.Fatalf("extra results = %#v, want matched basename extra", got[0].ExtraResults)
	}
	if got[0].Summary.ReportedExtraTotal != 1 {
		t.Fatalf("reported extra total = %d, want 1", got[0].Summary.ReportedExtraTotal)
	}
}

func TestPrepareResultsDirCreatesParentAndRunDir(t *testing.T) {
	cacheDir := t.TempDir()

	resultsDir, err := prepareResultsDir(cacheDir, "")
	if err != nil {
		t.Fatalf("prepareResultsDir() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(cacheDir, "results")); err != nil {
		t.Fatalf("Stat(results root) error = %v", err)
	}
	info, err := os.Stat(resultsDir)
	if err != nil {
		t.Fatalf("Stat(results dir) error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("results dir %q is not a directory", resultsDir)
	}
}

func TestPrepareResultsDirExplicitClearsOldContents(t *testing.T) {
	cacheDir := t.TempDir()
	resultsDir := filepath.Join(t.TempDir(), "compat", "latest")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(resultsDir) error = %v", err)
	}
	stalePath := filepath.Join(resultsDir, "stale.txt")
	if err := os.WriteFile(stalePath, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(stalePath) error = %v", err)
	}

	gotDir, err := prepareResultsDir(cacheDir, resultsDir)
	if err != nil {
		t.Fatalf("prepareResultsDir(explicit) error = %v", err)
	}
	if gotDir != resultsDir {
		t.Fatalf("prepareResultsDir(explicit) = %q, want %q", gotDir, resultsDir)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale file still present, err = %v", err)
	}
}

func TestRunMakeCheckExportsConfigShell(t *testing.T) {
	workDir := t.TempDir()
	makeBin := filepath.Join(workDir, "fake-make.sh")
	envPath := filepath.Join(workDir, "config-shell.txt")
	resolverModePath := filepath.Join(workDir, "resolver-mode.txt")
	reservedPath := filepath.Join(workDir, "reserved-path.txt")
	logPath := filepath.Join(workDir, "make.log")
	hookDir := filepath.Join(workDir, "build-aux", "gbash-harness")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(hookDir) error = %v", err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$CONFIG_SHELL\" > " + shellSingleQuoteForScript(envPath) + "\n" +
		"printf '%s\\n' \"$GBASH_COMPAT_RESOLVER_MODE\" > " + shellSingleQuoteForScript(resolverModePath) + "\n" +
		"printf '%s\\n' \"$GBASH_COMPAT_RESERVED_COMMANDS_FILE\" > " + shellSingleQuoteForScript(reservedPath) + "\n" +
		"printf 'PASS: tests/misc/example.sh\\n'\n"
	if err := os.WriteFile(makeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake make) error = %v", err)
	}
	configShell := "/tmp/gbash-bash"

	result, err := runMakeCheck(context.Background(), makeBin, workDir, configShell, []string{"tests/misc/example.sh"}, logPath)
	if err != nil {
		t.Fatalf("runMakeCheck() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("runMakeCheck() exit = %d, want 0", result.ExitCode)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("ReadFile(config shell) error = %v", err)
	}
	if got := string(data); got != configShell+"\n" {
		t.Fatalf("CONFIG_SHELL = %q, want %q", got, configShell+"\\n")
	}
	if data, err := os.ReadFile(resolverModePath); err != nil {
		t.Fatalf("ReadFile(resolver mode) error = %v", err)
	} else if got := string(data); got != "registry-then-host-fallback\n" {
		t.Fatalf("GBASH_COMPAT_RESOLVER_MODE = %q, want %q", got, "registry-then-host-fallback\\n")
	}
	if data, err := os.ReadFile(reservedPath); err != nil {
		t.Fatalf("ReadFile(reserved path) error = %v", err)
	} else if got := string(data); got != filepath.Join(workDir, "build-aux", "gbash-harness", "gnu-programs.txt")+"\n" {
		t.Fatalf("GBASH_COMPAT_RESERVED_COMMANDS_FILE = %q", got)
	}
}

func TestPrepareProgramDirAddsCompatShellHelpers(t *testing.T) {
	workDir := t.TempDir()
	srcDir := filepath.Join(workDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(srcDir) error = %v", err)
	}
	gbashBin := filepath.Join(workDir, "gbash")
	if err := os.WriteFile(gbashBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(gbashBin) error = %v", err)
	}

	err := prepareProgramDir(workDir, gbashBin, []string{"sort", "expr"}, map[string]struct{}{
		"bash": {},
		"sh":   {},
		"sort": {},
	})
	if err != nil {
		t.Fatalf("prepareProgramDir() error = %v", err)
	}

	for _, name := range []string{"bash", "sh"} {
		path := filepath.Join(srcDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat(%q) error = %v", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%q is not a symlink", path)
		}
	}
	data, err := os.ReadFile(filepath.Join(workDir, "build-aux", "gbash-harness", "gnu-programs.txt"))
	if err != nil {
		t.Fatalf("ReadFile(gnu-programs.txt) error = %v", err)
	}
	if got := string(data); got != "expr\nsort\n" {
		t.Fatalf("gnu-programs.txt = %q, want sorted reserved program list", got)
	}
}

func TestImplementedGNUProgramSetIncludesHelperCommands(t *testing.T) {
	supported := implementedGNUProgramSet()
	for _, name := range []string{"base32", "base64", "expr"} {
		if _, ok := supported[name]; !ok {
			t.Fatalf("implementedGNUProgramSet() missing %q", name)
		}
	}
	if _, ok := supported["definitely-not-a-command"]; ok {
		t.Fatalf("implementedGNUProgramSet() unexpectedly included unknown command")
	}
}

func TestPrepareWorkDirPreservesFileTimes(t *testing.T) {
	cacheDir := t.TempDir()
	sourceDir := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(sourceDir) error = %v", err)
	}
	sourceFile := filepath.Join(sourceDir, "configure")
	if err := os.WriteFile(sourceFile, []byte("generated\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(sourceFile) error = %v", err)
	}
	wantModTime := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(sourceFile, wantModTime, wantModTime); err != nil {
		t.Fatalf("Chtimes(sourceFile) error = %v", err)
	}

	workDir, err := prepareWorkDir(cacheDir, "9.10", sourceDir)
	if err != nil {
		t.Fatalf("prepareWorkDir() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(workDir, "configure"))
	if err != nil {
		t.Fatalf("Stat(copied configure) error = %v", err)
	}
	if !info.ModTime().Equal(wantModTime) {
		t.Fatalf("copied mod time = %v, want %v", info.ModTime(), wantModTime)
	}
}

func TestPrepareWorkDirFromPreparedArchiveRelocatesPathsAndPreservesSymlink(t *testing.T) {
	cacheDir := t.TempDir()
	sourceDir := t.TempDir()
	originalWorkDir := "/tmp/coreutils-build"
	configStatus := filepath.Join(sourceDir, "config.status")
	configBody := "#!/bin/sh\nac_pwd='" + originalWorkDir + "'\n"
	if err := os.WriteFile(configStatus, []byte(configBody), 0o755); err != nil {
		t.Fatalf("WriteFile(config.status) error = %v", err)
	}
	makefilePath := filepath.Join(sourceDir, "Makefile")
	makefileBody := "abs_top_builddir = " + originalWorkDir + "\n"
	if err := os.WriteFile(makefilePath, []byte(makefileBody), 0o644); err != nil {
		t.Fatalf("WriteFile(Makefile) error = %v", err)
	}
	targetPath := filepath.Join(sourceDir, "target.txt")
	if err := os.WriteFile(targetPath, []byte("payload\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(target.txt) error = %v", err)
	}
	linkPath := filepath.Join(sourceDir, "link.txt")
	if err := os.Symlink("target.txt", linkPath); err != nil {
		t.Fatalf("Symlink(link.txt) error = %v", err)
	}
	archivePath := filepath.Join(t.TempDir(), "prepared.tar.gz")
	if err := archiveDirectoryAsTarGz(sourceDir, archivePath); err != nil {
		t.Fatalf("archiveDirectoryAsTarGz() error = %v", err)
	}

	workDir, err := prepareWorkDirFromPreparedArchive(context.Background(), cacheDir, "9.10", archivePath)
	if err != nil {
		t.Fatalf("prepareWorkDirFromPreparedArchive() error = %v", err)
	}

	makefileData, err := os.ReadFile(filepath.Join(workDir, "Makefile"))
	if err != nil {
		t.Fatalf("ReadFile(Makefile) error = %v", err)
	}
	if strings.Contains(string(makefileData), originalWorkDir) {
		t.Fatalf("Makefile still mentions original workdir %q", originalWorkDir)
	}
	if !strings.Contains(string(makefileData), workDir) {
		t.Fatalf("Makefile does not mention restored workdir %q", workDir)
	}
	target, err := os.Readlink(filepath.Join(workDir, "link.txt"))
	if err != nil {
		t.Fatalf("Readlink(link.txt) error = %v", err)
	}
	if target != "target.txt" {
		t.Fatalf("link target = %q, want %q", target, "target.txt")
	}
}

func TestExtractTarGzPreservesTarHeaderModTimes(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "coreutils.tar.gz")
	dest := t.TempDir()
	makefileAMTime := time.Date(2026, time.January, 19, 5, 16, 0, 0, time.UTC)
	makefileINTime := time.Date(2026, time.February, 4, 5, 19, 0, 0, time.UTC)

	if err := writeTarGz(t, archivePath, []tar.Header{
		{
			Name:     "coreutils-9.10/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
			ModTime:  makefileINTime,
		},
		{
			Name:     "coreutils-9.10/Makefile.am",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len("am\n")),
			ModTime:  makefileAMTime,
		},
		{
			Name:     "coreutils-9.10/Makefile.in",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len("in\n")),
			ModTime:  makefileINTime,
		},
	}, []string{"", "am\n", "in\n"}); err != nil {
		t.Fatalf("writeTarGz() error = %v", err)
	}

	if err := extractTarGz(archivePath, dest); err != nil {
		t.Fatalf("extractTarGz() error = %v", err)
	}

	amInfo, err := os.Stat(filepath.Join(dest, "coreutils-9.10", "Makefile.am"))
	if err != nil {
		t.Fatalf("Stat(Makefile.am) error = %v", err)
	}
	if !amInfo.ModTime().Equal(makefileAMTime) {
		t.Fatalf("Makefile.am mod time = %v, want %v", amInfo.ModTime(), makefileAMTime)
	}

	inInfo, err := os.Stat(filepath.Join(dest, "coreutils-9.10", "Makefile.in"))
	if err != nil {
		t.Fatalf("Stat(Makefile.in) error = %v", err)
	}
	if !inInfo.ModTime().Equal(makefileINTime) {
		t.Fatalf("Makefile.in mod time = %v, want %v", inInfo.ModTime(), makefileINTime)
	}
}

func TestParseOptionsAllowsPreparedArchiveWriterWithoutGBASHBin(t *testing.T) {
	argv := os.Args
	t.Cleanup(func() { os.Args = argv })
	os.Args = []string{"gbash-gnu", "--write-prepared-build-archive", "/tmp/prepared.tar.gz"}

	opts, err := parseOptions()
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if opts.writePreparedBuildArchive != "/tmp/prepared.tar.gz" {
		t.Fatalf("writePreparedBuildArchive = %q, want /tmp/prepared.tar.gz", opts.writePreparedBuildArchive)
	}
}

func TestParseOptionsRejectsConflictingPreparedArchiveFlags(t *testing.T) {
	argv := os.Args
	t.Cleanup(func() { os.Args = argv })
	os.Args = []string{"gbash-gnu", "--gbash-bin", "/tmp/gbash", "--prepared-build-archive", "/tmp/in.tar.gz", "--write-prepared-build-archive", "/tmp/out.tar.gz"}

	if _, err := parseOptions(); err == nil {
		t.Fatalf("parseOptions() error = nil, want conflict error")
	}
}

func TestSourceCacheCurrentUsesVersionStamp(t *testing.T) {
	sourceDir := t.TempDir()

	current, err := sourceCacheCurrent(sourceDir)
	if err != nil {
		t.Fatalf("sourceCacheCurrent() error = %v", err)
	}
	if current {
		t.Fatalf("sourceCacheCurrent() = true without version stamp")
	}

	if err := writeSourceCacheVersion(sourceDir); err != nil {
		t.Fatalf("writeSourceCacheVersion() error = %v", err)
	}

	current, err = sourceCacheCurrent(sourceDir)
	if err != nil {
		t.Fatalf("sourceCacheCurrent() after write error = %v", err)
	}
	if !current {
		t.Fatalf("sourceCacheCurrent() = false after version stamp write")
	}
}

func TestParseListDeduplicatesAndSplitsOnCommonSeparators(t *testing.T) {
	got := parseList("ls, cat\nls\tprintf  cat")
	want := []string{"ls", "cat", "printf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseList() = %#v, want %#v", got, want)
	}
}

func TestResolveUtilityTestsAppliesPatternAndSkipFilters(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "tests/cat/basic.sh", "echo ok\n")
	writeTestFile(t, root, "tests/cat/tty.sh", "require_controlling_input_terminal\n")
	writeTestFile(t, root, "tests/help/help-version.sh", "echo skip\n")

	tests, skipped, err := resolveUtilityTests(root, utilityManifest{
		Name:     "cat",
		Patterns: []string{"tests/cat/*", "tests/help/*"},
	}, []skipPattern{{Pattern: "tests/help/*", Reason: "help/version tests are skipped in v1"}}, nil)
	if err != nil {
		t.Fatalf("resolveUtilityTests() error = %v", err)
	}

	if got, want := tests, []string{"tests/cat/basic.sh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tests = %#v, want %#v", got, want)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %#v, want two skipped entries", skipped)
	}
}

func TestResolveUtilityTestsSkipsGeneratedArtifacts(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "tests/seq/seq-precision.sh", "#!/bin/sh\n")
	writeTestFile(t, root, "tests/seq/seq-precision.log", "generated\n")
	writeTestFile(t, root, "tests/seq/seq-precision.trs", "generated\n")

	tests, skipped, err := resolveUtilityTests(root, utilityManifest{
		Name:     "seq",
		Patterns: []string{"tests/seq/*"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("resolveUtilityTests() error = %v", err)
	}
	if got, want := tests, []string{"tests/seq/seq-precision.sh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tests = %#v, want %#v", got, want)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want no skipped entries", skipped)
	}
}

func writeTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	mode := os.FileMode(0o644)
	switch filepath.Ext(path) {
	case ".sh", ".pl", ".xpl":
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func writeTarGz(t *testing.T, path string, headers []tar.Header, contents []string) error {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	gzw := gzip.NewWriter(file)
	defer func() { _ = gzw.Close() }()

	tw := tar.NewWriter(gzw)
	defer func() { _ = tw.Close() }()

	for i := range headers {
		hdr := headers[i]
		if err := tw.WriteHeader(&hdr); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(contents[i])); err != nil {
				return err
			}
		}
	}
	return nil
}
