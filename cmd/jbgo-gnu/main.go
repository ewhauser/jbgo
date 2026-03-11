package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	jbcommands "github.com/ewhauser/jbgo/commands"
	"github.com/ewhauser/jbgo/internal/compatshims"
)

//go:embed manifest.json
var manifestFS embed.FS

type manifest struct {
	GNUVersion    string            `json:"gnu_version"`
	TarballURL    string            `json:"tarball_url"`
	TarballSHA256 string            `json:"tarball_sha256"`
	Utilities     []utilityManifest `json:"utilities"`
	SkipPatterns  []skipPattern     `json:"skip_patterns"`
}

type utilityManifest struct {
	Name     string   `json:"name"`
	Patterns []string `json:"patterns"`
	Skips    []string `json:"skips,omitempty"`
}

type skipPattern struct {
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

type options struct {
	cacheDir    string
	jbgoBin     string
	utils       string
	tests       string
	resultsDir  string
	setupOnly   bool
	keepWorkdir bool
}

type testResult struct {
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	ReportedAs []string `json:"reported_as,omitempty"`
}

type testSummary struct {
	SelectedTotal      int     `json:"selected_total"`
	FilteredSkipTotal  int     `json:"filtered_skip_total,omitempty"`
	ReportedExtraTotal int     `json:"reported_extra_total,omitempty"`
	Pass               int     `json:"pass"`
	Fail               int     `json:"fail"`
	Skip               int     `json:"skip"`
	XFail              int     `json:"xfail"`
	XPass              int     `json:"xpass"`
	Error              int     `json:"error"`
	Unreported         int     `json:"unreported"`
	RunnableTotal      int     `json:"runnable_total"`
	PassPctSelected    float64 `json:"pass_pct_selected"`
	PassPctRunnable    float64 `json:"pass_pct_runnable"`
}

type utilityResult struct {
	Name         string       `json:"name"`
	Inactive     bool         `json:"inactive,omitempty"`
	Tests        []string     `json:"tests"`
	Skipped      []string     `json:"skipped,omitempty"`
	TestResults  []testResult `json:"test_results,omitempty"`
	ExtraResults []testResult `json:"extra_results,omitempty"`
	Summary      testSummary  `json:"summary"`
	ExitCode     int          `json:"exit_code"`
	Passed       bool         `json:"passed"`
	LogFile      string       `json:"log_file,omitempty"`
	LogPath      string       `json:"log_path,omitempty"`
	Reason       string       `json:"reason,omitempty"`
}

type utilityTotals struct {
	Total           int     `json:"total"`
	Passed          int     `json:"passed"`
	Failed          int     `json:"failed"`
	NoRunnableTests int     `json:"no_runnable_tests"`
	PassPctTotal    float64 `json:"pass_pct_total"`
	PassPctRunnable float64 `json:"pass_pct_runnable"`
}

type runSummary struct {
	GNUVersion     string          `json:"gnu_version"`
	GeneratedAt    string          `json:"generated_at"`
	WorkDir        string          `json:"work_dir"`
	ResultsDir     string          `json:"results_dir"`
	Overall        testSummary     `json:"overall"`
	UtilitySummary utilityTotals   `json:"utility_summary"`
	Utilities      []utilityResult `json:"utilities"`
}

type makeCheckResult struct {
	ExitCode int
	Output   []byte
}

const sourceCacheVersion = "2"

func main() {
	ctx := context.Background()
	opts, err := parseOptions()
	if err != nil {
		fatalf("parse options: %v", err)
	}
	manifest, err := loadManifest()
	if err != nil {
		fatalf("load manifest: %v", err)
	}
	if err := run(ctx, manifest, &opts); err != nil {
		fatalf("%v", err)
	}
}

func parseOptions() (options, error) {
	var opts options
	fs := flag.NewFlagSet("jbgo-gnu", flag.ContinueOnError)
	fs.StringVar(&opts.cacheDir, "cache-dir", ".cache/gnu", "cache directory for GNU sources and results")
	fs.StringVar(&opts.jbgoBin, "jbgo-bin", "", "path to the jbgo binary under test")
	fs.StringVar(&opts.utils, "utils", strings.TrimSpace(os.Getenv("GNU_UTILS")), "comma or space separated utility list")
	fs.StringVar(&opts.tests, "tests", strings.TrimSpace(os.Getenv("GNU_TESTS")), "comma or newline separated explicit GNU test files")
	fs.StringVar(&opts.resultsDir, "results-dir", strings.TrimSpace(os.Getenv("GNU_RESULTS_DIR")), "directory to write summary.json, logs, and published report assets")
	fs.BoolVar(&opts.setupOnly, "setup", false, "download and extract the pinned GNU source tree, then exit")
	fs.BoolVar(&opts.keepWorkdir, "keep-workdir", os.Getenv("GNU_KEEP_WORKDIR") == "1", "preserve the per-run workdir")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if !opts.setupOnly && strings.TrimSpace(opts.jbgoBin) == "" {
		return options{}, fmt.Errorf("--jbgo-bin is required unless --setup is used")
	}
	return opts, nil
}

func loadManifest() (*manifest, error) {
	data, err := manifestFS.ReadFile("manifest.json")
	if err != nil {
		return nil, err
	}
	var out manifest
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func run(ctx context.Context, mf *manifest, opts *options) error {
	makeBin, err := findMake()
	if err != nil {
		return err
	}
	if err := requireTool("perl"); err != nil {
		return err
	}
	if err := requireCC(); err != nil {
		return err
	}

	cacheDir, err := filepath.Abs(opts.cacheDir)
	if err != nil {
		return err
	}
	sourceDir, err := ensureSourceCache(ctx, mf, cacheDir)
	if err != nil {
		return err
	}
	if opts.setupOnly {
		fmt.Printf("GNU coreutils %s prepared at %s\n", mf.GNUVersion, sourceDir)
		return nil
	}

	jbgoBin, err := filepath.Abs(opts.jbgoBin)
	if err != nil {
		return err
	}
	if err := ensureExecutable(jbgoBin); err != nil {
		return err
	}

	workDir, err := prepareWorkDir(cacheDir, mf.GNUVersion, sourceDir)
	if err != nil {
		return err
	}
	if !opts.keepWorkdir {
		defer func() { _ = os.RemoveAll(workDir) }()
	}

	resultsDir, err := prepareResultsDir(cacheDir, opts.resultsDir)
	if err != nil {
		return fmt.Errorf("create results dir: %w", err)
	}

	if err := configureAndBuild(ctx, makeBin, workDir); err != nil {
		return err
	}

	programs, err := listGNUPrograms(ctx, workDir)
	if err != nil {
		return err
	}
	selectedUtilities, err := selectUtilities(mf, opts.utils)
	if err != nil {
		return err
	}
	runTargets := append([]utilityManifest(nil), selectedUtilities...)
	explicitTests := parseList(opts.tests)
	if len(explicitTests) != 0 {
		runTargets = []utilityManifest{{Name: "explicit-tests"}}
	}
	supportedSet := implementedGNUProgramSet()
	if err := prepareProgramDir(workDir, jbgoBin, programs, supportedSet); err != nil {
		return err
	}
	if err := disableCheckRebuild(workDir); err != nil {
		return err
	}

	summary := runSummary{
		GNUVersion: mf.GNUVersion,
		WorkDir:    workDir,
		ResultsDir: resultsDir,
	}
	hadFailure := false
	for _, utility := range runTargets {
		tests, skipped, err := resolveUtilityTests(workDir, utility, mf.SkipPatterns, explicitTests)
		if err != nil {
			return err
		}
		result := utilityResult{
			Name:    utility.Name,
			Tests:   tests,
			Skipped: skipped,
			Summary: summarizeTestResults(nil, len(skipped), 0),
		}
		if len(tests) == 0 {
			result.Reason = "no runnable GNU tests matched after applying skip filters"
			summary.Utilities = append(summary.Utilities, result)
			continue
		}

		logFile := utility.Name + ".log"
		logPath := filepath.Join(resultsDir, logFile)
		makeCheckResult, err := runMakeCheck(ctx, makeBin, workDir, tests, logPath)
		if err != nil {
			return err
		}
		result.TestResults, result.ExtraResults = parseReportedTestResults(makeCheckResult.Output, tests)
		result.Summary = summarizeTestResults(result.TestResults, len(skipped), len(result.ExtraResults))
		result.LogFile = logFile
		result.LogPath = logPath
		result.ExitCode = makeCheckResult.ExitCode
		result.Passed = makeCheckResult.ExitCode == 0
		if makeCheckResult.ExitCode != 0 {
			hadFailure = true
		}
		summary.Utilities = append(summary.Utilities, result)
		fmt.Printf("%s: %d tests, exit=%d, pass=%s\n", utility.Name, len(tests), makeCheckResult.ExitCode, formatPercent(result.Summary.PassPctSelected))
	}

	if len(explicitTests) == 0 {
		summary.Utilities = completeUtilityResults(summary.Utilities, programs, mf.Utilities, selectedUtilities, supportedSet)
	}

	summary.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	summary.Overall = summarizeOverall(summary.Utilities)
	summary.UtilitySummary = summarizeUtilityTotals(summary.Utilities)

	summaryPath := filepath.Join(resultsDir, "summary.json")
	if err := writeJSON(summaryPath, summary); err != nil {
		return err
	}
	fmt.Printf("results: %s\n", resultsDir)
	if hadFailure {
		return fmt.Errorf("GNU compatibility run failed")
	}
	return nil
}

func findMake() (string, error) {
	for _, candidate := range []string{"gmake", "make"} {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("could not find make or gmake")
}

func requireTool(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("missing required tool %q", name)
	}
	return nil
}

func requireCC() error {
	for _, candidate := range []string{os.Getenv("CC"), "cc", "clang", "gcc"} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if _, err := exec.LookPath(candidate); err == nil {
			return nil
		}
	}
	return fmt.Errorf("missing required C compiler (tried $CC, cc, clang, gcc)")
}

func ensureSourceCache(ctx context.Context, mf *manifest, cacheDir string) (string, error) {
	downloadsDir := filepath.Join(cacheDir, "downloads")
	sourceRoot := filepath.Join(cacheDir, "src")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		return "", err
	}

	tarballPath := filepath.Join(downloadsDir, filepath.Base(mf.TarballURL))
	if _, err := os.Stat(tarballPath); errorsIsNotExist(err) {
		if err := downloadFile(ctx, mf.TarballURL, tarballPath); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(mf.TarballSHA256) != "" {
		if err := verifySHA256(tarballPath, mf.TarballSHA256); err != nil {
			return "", err
		}
	}

	sourceDir := filepath.Join(sourceRoot, "coreutils-"+mf.GNUVersion)
	if _, err := os.Stat(sourceDir); err == nil {
		cacheCurrent, err := sourceCacheCurrent(sourceDir)
		if err != nil {
			return "", err
		}
		if cacheCurrent {
			return sourceDir, nil
		}
		if err := os.RemoveAll(sourceDir); err != nil {
			return "", err
		}
	}
	if err := extractTarGz(tarballPath, sourceRoot); err != nil {
		return "", err
	}
	if err := writeSourceCacheVersion(sourceDir); err != nil {
		return "", err
	}
	return sourceDir, nil
}

func prepareWorkDir(cacheDir, version, sourceDir string) (string, error) {
	workRoot := filepath.Join(cacheDir, "work")
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return "", err
	}
	workDir, err := os.MkdirTemp(workRoot, "coreutils-"+version+"-")
	if err != nil {
		return "", err
	}
	cmd := exec.Command("cp", "-Rp", sourceDir+"/.", workDir)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("copy source tree: %w", err)
	}
	return workDir, nil
}

