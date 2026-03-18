package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDecodeManifestNormalizesExpectedFailures(t *testing.T) {
	t.Parallel()

	mf, err := decodeManifest([]byte(`{
  "expected_failures": {
    " tests\\misc\\dirname.pl ": {
      "reason": "dirname still diverges from GNU",
      "goos": [" ` + strings.ToUpper(runtime.GOOS) + ` "]
    }
  }
}`))
	if err != nil {
		t.Fatalf("decodeManifest() error = %v", err)
	}

	entry, ok := lookupExpectedFailure(mf, "tests/misc/dirname.pl")
	if !ok {
		t.Fatalf("lookupExpectedFailure() = false, want true")
	}
	if got, want := entry.Reason, "dirname still diverges from GNU"; got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
	if got, want := entry.GOOS, []string{runtime.GOOS}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("goos = %#v, want %#v", got, want)
	}
}

func TestDecodeManifestRejectsEmptyExpectedFailureReason(t *testing.T) {
	t.Parallel()

	_, err := decodeManifest([]byte(`{
  "expected_failures": {
    "tests/misc/dirname.pl": { "reason": "   " }
  }
}`))
	if err == nil || !strings.Contains(err.Error(), "expected failure reason must not be empty") {
		t.Fatalf("decodeManifest() error = %v, want expected failure reason validation", err)
	}
}

func TestApplyExpectedFailuresReclassifiesResults(t *testing.T) {
	t.Parallel()

	results := applyExpectedFailures([]testResult{
		{Name: "tests/misc/basename.pl", Status: "pass"},
		{Name: "tests/misc/dirname.pl", Status: "fail"},
		{Name: "tests/misc/echo.sh", Status: "skip"},
		{Name: "tests/misc/nohup.sh", Status: "unreported"},
	}, &manifest{
		ExpectedFailures: map[string]xfailEntry{
			"tests/misc/basename.pl": {Reason: "basename still mismatches GNU"},
			"tests/misc/dirname.pl":  {Reason: "dirname still mismatches GNU"},
			"tests/misc/nohup.sh":    {Reason: "nohup harness still does not report"},
		},
	})

	if got, want := results[0].Status, "xpass"; got != want {
		t.Fatalf("basename status = %q, want %q", got, want)
	}
	if got, want := results[1].Status, "xfail"; got != want {
		t.Fatalf("dirname status = %q, want %q", got, want)
	}
	if got, want := results[2].Status, "skip"; got != want {
		t.Fatalf("echo status = %q, want %q", got, want)
	}
	if got, want := results[3].Status, "unreported"; got != want {
		t.Fatalf("nohup status = %q, want %q", got, want)
	}
	if got := results[3].ExpectedFailureReason; got == "" {
		t.Fatalf("nohup expected failure reason = %q, want non-empty reason", got)
	}
}

func TestRunReturnsUnexpectedPassForManifestExpectedFailure(t *testing.T) {
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

	reason := "basename still mismatches GNU output formatting"
	mf, err := loadManifest()
	if err != nil {
		t.Fatalf("loadManifest() error = %v", err)
	}
	mf.ExpectedFailures["tests/misc/basename.pl"] = xfailEntry{Reason: reason}

	err = run(context.Background(), mf, &options{
		workDir:    workDir,
		utils:      "basename",
		resultsDir: resultsDir,
		logPath:    logPath,
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected pass for expected failure tests/misc/basename.pl") || !strings.Contains(err.Error(), reason) {
		t.Fatalf("run() error = %v, want unexpected-pass manifest failure", err)
	}

	data, readErr := os.ReadFile(filepath.Join(resultsDir, "summary.json"))
	if readErr != nil {
		t.Fatalf("ReadFile(summary.json) error = %v", readErr)
	}
	var summary runSummary
	if unmarshalErr := json.Unmarshal(data, &summary); unmarshalErr != nil {
		t.Fatalf("Unmarshal(summary.json) error = %v", unmarshalErr)
	}
	if got, want := summary.Utilities[0].TestResults[0].Status, "xpass"; got != want {
		t.Fatalf("utility test status = %q, want %q", got, want)
	}
	if got, want := summary.Utilities[0].TestResults[0].ExpectedFailureReason, reason; got != want {
		t.Fatalf("utility expected failure reason = %q, want %q", got, want)
	}
	if got, want := summary.Suite.Tests[0].ExpectedFailureReason, reason; got != want {
		t.Fatalf("suite expected failure reason = %q, want %q", got, want)
	}
}
