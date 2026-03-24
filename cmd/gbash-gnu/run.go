package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func run(ctx context.Context, mf *manifest, opts *options) error {
	workDir, err := filepath.Abs(opts.workDir)
	if err != nil {
		return err
	}
	plan, err := buildRunPlan(ctx, mf, workDir, opts)
	if err != nil {
		return err
	}
	if opts.printTests {
		for _, test := range plan.selectedTests {
			fmt.Println(test)
		}
		return nil
	}

	summary, hadFailure, err := summarizeRun(mf, workDir, opts.resultsDir, opts.logPath, opts.exitCode, plan)
	if err != nil {
		return err
	}
	summaryPath := filepath.Join(summary.ResultsDir, "summary.json")
	if err := writeJSON(summaryPath, summary); err != nil {
		return err
	}
	fmt.Printf("results: %s\n", summary.ResultsDir)
	if hadFailure {
		return fmt.Errorf("GNU compatibility run failed")
	}
	return nil
}

func listGNUPrograms(ctx context.Context, workDir string) ([]string, error) {
	scriptPath := filepath.Join(workDir, "build-aux", "gen-lists-of-programs.sh")
	out, err := outputWithETXTBSYRetry(func() ([]byte, error) {
		cmd := exec.CommandContext(ctx, scriptPath, "--list-progs")
		cmd.Dir = workDir
		return cmd.Output()
	})
	if err != nil {
		return nil, fmt.Errorf("list GNU programs: %w", err)
	}
	lines := strings.Fields(string(out))
	return uniqueSortedStrings(lines), nil
}