func configureAndBuild(ctx context.Context, makeBin, workDir string) error {
	configure := exec.CommandContext(ctx, "./configure", "--disable-nls", "--disable-dependency-tracking")
	configure.Dir = workDir
	configure.Stdout = os.Stdout
	configure.Stderr = os.Stderr
	if err := configure.Run(); err != nil {
		return fmt.Errorf("configure GNU coreutils: %w", err)
	}

	makeCmd := exec.CommandContext(ctx, makeBin, "-j", fmt.Sprintf("%d", maxInt(runtime.NumCPU(), 2)))
	makeCmd.Dir = workDir
	makeCmd.Stdout = os.Stdout
	makeCmd.Stderr = os.Stderr
	if err := makeCmd.Run(); err != nil {
		return fmt.Errorf("build GNU coreutils: %w", err)
	}
	return nil
}

func listGNUPrograms(ctx context.Context, workDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(workDir, "build-aux", "gen-lists-of-programs.sh"), "--list-progs")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list GNU programs: %w", err)
	}
	lines := strings.Fields(string(out))
	sort.Strings(lines)
	return lines, nil
}

func prepareProgramDir(workDir, jbgoBin string, programs []string, supported map[string]struct{}) error {
	srcDir := filepath.Join(workDir, "src")
	supportedNames := make([]string, 0)
	unsupportedNames := make([]string, 0)
	for _, name := range programs {
		path := filepath.Join(srcDir, name)
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if _, ok := supported[name]; ok {
			supportedNames = append(supportedNames, name)
		} else {
			unsupportedNames = append(unsupportedNames, name)
		}
	}
	if err := compatshims.SymlinkCommands(srcDir, jbgoBin, supportedNames); err != nil {
		return err
	}
	if err := compatshims.WriteUnsupportedStubs(srcDir, unsupportedNames); err != nil {
		return err
	}
	if _, ok := supported["install"]; ok {
		supportedNames = append(supportedNames, "ginstall")
		if err := compatshims.SymlinkCommands(srcDir, jbgoBin, []string{"ginstall"}); err != nil {
			return err
		}
	} else {
		unsupportedNames = append(unsupportedNames, "ginstall")
		if err := compatshims.WriteUnsupportedStubs(srcDir, []string{"ginstall"}); err != nil {
			return err
		}
	}
	if err := installTestRelinkHook(workDir, jbgoBin, supportedNames, unsupportedNames); err != nil {
		return err
	}
	return nil
}

