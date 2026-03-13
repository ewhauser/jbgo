package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReportWritesIndexAndBadge(t *testing.T) {
	outputDir := t.TempDir()
	summary := runSummary{
		GNUVersion:  "9.10",
		GeneratedAt: "2026-03-11T18:30:00Z",
		WorkDir:     "/tmp/coreutils-work",
		ResultsDir:  "/tmp/compat-results",
		Overall: testSummary{
			SelectedTotal:   3,
			Pass:            1,
			Fail:            1,
			RunnableTotal:   2,
			Skip:            1,
			PassPctSelected: 33.33,
			PassPctRunnable: 50,
		},
		UtilitySummary: utilityTotals{
			Total:           3,
			Passed:          1,
			Failed:          1,
			NoRunnableTests: 1,
			PassPctTotal:    33.33,
			PassPctRunnable: 50,
		},
		Utilities: []utilityResult{
			{
				Name:    "basename",
				LogFile: "basename.log",
				Summary: testSummary{SelectedTotal: 1, Pass: 1, RunnableTotal: 1, PassPctSelected: 100, PassPctRunnable: 100},
			},
			{
				Name:    "cat",
				LogFile: "cat.log",
				Summary: testSummary{SelectedTotal: 1, Skip: 1, RunnableTotal: 0, PassPctSelected: 0, PassPctRunnable: 0},
			},
			{
				Name: "dirname",
				Summary: testSummary{
					SelectedTotal:   1,
					Fail:            1,
					RunnableTotal:   1,
					PassPctSelected: 0,
					PassPctRunnable: 0,
				},
			},
		},
	}

	if err := writeReport(outputDir, &summary); err != nil {
		t.Fatalf("writeReport() error = %v", err)
	}

	indexData, err := os.ReadFile(filepath.Join(outputDir, "index.html"))
	if err != nil {
		t.Fatalf("ReadFile(index.html) error = %v", err)
	}
	index := string(indexData)
	for _, needle := range []string{
		"GNU Coreutils Compatibility",
		"Selected Test Pass",
		"Runnable Command Pass",
		"summary.json",
		"basename.log",
		"dirname",
		"cat.log",
		"all selected tests skipped",
		"1 passed, 1 failed, 1 skip-only, 0 empty",
		"n/a",
		"33.33%",
		"50%",
	} {
		if !strings.Contains(index, needle) {
			t.Fatalf("index.html missing %q", needle)
		}
	}

	badgeData, err := os.ReadFile(filepath.Join(outputDir, "badge.svg"))
	if err != nil {
		t.Fatalf("ReadFile(badge.svg) error = %v", err)
	}
	badge := string(badgeData)
	if !strings.Contains(badge, "compat") || !strings.Contains(badge, "33.33%") {
		t.Fatalf("badge.svg content = %q, want compat and 33.33%%", badge)
	}
}

func TestLoadSummaryReadsHarnessJSON(t *testing.T) {
	summaryPath := filepath.Join(t.TempDir(), "summary.json")
	if err := os.WriteFile(summaryPath, []byte(`{
  "gnu_version": "9.10",
  "generated_at": "2026-03-11T18:30:00Z",
  "work_dir": "/tmp/coreutils-work",
  "results_dir": "/tmp/compat-results",
  "overall": { "selected_total": 1, "pass": 1, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 1, "pass_pct_selected": 100, "pass_pct_runnable": 100 },
  "utility_summary": { "total": 1, "passed": 1, "failed": 0, "no_runnable_tests": 0, "pass_pct_total": 100, "pass_pct_runnable": 100 },
  "utilities": [
    { "name": "basename", "tests": ["tests/misc/basename.pl"], "test_results": [{ "name": "tests/misc/basename.pl", "status": "pass", "reported_as": ["tests/misc/basename.pl"] }], "summary": { "selected_total": 1, "pass": 1, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 1, "pass_pct_selected": 100, "pass_pct_runnable": 100 }, "exit_code": 0, "passed": true, "log_file": "basename.log", "log_path": "/tmp/compat-results/basename.log" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("WriteFile(summary.json) error = %v", err)
	}

	summary, err := loadSummary(summaryPath)
	if err != nil {
		t.Fatalf("loadSummary() error = %v", err)
	}
	if summary.GNUVersion != "9.10" {
		t.Fatalf("GNUVersion = %q, want 9.10", summary.GNUVersion)
	}
	if summary.WorkDir != "/tmp/coreutils-work" || summary.ResultsDir != "/tmp/compat-results" {
		t.Fatalf("work/results dirs = (%q, %q), want current harness paths", summary.WorkDir, summary.ResultsDir)
	}
	if len(summary.Utilities) != 1 || summary.Utilities[0].Name != "basename" {
		t.Fatalf("utilities = %#v, want one basename utility", summary.Utilities)
	}
}

func TestLoadSummaryRejectsUnknownFields(t *testing.T) {
	summaryPath := filepath.Join(t.TempDir(), "summary.json")
	if err := os.WriteFile(summaryPath, []byte(`{
  "gnu_version": "9.10",
  "generated_at": "2026-03-11T18:30:00Z",
  "work_dir": "/tmp/coreutils-work",
  "results_dir": "/tmp/compat-results",
  "overall": { "selected_total": 0, "pass": 0, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 0, "pass_pct_selected": 0, "pass_pct_runnable": 0 },
  "utility_summary": { "total": 0, "passed": 0, "failed": 0, "no_runnable_tests": 0, "pass_pct_total": 0, "pass_pct_runnable": 0 },
  "utilities": [
    { "name": "basename", "summary": { "selected_total": 0, "pass": 0, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 0, "pass_pct_selected": 0, "pass_pct_runnable": 0 }, "exit_code": 0, "passed": false, "unexpected": true }
  ]
}`), 0o644); err != nil {
		t.Fatalf("WriteFile(summary.json) error = %v", err)
	}

	if _, err := loadSummary(summaryPath); err == nil {
		t.Fatalf("loadSummary() error = nil, want unknown-field failure")
	}
}
