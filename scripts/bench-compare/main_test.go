package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateWorkspaceFixture(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	summary, err := createWorkspaceFixture(root)
	if err != nil {
		t.Fatalf("createWorkspaceFixture() error = %v", err)
	}
	if summary.FileCount != 300 {
		t.Fatalf("FileCount = %d, want 300", summary.FileCount)
	}

	var countedFiles int
	var countedBytes int64
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		countedFiles++
		countedBytes += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
	if countedFiles != summary.FileCount {
		t.Fatalf("walked files = %d, want %d", countedFiles, summary.FileCount)
	}
	if countedBytes != summary.TotalBytes {
		t.Fatalf("walked bytes = %d, want %d", countedBytes, summary.TotalBytes)
	}
}

func TestSummarizeDurations(t *testing.T) {
	stats, ok := summarizeDurations([]time.Duration{
		7 * time.Millisecond,
		1 * time.Millisecond,
		5 * time.Millisecond,
		9 * time.Millisecond,
		3 * time.Millisecond,
	})
	if !ok {
		t.Fatalf("summarizeDurations() ok = false, want true")
	}
	if got, want := time.Duration(stats.MinNanos), 1*time.Millisecond; got != want {
		t.Fatalf("Min = %s, want %s", got, want)
	}
	if got, want := time.Duration(stats.MedianNanos), 5*time.Millisecond; got != want {
		t.Fatalf("Median = %s, want %s", got, want)
	}
	if got, want := time.Duration(stats.P95Nanos), 9*time.Millisecond; got != want {
		t.Fatalf("P95 = %s, want %s", got, want)
	}
}

func TestRenderTextReportAndJSON(t *testing.T) {
	report := benchmarkReport{
		GeneratedAt:  "2026-03-13T00:00:00Z",
		Runs:         2,
		JustBashSpec: "just-bash@2.13.0",
		Scenarios: []scenarioReport{
			{
				Name:           "startup_echo",
				Description:    "Cold process start plus one simple command.",
				Command:        "echo benchmark",
				ExpectedStdout: "benchmark\n",
				Results: []runtimeReport{
					{
						Name:         "gbash",
						SuccessCount: 2,
						Stats: &latencyStats{
							MinNanos:    int64(time.Millisecond),
							MedianNanos: int64(2 * time.Millisecond),
							P95Nanos:    int64(3 * time.Millisecond),
						},
						Trials: []trialResult{
							{Index: 1, DurationNanos: int64(time.Millisecond), ExitCode: 0, Success: true, Stdout: "benchmark\n"},
							{Index: 2, DurationNanos: int64(3 * time.Millisecond), ExitCode: 0, Success: true, Stdout: "benchmark\n"},
						},
					},
				},
			},
		},
	}

	rendered := renderTextReport(report)
	if !strings.Contains(rendered, "[startup_echo]") {
		t.Fatalf("renderTextReport() missing scenario header:\n%s", rendered)
	}
	if !strings.Contains(rendered, "gbash: 2/2 successful") {
		t.Fatalf("renderTextReport() missing runtime summary:\n%s", rendered)
	}
	if !strings.Contains(rendered, "median=2ms") {
		t.Fatalf("renderTextReport() missing latency stats:\n%s", rendered)
	}

	jsonPath := filepath.Join(t.TempDir(), "report.json")
	if err := writeJSONReport(jsonPath, report); err != nil {
		t.Fatalf("writeJSONReport() error = %v", err)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "\"startup_echo\"") {
		t.Fatalf("JSON output missing scenario: %s", string(data))
	}
	if !strings.Contains(string(data), "\"just_bash_spec\": \"just-bash@2.13.0\"") {
		t.Fatalf("JSON output missing just-bash spec: %s", string(data))
	}
}