func implementedGNUProgramSet() map[string]struct{} {
	supported := make(map[string]struct{})
	for _, name := range jbcommands.DefaultRegistry().Names() {
		supported[name] = struct{}{}
	}
	return supported
}

func disableCheckRebuild(workDir string) error {
	makefilePath := filepath.Join(workDir, "Makefile")
	data, err := os.ReadFile(makefilePath)
	if err != nil {
		return err
	}
	updated := strings.Replace(string(data), "check-am: all-am", "check-am:", 1)
	if updated == string(data) {
		return nil
	}
	return os.WriteFile(makefilePath, []byte(updated), 0o644)
}

func installTestRelinkHook(workDir, jbgoBin string, supportedNames, unsupportedNames []string) error {
	hookDir := filepath.Join(workDir, "build-aux", "jbgo-harness")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}

	if err := writeLines(filepath.Join(hookDir, "supported-programs.txt"), supportedNames); err != nil {
		return err
	}
	if err := writeLines(filepath.Join(hookDir, "unsupported-programs.txt"), unsupportedNames); err != nil {
		return err
	}

	scriptPath := filepath.Join(hookDir, "relink.sh")
	script := fmt.Sprintf(`#!/bin/sh
set -eu

src_dir=$1
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
jbgo_bin=%s

while IFS= read -r name || [ -n "$name" ]; do
  [ -n "$name" ] || continue
  rm -rf "$src_dir/$name"
  ln -sf "$jbgo_bin" "$src_dir/$name"
done < "$script_dir/supported-programs.txt"

while IFS= read -r name || [ -n "$name" ]; do
  [ -n "$name" ] || continue
  rm -rf "$src_dir/$name"
  cat > "$src_dir/$name" <<'EOF'
#!/bin/sh
printf '%%s: unsupported by jbgo GNU harness\n' "$(basename "$0")" >&2
exit 127
EOF
  chmod 755 "$src_dir/$name"
done < "$script_dir/unsupported-programs.txt"
`, shellSingleQuoteForScript(jbgoBin))
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return err
	}

	return patchTestsEnvironment(filepath.Join(workDir, "Makefile"))
}

