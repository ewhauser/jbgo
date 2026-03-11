package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
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

	if got, want := first.Summary.PassPctSelected, 25.0; got != want {
		t.Fatalf("first pass_pct_selected = %v, want %v", got, want)
	}
	if got, want := first.Summary.PassPctRunnable, 50.0; got != want {
		t.Fatalf("first pass_pct_runnable = %v, want %v", got, want)
	}

	overall := summarizeOverall([]utilityResult{first, second, third})
	if got, want := overall.SelectedTotal, 5; got != want {
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
	if got, want := overall.Skip, 1; got != want {
		t.Fatalf("overall skip = %d, want %d", got, want)
	}
	if got, want := overall.Unreported, 1; got != want {
		t.Fatalf("overall unreported = %d, want %d", got, want)
	}
	if got, want := overall.RunnableTotal, 3; got != want {
		t.Fatalf("overall runnable_total = %d, want %d", got, want)
	}
	if got, want := overall.PassPctSelected, 40.0; got != want {
		t.Fatalf("overall pass_pct_selected = %v, want %v", got, want)
	}
	if got, want := overall.PassPctRunnable, 66.67; got != want {
		t.Fatalf("overall pass_pct_runnable = %v, want %v", got, want)
	}

	totals := summarizeUtilityTotals([]utilityResult{first, second, third})
	if got, want := totals.Total, 3; got != want {
		t.Fatalf("utility total = %d, want %d", got, want)
	}
	if got, want := totals.Passed, 1; got != want {
		t.Fatalf("utility passed = %d, want %d", got, want)
	}
	if got, want := totals.Failed, 1; got != want {
		t.Fatalf("utility failed = %d, want %d", got, want)
	}
	if got, want := totals.NoRunnableTests, 1; got != want {
		t.Fatalf("utility no_runnable_tests = %d, want %d", got, want)
	}
	if got, want := totals.PassPctTotal, 33.33; got != want {
		t.Fatalf("utility pass_pct_total = %v, want %v", got, want)
	}
	if got, want := totals.PassPctRunnable, 50.0; got != want {
		t.Fatalf("utility pass_pct_runnable = %v, want %v", got, want)
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

func writeTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
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