func outputWithETXTBSYRetry(run func() ([]byte, error)) ([]byte, error) {
	const maxAttempts = 5

	var (
		out []byte
		err error
	)
	for attempt := range maxAttempts {
		out, err = run()
		if err == nil || !errors.Is(err, syscall.ETXTBSY) {
			return out, err
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return out, err
}

func buildRunPlan(ctx context.Context, mf *manifest, workDir string, opts *options) (*runPlan, error) {
	programs, err := listGNUPrograms(ctx, workDir)
	if err != nil {
		return nil, err
	}
	selectedUtilities, err := selectUtilities(programs, mf, opts.utils)
	if err != nil {
		return nil, err
	}
	explicitTests := parseList(opts.tests)
	allTests, globalSkipped, err := discoverRunnableTests(workDir, mf.SkipPatterns, explicitTests)
	if err != nil {
		return nil, err
	}
	runs, _, err := resolveUtilityRuns(workDir, selectedUtilities, mf.SkipPatterns, explicitTests)
	if err != nil {
		return nil, err
	}
	selectedTests := append([]string(nil), allTests...)
	filteredEntries := parseSkippedEntries(globalSkipped)
	if strings.TrimSpace(opts.utils) != "" || len(explicitTests) != 0 {
		selectedTests = combinedTestsForRuns(runs)
		filteredEntries = combinedSkippedEntriesForRuns(runs)
	}
	return &runPlan{
		runs:            runs,
		selectedTests:   selectedTests,
		filteredEntries: filteredEntries,
	}, nil
}

func summarizeRun(mf *manifest, workDir, resultsDirRaw, logPathRaw string, exitCode int, plan *runPlan) (runSummary, bool, error) {
	resultsDir, err := filepath.Abs(resultsDirRaw)
	if err != nil {
		return runSummary{}, false, err
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return runSummary{}, false, err
	}

	logPath := strings.TrimSpace(logPathRaw)
	logFile := ""
	var logData []byte
	if logPath != "" {
		logPath, err = filepath.Abs(logPath)
		if err != nil {
			return runSummary{}, false, err
		}
		logFile = filepath.Base(logPath)
		logData, err = os.ReadFile(logPath)
		if err != nil {
			return runSummary{}, false, err
		}
	}

	makeResult := makeCheckResult{
		ExitCode: exitCode,
		Output:   logData,
	}
	utilityResults, overall := buildBatchedUtilityResults(plan.runs, plan.selectedTests, len(plan.filteredEntries), makeResult, logFile, logPath)
	combinedResults, combinedExtras := parseReportedTestResults(logData, plan.selectedTests)

	summary := runSummary{
		GNUVersion:     mf.GNUVersion,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		WorkDir:        workDir,
		ResultsDir:     resultsDir,
		Overall:        overall,
		UtilitySummary: summarizeUtilityTotals(utilityResults),
		Utilities:      utilityResults,
	}
	summary.Suite, summary.Categories, summary.Commands, summary.Coverage, summary.ExtraResults = buildCoverageArtifacts(plan.selectedTests, plan.filteredEntries, combinedResults, combinedExtras, plan.runs, mf)
	return summary, !utilityPassed(&overall), nil
}

func combinedTestsForRuns(runs []utilityRun) []string {
	combined := make([]string, 0)
	seen := make(map[string]struct{})
	for _, run := range runs {
		for _, test := range run.Tests {
			if _, ok := seen[test]; ok {
				continue
			}
			seen[test] = struct{}{}
			combined = append(combined, test)
		}
	}
	return combined
}

func buildBatchedUtilityResults(runs []utilityRun, allTests []string, filteredSkipTotal int, makeCheckResult makeCheckResult, logFile, logPath string) ([]utilityResult, testSummary) {
	combinedResults, combinedExtras := parseReportedTestResults(makeCheckResult.Output, allTests)
	resultByName := make(map[string]testResult, len(combinedResults))
	for _, result := range combinedResults {
		resultByName[result.Name] = result
	}
	overall := summarizeTestResults(combinedResults, filteredSkipTotal, len(combinedExtras))

	results := make([]utilityResult, 0, len(runs))
	for _, run := range runs {
		result := utilityResult{
			Name:    run.Utility.Name,
			Tests:   run.Tests,
			Skipped: run.Skipped,
			Summary: summarizeTestResults(nil, len(run.Skipped), 0),
		}
		if len(run.Tests) == 0 {
			results = append(results, result)
			continue
		}

		testResults := make([]testResult, 0, len(run.Tests))
		for _, testName := range run.Tests {
			canonicalName := filepath.ToSlash(testName)
			if mapped, ok := resultByName[canonicalName]; ok {
				testResults = append(testResults, mapped)
				continue
			}
			testResults = append(testResults, testResult{Name: canonicalName, Status: "unreported"})
		}

		extraResults := extraResultsForUtility(run.Utility, combinedExtras)
		result.TestResults = testResults
		result.ExtraResults = extraResults
		result.Summary = summarizeTestResults(result.TestResults, len(run.Skipped), len(result.ExtraResults))
		result.LogFile = logFile
		result.LogPath = logPath
		result.ExitCode = utilityExitCode(&result.Summary, makeCheckResult.ExitCode)
		result.Passed = utilityPassed(&result.Summary)
		results = append(results, result)
	}
	return results, overall
}

func extraResultsForUtility(utility attributedUtility, extras []testResult) []testResult {
	if len(extras) == 0 {
		return nil
	}
	filtered := make([]testResult, 0)
	for _, extra := range extras {
		if utilityMatchesTestPath(utility, extra.Name) {
			filtered = append(filtered, extra)
		}
	}
	return filtered
}

func utilityMatchesTestPath(utility attributedUtility, rel string) bool {
	for _, pattern := range utility.Patterns {
		if matched, err := filepath.Match(pattern, rel); err == nil && matched {
			return true
		}
	}
	return false
}

func utilityPassed(summary *testSummary) bool {
	return summary.Fail == 0 && summary.XPass == 0 && summary.Error == 0 && summary.Unreported == 0
}

func utilityExitCode(summary *testSummary, batchExitCode int) int {
	if utilityPassed(summary) {
		return 0
	}
	if batchExitCode != 0 {
		return batchExitCode
	}
	return 1
}
