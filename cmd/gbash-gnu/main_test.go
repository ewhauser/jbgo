package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseReportedTestResultsNormalizesAliasesAndDeduplicates(t *testing.T) {
	t.Parallel()
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

func TestSummariesComputePercentagesAndRollups(t *testing.T) {
	t.Parallel()
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

	overall := summarizeOverall([]utilityResult{first, second})
	if got, want := overall.PassPctSelected, 40.0; got != want {
		t.Fatalf("overall pass_pct_selected = %v, want %v", got, want)
	}
	if got, want := overall.PassPctRunnable, 66.67; got != want {
		t.Fatalf("overall pass_pct_runnable = %v, want %v", got, want)
	}

	totals := summarizeUtilityTotals([]utilityResult{first, second})
	if got, want := totals.Total, 2; got != want {
		t.Fatalf("utility total = %d, want %d", got, want)
	}
	if got, want := totals.Passed, 1; got != want {
		t.Fatalf("utility passed = %d, want %d", got, want)
	}
	if got, want := totals.Failed, 1; got != want {
		t.Fatalf("utility failed = %d, want %d", got, want)
	}
}

func TestLoadManifestIncludesExpandedCompatibilityCoverage(t *testing.T) {
	t.Parallel()
	mf, err := loadManifest()
	if err != nil {
		t.Fatalf("loadManifest() error = %v", err)
	}

	if mf.GNUVersion != "9.10" {
		t.Fatalf("gnu_version = %q, want 9.10", mf.GNUVersion)
	}
	if len(mf.UtilityOverrides) == 0 {
		t.Fatalf("manifest utility_overrides = %#v, want entries", mf.UtilityOverrides)
	}
	if len(mf.AttributionRules) == 0 {
		t.Fatalf("manifest attribution_rules = %#v, want entries", mf.AttributionRules)
	}
}

func TestBuildBatchedUtilityResultsSharesLogAndSynthesizesExitCodes(t *testing.T) {
	t.Parallel()
	runs := []utilityRun{
		{
			Utility: attributedUtility{Name: "basename", Patterns: []string{"tests/misc/basename*"}},
			Tests:   []string{"tests/misc/basename.pl"},
		},
		{
			Utility: attributedUtility{Name: "dirname", Patterns: []string{"tests/misc/dirname*"}},
			Tests:   []string{"tests/misc/dirname.pl"},
		},
	}

	combinedResults, combinedExtras := parseReportedTestResults([]byte(`
PASS: tests/misc/basename.pl
FAIL: tests/misc/dirname.pl
`), []string{"tests/misc/basename.pl", "tests/misc/dirname.pl"})
	got, overall := buildBatchedUtilityResults(runs, 0, combinedResults, combinedExtras, makeCheckResult{
		ExitCode: 1,
		Output:   []byte(""),
	}, "compat.log", "/tmp/compat.log")

	if len(got) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(got))
	}
	if !got[0].Passed || got[0].ExitCode != 0 {
		t.Fatalf("basename result = %#v, want passed with exit 0", got[0])
	}
	if got[1].Passed || got[1].ExitCode != 1 {
		t.Fatalf("dirname result = %#v, want failed with exit 1", got[1])
	}
	if overall.Fail != 1 || overall.Pass != 1 {
		t.Fatalf("overall summary = %#v, want one pass and one fail", overall)
	}
}

func TestBuildRunPlanFiltersUtilitySelection(t *testing.T) {
	t.Parallel()
	workDir := makeMinimalGNUWorkdir(t)
	mf, err := loadManifest()
	if err != nil {
		t.Fatalf("loadManifest() error = %v", err)
	}

	plan, err := buildRunPlan(context.Background(), mf, workDir, &options{
		workDir: workDir,
		utils:   "basename",
	})
	if err != nil {
		t.Fatalf("buildRunPlan() error = %v", err)
	}

	if got, want := plan.selectedTests, []string{"tests/misc/basename.pl"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selectedTests = %#v, want %#v", got, want)
	}
	if len(plan.runs) != 1 || plan.runs[0].Utility.Name != "basename" {
		t.Fatalf("runs = %#v, want basename only", plan.runs)
	}
}

func TestRunWritesSummaryFromSharedLog(t *testing.T) {
	t.Parallel()
	workDir := makeMinimalGNUWorkdir(t)
	resultsDir := filepath.Join(t.TempDir(), "results")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(resultsDir) error = %v", err)
	}
	logPath := filepath.Join(resultsDir, "compat.log")
	if err := os.WriteFile(logPath, []byte("PASS: tests/misc/basename.pl\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(logPath) error = %v", err)
	}

	mf, err := loadManifest()
	if err != nil {
		t.Fatalf("loadManifest() error = %v", err)
	}
	if err := run(context.Background(), mf, &options{
		workDir:    workDir,
		utils:      "basename",
		resultsDir: resultsDir,
		logPath:    logPath,
	}); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	summaryPath := filepath.Join(resultsDir, "summary.json")
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("ReadFile(summary.json) error = %v", err)
	}
	var summary runSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("Unmarshal(summary.json) error = %v", err)
	}
	if got, want := summary.Overall.Pass, 1; got != want {
		t.Fatalf("overall pass = %d, want %d", got, want)
	}
	if got, want := len(summary.Utilities), 1; got != want {
		t.Fatalf("len(utilities) = %d, want %d", got, want)
	}
}

func makeMinimalGNUWorkdir(t *testing.T) string {
	t.Helper()

	workDir := t.TempDir()
	scriptPath := filepath.Join(workDir, "build-aux", "gen-lists-of-programs.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(build-aux) error = %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'basename dirname\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(gen-lists-of-programs.sh) error = %v", err)
	}

	localMKPath := filepath.Join(workDir, "tests", "local.mk")
	if err := os.MkdirAll(filepath.Dir(localMKPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(tests) error = %v", err)
	}
	localMK := "TESTS = \\\n  tests/misc/basename.pl \\\n  tests/misc/dirname.pl\n"
	if err := os.WriteFile(localMKPath, []byte(localMK), 0o644); err != nil {
		t.Fatalf("WriteFile(local.mk) error = %v", err)
	}

	for _, rel := range []string{
		"tests/misc/basename.pl",
		"tests/misc/dirname.pl",
	} {
		path := filepath.Join(workDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	return workDir
}
