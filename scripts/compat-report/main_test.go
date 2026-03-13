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
		Suite: suiteSummary{
			SelectedTotal:   4,
			FilteredTotal:   1,
			Pass:            1,
			Fail:            1,
			Skip:            1,
			RunnableTotal:   2,
			PassPctSelected: 25,
			PassPctRunnable: 50,
			Tests: []suiteTest{
				{Path: "tests/misc/basename.pl", Category: "misc", Status: "pass", Attributions: []testAttribution{{Command: "basename", Kind: "direct"}}},
				{Path: "tests/cat/cat-self.sh", Category: "cat", Status: "skip", Attributions: []testAttribution{{Command: "cat", Kind: "direct"}}},
				{Path: "tests/misc/dirname.pl", Category: "misc", Status: "fail", Attributions: []testAttribution{{Command: "dirname", Kind: "direct"}}},
				{Path: "tests/help/help-version.sh", Category: "help", Status: "filtered", Filtered: true, FilterReason: "help/version tests are out of scope"},
			},
		},
		Categories: []categoryResult{
			{
				Name:    "misc",
				Summary: coverageBucket{SelectedTotal: 2, Pass: 1, Fail: 1, RunnableTotal: 2, PassPctSelected: 50, PassPctRunnable: 50},
				Tests: []suiteTest{
					{Path: "tests/misc/basename.pl", Category: "misc", Status: "pass", Attributions: []testAttribution{{Command: "basename", Kind: "direct"}}},
					{Path: "tests/misc/dirname.pl", Category: "misc", Status: "fail", Attributions: []testAttribution{{Command: "dirname", Kind: "direct"}}},
				},
			},
			{
				Name:    "cat",
				Summary: coverageBucket{SelectedTotal: 1, Skip: 1, RunnableTotal: 0, PassPctSelected: 0, PassPctRunnable: 0},
				Tests: []suiteTest{
					{Path: "tests/cat/cat-self.sh", Category: "cat", Status: "skip", Attributions: []testAttribution{{Command: "cat", Kind: "direct"}}},
				},
			},
			{
				Name:    "help",
				Summary: coverageBucket{SelectedTotal: 1, FilteredTotal: 1, PassPctSelected: 0, PassPctRunnable: 0},
				Tests: []suiteTest{
					{Path: "tests/help/help-version.sh", Category: "help", Status: "filtered", Filtered: true, FilterReason: "help/version tests are out of scope"},
				},
			},
		},
		Commands: []commandCoverage{
			{
				Name:          "basename",
				CoverageState: "primary",
				Primary:       coverageBucket{SelectedTotal: 1, Pass: 1, RunnableTotal: 1, PassPctSelected: 100, PassPctRunnable: 100},
				Tests:         []commandTestRef{{Path: "tests/misc/basename.pl", Status: "pass", Kind: "direct"}},
			},
			{
				Name:          "cat",
				CoverageState: "primary",
				Primary:       coverageBucket{SelectedTotal: 1, Skip: 1, RunnableTotal: 0, PassPctSelected: 0, PassPctRunnable: 0},
				Tests:         []commandTestRef{{Path: "tests/cat/cat-self.sh", Status: "skip", Kind: "direct"}},
			},
			{
				Name:          "dirname",
				CoverageState: "primary",
				Primary:       coverageBucket{SelectedTotal: 1, Fail: 1, RunnableTotal: 1, PassPctSelected: 0, PassPctRunnable: 0},
				Tests:         []commandTestRef{{Path: "tests/misc/dirname.pl", Status: "fail", Kind: "direct"}},
			},
			{
				Name:          "yes",
				CoverageState: "empty",
				Primary:       coverageBucket{},
				Shared:        coverageBucket{},
			},
		},
		Coverage: coverageDebtSummary{
			CommandTotal:           4,
			PrimaryCoveredCommands: 3,
			PrimaryPassingCommands: 1,
			EmptyCommands:          1,
			PrimaryPassPct:         33.33,
			UnmappedSelectedTests:  1,
			UnmappedRunnableTests:  0,
			MultiDirectTests:       0,
			ExtraReportedTotal:     0,
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
		"GNU Test Coverage",
		"In-Scope Test Pass",
		"Coverage per category",
		"summary.json",
		"tests/misc/dirname.pl",
		"1 / 0 / 1",
		"33.33%",
		"1 out of scope",
		"out of scope",
		"help/version tests are out of scope",
	} {
		if !strings.Contains(index, needle) {
			t.Fatalf("index.html missing %q", needle)
		}
	}
	for _, needle := range []string{
		"Commands",
		"Primary Command Pass",
		"Coverage Debt",
		"shared-only",
		"no attributed coverage",
	} {
		if strings.Contains(index, needle) {
			t.Fatalf("index.html unexpectedly contains %q", needle)
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
  "suite": { "selected_total": 1, "filtered_total": 0, "pass": 1, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 1, "pass_pct_selected": 100, "pass_pct_runnable": 100, "tests": [ { "path": "tests/misc/basename.pl", "category": "misc", "status": "pass", "attributions": [ { "command": "basename", "kind": "direct" } ] } ] },
  "categories": [ { "name": "misc", "summary": { "selected_total": 1, "filtered_total": 0, "pass": 1, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 1, "pass_pct_selected": 100, "pass_pct_runnable": 100 }, "tests": [ { "path": "tests/misc/basename.pl", "category": "misc", "status": "pass", "attributions": [ { "command": "basename", "kind": "direct" } ] } ] } ],
  "commands": [ { "name": "basename", "coverage_state": "primary", "primary": { "selected_total": 1, "filtered_total": 0, "pass": 1, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 1, "pass_pct_selected": 100, "pass_pct_runnable": 100 }, "shared": { "selected_total": 0, "filtered_total": 0, "pass": 0, "fail": 0, "skip": 0, "xfail": 0, "xpass": 0, "error": 0, "unreported": 0, "runnable_total": 0, "pass_pct_selected": 0, "pass_pct_runnable": 0 }, "tests": [ { "path": "tests/misc/basename.pl", "status": "pass", "kind": "direct" } ] } ],
  "coverage": { "command_total": 1, "primary_covered_commands": 1, "primary_passing_commands": 1, "shared_only_commands": 0, "filtered_only_commands": 0, "empty_commands": 0, "primary_pass_pct": 100, "unmapped_selected_tests": 0, "unmapped_runnable_tests": 0, "multi_direct_tests": 0, "extra_reported_total": 0 },
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