func writeLines(path string, lines []string) error {
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	data := strings.Join(sorted, "\n")
	if data != "" {
		data += "\n"
	}
	return os.WriteFile(path, []byte(data), 0o644)
}

func patchTestsEnvironment(makefilePath string) error {
	data, err := os.ReadFile(makefilePath)
	if err != nil {
		return err
	}
	const needle = "TESTS_ENVIRONMENT = \\\n"
	const injection = "TESTS_ENVIRONMENT = \\\n  $(SHELL) '$(abs_top_builddir)/build-aux/jbgo-harness/relink.sh' '$(abs_top_builddir)/src' || exit $$?; \\\n"
	contents := string(data)
	if strings.Contains(contents, "build-aux/jbgo-harness/relink.sh") {
		return nil
	}
	updated := strings.Replace(contents, needle, injection, 1)
	if updated == contents {
		return fmt.Errorf("patch TESTS_ENVIRONMENT: marker not found")
	}
	return os.WriteFile(makefilePath, []byte(updated), 0o644)
}

func shellSingleQuoteForScript(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func selectUtilities(mf *manifest, raw string) ([]utilityManifest, error) {
	selected := parseList(raw)
	if len(selected) == 0 {
		return append([]utilityManifest(nil), mf.Utilities...), nil
	}
	allowed := make(map[string]utilityManifest, len(mf.Utilities))
	for _, utility := range mf.Utilities {
		allowed[utility.Name] = utility
	}
	out := make([]utilityManifest, 0, len(selected))
	for _, name := range selected {
		utility, ok := allowed[name]
		if !ok {
			return nil, fmt.Errorf("unknown utility %q", name)
		}
		out = append(out, utility)
	}
	return out, nil
}

func resolveUtilityTests(workDir string, utility utilityManifest, globalSkips []skipPattern, explicitTests []string) (testsOut, skippedOut []string, err error) {
	if len(explicitTests) != 0 {
		filtered := make([]string, 0, len(explicitTests))
		for _, test := range explicitTests {
			if skip, _, err := shouldSkipTest(filepath.ToSlash(test), filepath.Join(workDir, test), globalSkips, utility.Skips); err != nil {
				return nil, nil, err
			} else if skip {
				continue
			} else {
				filtered = append(filtered, test)
			}
		}
		sort.Strings(filtered)
		return filtered, nil, nil
	}

	tests := make(map[string]struct{})
	skipped := make([]string, 0)
	for _, pattern := range utility.Patterns {
		matches, err := filepath.Glob(filepath.Join(workDir, pattern))
		if err != nil {
			return nil, nil, err
		}
		sort.Strings(matches)
		for _, match := range matches {
			rel, err := filepath.Rel(workDir, match)
			if err != nil {
				return nil, nil, err
			}
			rel = filepath.ToSlash(rel)
			if skip, reason, err := shouldSkipTest(rel, match, globalSkips, utility.Skips); err != nil {
				return nil, nil, err
			} else if skip {
				skipped = append(skipped, rel+": "+reason)
				continue
			}
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			tests[rel] = struct{}{}
		}
	}
	out := make([]string, 0, len(tests))
	for test := range tests {
		out = append(out, test)
	}
	sort.Strings(out)
	sort.Strings(skipped)
	return out, skipped, nil
}

func shouldSkipTest(rel, path string, globalSkips []skipPattern, utilitySkips []string) (skip bool, reason string, err error) {
	for _, skip := range globalSkips {
		if matched, err := filepath.Match(skip.Pattern, rel); err == nil && matched {
			return true, skip.Reason, nil
		}
	}
	for _, pattern := range utilitySkips {
		if matched, err := filepath.Match(pattern, rel); err == nil && matched {
			return true, "utility-specific skip", nil
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", err
	}
	contents := string(data)
	switch {
	case strings.Contains(contents, "require_controlling_input_terminal"):
		return true, "controlling TTY tests are skipped in v1", nil
	case strings.Contains(contents, "require_root_"):
		return true, "root-required tests are skipped in v1", nil
	case strings.Contains(contents, "require_selinux_"):
		return true, "SELinux tests are skipped in v1", nil
	case strings.Contains(rel, "help-version"):
		return true, "help/version tests are skipped in v1", nil
	default:
		return false, "", nil
	}
}

func runMakeCheck(ctx context.Context, makeBin, workDir string, tests []string, logPath string) (makeCheckResult, error) {
	args := []string{
		"check",
		"SUBDIRS=.",
		"VERBOSE=no",
		"RUN_EXPENSIVE_TESTS=yes",
		"RUN_VERY_EXPENSIVE_TESTS=yes",
		"srcdir=" + workDir,
		"TESTS=" + strings.Join(tests, " "),
	}
	cmd := exec.CommandContext(ctx, makeBin, args...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if writeErr := os.WriteFile(logPath, output, 0o644); writeErr != nil {
		return makeCheckResult{}, writeErr
	}
	if err == nil {
		return makeCheckResult{Output: output}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return makeCheckResult{ExitCode: exitErr.ExitCode(), Output: output}, nil
	}
	return makeCheckResult{}, err
}

func parseReportedTestResults(logData []byte, selectedTests []string) (selectedResultsOut, extraResultsOut []testResult) {
	aliases := buildTestAliases(selectedTests)
	selectedResults := make(map[string]*testResult, len(selectedTests))
	extraResults := make(map[string]*testResult)

	for rawLine := range strings.SplitSeq(string(logData), "\n") {
		status, reportedName, ok := parseReportedTestStatusLine(rawLine)
		if !ok {
			continue
		}
		reportedName = filepath.ToSlash(reportedName)
		canonicalName := reportedName
		target := extraResults
		if mappedName, ok := aliases[reportedName]; ok {
			canonicalName = mappedName
			target = selectedResults
		}
		recordReportedTestStatus(target, canonicalName, reportedName, status)
	}

	results := make([]testResult, 0, len(selectedTests))
	for _, selectedTest := range selectedTests {
		canonicalName := filepath.ToSlash(selectedTest)
		if result, ok := selectedResults[canonicalName]; ok {
			results = append(results, *result)
			continue
		}
		results = append(results, testResult{
			Name:   canonicalName,
			Status: "unreported",
		})
	}

	extraKeys := make([]string, 0, len(extraResults))
	for name := range extraResults {
		extraKeys = append(extraKeys, name)
	}
	sort.Strings(extraKeys)

	extras := make([]testResult, 0, len(extraKeys))
	for _, name := range extraKeys {
		extras = append(extras, *extraResults[name])
	}
	return results, extras
}

func buildTestAliases(selectedTests []string) map[string]string {
	candidates := make(map[string][]string, len(selectedTests)*2)
	for _, selectedTest := range selectedTests {
		canonicalName := filepath.ToSlash(selectedTest)
		candidates[canonicalName] = append(candidates[canonicalName], canonicalName)
		switch ext := filepath.Ext(canonicalName); ext {
		case ".pl", ".sh", ".xpl":
			alias := strings.TrimSuffix(canonicalName, ext)
			candidates[alias] = append(candidates[alias], canonicalName)
		}
	}

	aliases := make(map[string]string, len(candidates))
	for alias, names := range candidates {
		if len(names) == 1 {
			aliases[alias] = names[0]
		}
	}
	return aliases
}

func parseReportedTestStatusLine(line string) (status, name string, ok bool) {
	line = strings.TrimSpace(line)
	for prefix, statusName := range map[string]string{
		"PASS:":  "pass",
		"FAIL:":  "fail",
		"SKIP:":  "skip",
		"XFAIL:": "xfail",
		"XPASS:": "xpass",
		"ERROR:": "error",
	} {
		if strings.HasPrefix(line, prefix) {
			testName := strings.TrimSpace(line[len(prefix):])
			if testName == "" {
				return "", "", false
			}
			return statusName, testName, true
		}
	}
	return "", "", false
}

func recordReportedTestStatus(results map[string]*testResult, canonicalName, reportedName, status string) {
	existing := results[canonicalName]
	if existing == nil {
		results[canonicalName] = &testResult{
			Name:       canonicalName,
			Status:     status,
			ReportedAs: []string{reportedName},
		}
		return
	}
	if testStatusPrecedence(status) > testStatusPrecedence(existing.Status) {
		existing.Status = status
	}
	if !containsString(existing.ReportedAs, reportedName) {
		existing.ReportedAs = append(existing.ReportedAs, reportedName)
	}
}

func testStatusPrecedence(status string) int {
	switch status {
	case "error":
		return 6
	case "xpass":
		return 5
	case "fail":
		return 4
	case "xfail":
		return 3
	case "skip":
		return 2
	case "pass":
		return 1
	default:
		return 0
	}
}

func summarizeTestResults(results []testResult, filteredSkipTotal, reportedExtraTotal int) testSummary {
	summary := testSummary{
		SelectedTotal:      len(results),
		FilteredSkipTotal:  filteredSkipTotal,
		ReportedExtraTotal: reportedExtraTotal,
	}
	for _, result := range results {
		switch result.Status {
		case "pass":
			summary.Pass++
		case "fail":
			summary.Fail++
		case "skip":
			summary.Skip++
		case "xfail":
			summary.XFail++
		case "xpass":
			summary.XPass++
		case "error":
			summary.Error++
		default:
			summary.Unreported++
		}
	}
	summary.RunnableTotal = summary.Pass + summary.Fail + summary.XFail + summary.XPass + summary.Error
	summary.PassPctSelected = percentage(summary.Pass, summary.SelectedTotal)
	summary.PassPctRunnable = percentage(summary.Pass, summary.RunnableTotal)
	return summary
}

func summarizeOverall(results []utilityResult) testSummary {
	overall := testSummary{}
	for i := range results {
		utility := &results[i]
		overall.SelectedTotal += utility.Summary.SelectedTotal
		overall.FilteredSkipTotal += utility.Summary.FilteredSkipTotal
		overall.ReportedExtraTotal += utility.Summary.ReportedExtraTotal
		overall.Pass += utility.Summary.Pass
		overall.Fail += utility.Summary.Fail
		overall.Skip += utility.Summary.Skip
		overall.XFail += utility.Summary.XFail
		overall.XPass += utility.Summary.XPass
		overall.Error += utility.Summary.Error
		overall.Unreported += utility.Summary.Unreported
		overall.RunnableTotal += utility.Summary.RunnableTotal
	}
	overall.PassPctSelected = percentage(overall.Pass, overall.SelectedTotal)
	overall.PassPctRunnable = percentage(overall.Pass, overall.RunnableTotal)
	return overall
}

func summarizeUtilityTotals(results []utilityResult) utilityTotals {
	totals := utilityTotals{}
	for i := range results {
		result := &results[i]
		if result.Inactive {
			continue
		}
		totals.Total++
		if result.Summary.RunnableTotal == 0 {
			totals.NoRunnableTests++
			continue
		}
		if result.Passed {
			totals.Passed++
			continue
		}
		totals.Failed++
	}
	totals.PassPctTotal = percentage(totals.Passed, totals.Total)
	totals.PassPctRunnable = percentage(totals.Passed, totals.Passed+totals.Failed)
	return totals
}

func completeUtilityResults(results []utilityResult, programs []string, manifestUtilities, selectedUtilities []utilityManifest, supportedSet map[string]struct{}) []utilityResult {
	activeByName := make(map[string]utilityResult, len(results))
	for i := range results {
		result := results[i]
		activeByName[result.Name] = result
	}
	manifestSet := make(map[string]struct{}, len(manifestUtilities))
	for _, utility := range manifestUtilities {
		manifestSet[utility.Name] = struct{}{}
	}
	selectedSet := make(map[string]struct{}, len(selectedUtilities))
	for _, utility := range selectedUtilities {
		selectedSet[utility.Name] = struct{}{}
	}

	out := make([]utilityResult, 0, len(programs))
	for _, name := range programs {
		if result, ok := activeByName[name]; ok {
			out = append(out, result)
			delete(activeByName, name)
			continue
		}
		out = append(out, utilityResult{
			Name:     name,
			Inactive: true,
			Reason:   inactiveUtilityReason(name, manifestSet, selectedSet, supportedSet),
		})
	}
	for i := range results {
		result := results[i]
		if _, ok := activeByName[result.Name]; ok {
			out = append(out, result)
			delete(activeByName, result.Name)
		}
	}
	return out
}

func inactiveUtilityReason(name string, manifestSet, selectedSet, supportedSet map[string]struct{}) string {
	if _, ok := manifestSet[name]; ok {
		if _, ok := selectedSet[name]; ok {
			return ""
		}
		return "not selected in this run"
	}
	if _, ok := supportedSet[name]; ok {
		return "implemented in jbgo, but not included in the compatibility manifest"
	}
	return "not currently covered by the compatibility manifest"
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func formatPercent(value float64) string {
	if value == 0 {
		return "0%"
	}
	if value == math.Trunc(value) {
		return fmt.Sprintf("%.0f%%", value)
	}
	formatted := fmt.Sprintf("%.2f", value)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	return formatted + "%"
}

func percentage(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return math.Round((float64(numerator)/float64(denominator))*10000) / 100
}

func containsString(items []string, needle string) bool {
	return slices.Contains(items, needle)
}

func prepareResultsDir(cacheDir, explicitDir string) (string, error) {
	if strings.TrimSpace(explicitDir) == "" {
		root := filepath.Join(cacheDir, "results")
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", err
		}
		return os.MkdirTemp(root, "run-")
	}

	resultsDir, err := filepath.Abs(explicitDir)
	if err != nil {
		return "", err
	}
	if filepath.Clean(resultsDir) == string(os.PathSeparator) {
		return "", fmt.Errorf("refusing to use filesystem root as results dir")
	}
	info, err := os.Stat(resultsDir)
	switch {
	case err == nil && !info.IsDir():
		return "", fmt.Errorf("results dir %s exists and is not a directory", resultsDir)
	case err == nil:
		entries, err := os.ReadDir(resultsDir)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if err := os.RemoveAll(filepath.Join(resultsDir, entry.Name())); err != nil {
				return "", err
			}
		}
	case errorsIsNotExist(err):
		if err := os.MkdirAll(resultsDir, 0o755); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	return resultsDir, nil
}

func downloadFile(ctx context.Context, url, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	tmpPath := destination + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
}

func verifySHA256(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(sum, expected) {
		return fmt.Errorf("sha256 mismatch for %s: got %s want %s", path, sum, expected)
	}
	return nil
}

func extractTarGz(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = gzr.Close() }()
	tr := tar.NewReader(gzr)
	type dirModTime struct {
		path    string
		modTime time.Time
	}
	var dirs []dirModTime
	for {
		header, err := tr.Next()
		if err == io.EOF {
			for i := len(dirs) - 1; i >= 0; i-- {
				if err := os.Chtimes(dirs[i].path, dirs[i].modTime, dirs[i].modTime); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destination, header.Name)
		modTime := header.ModTime
		if modTime.IsZero() {
			modTime = time.Unix(0, 0)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, header.FileInfo().Mode()); err != nil {
				return err
			}
			dirs = append(dirs, dirModTime{path: target, modTime: modTime})
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, tr); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			if err := os.Chtimes(target, modTime, modTime); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
}

func sourceCacheCurrent(sourceDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(sourceDir, ".jbgo-cache-version"))
	if errorsIsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(data)) == sourceCacheVersion, nil
}

func writeSourceCacheVersion(sourceDir string) error {
	return os.WriteFile(filepath.Join(sourceDir, ".jbgo-cache-version"), []byte(sourceCacheVersion+"\n"), 0o644)
}

func ensureExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, want jbgo binary", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

func parseList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "jbgo-gnu: "+format+"\n", args...)
	os.Exit(1)
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
