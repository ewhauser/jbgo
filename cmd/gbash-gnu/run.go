package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type executionEnv struct {
	cacheDir   string
	gbashBin   string
	resultsDir string
	workDir    string
}

type executionPlan struct {
	programs          []string
	selectedUtilities []utilityManifest
	runTargets        []utilityManifest
	explicitTests     []string
	supportedSet      map[string]struct{}
	configShell       string
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
	if opts.setupOnly {
		sourceDir, err := ensureSourceCache(ctx, mf, cacheDir)
		if err != nil {
			return err
		}
		fmt.Printf("GNU coreutils %s prepared at %s\n", mf.GNUVersion, sourceDir)
		return nil
	}
	if strings.TrimSpace(opts.writePreparedBuildArchive) != "" {
		return writePreparedBuildArchive(ctx, mf, opts, makeBin, cacheDir)
	}

	env, err := prepareExecutionEnvironment(ctx, mf, opts, makeBin, cacheDir)
	if err != nil {
		return err
	}
	if !opts.keepWorkdir {
		defer func() { _ = os.RemoveAll(env.workDir) }()
	}

	plan, err := prepareExecutionPlan(ctx, mf, env, opts)
	if err != nil {
		return err
	}

	summary, hadFailure, err := executeCompatibilityRun(ctx, makeBin, mf, env, plan)
	if err != nil {
		return err
	}
	summary.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	summary.Overall = summarizeOverall(summary.Utilities)
	summary.UtilitySummary = summarizeUtilityTotals(summary.Utilities)

	summaryPath := filepath.Join(env.resultsDir, "summary.json")
	if err := writeJSON(summaryPath, summary); err != nil {
		return err
	}
	fmt.Printf("results: %s\n", env.resultsDir)
	if hadFailure {
		return fmt.Errorf("GNU compatibility run failed")
	}
	return nil
}

func writePreparedBuildArchive(ctx context.Context, mf *manifest, opts *options, makeBin, cacheDir string) error {
	archivePath, err := filepath.Abs(opts.writePreparedBuildArchive)
	if err != nil {
		return err
	}
	sourceDir, err := ensureSourceCache(ctx, mf, cacheDir)
	if err != nil {
		return err
	}
	if err := buildPreparedBuildArchive(ctx, makeBin, cacheDir, mf.GNUVersion, sourceDir, archivePath, opts.keepWorkdir); err != nil {
		return err
	}
	fmt.Printf("prepared GNU build archive: %s\n", archivePath)
	return nil
}

func prepareExecutionEnvironment(ctx context.Context, mf *manifest, opts *options, makeBin, cacheDir string) (*executionEnv, error) {
	gbashBin, err := filepath.Abs(opts.gbashBin)
	if err != nil {
		return nil, err
	}
	if err := ensureExecutable(gbashBin); err != nil {
		return nil, err
	}

	resultsDir, err := prepareResultsDir(cacheDir, opts.resultsDir)
	if err != nil {
		return nil, fmt.Errorf("create results dir: %w", err)
	}

	preparedArchivePath := strings.TrimSpace(opts.preparedBuildArchive)
	if preparedArchivePath != "" {
		preparedArchivePath, err = filepath.Abs(preparedArchivePath)
		if err != nil {
			return nil, err
		}
	}

	workDir, err := prepareExecutionWorkDir(ctx, mf, makeBin, cacheDir, preparedArchivePath)
	if err != nil {
		return nil, err
	}

	return &executionEnv{
		cacheDir:   cacheDir,
		gbashBin:   gbashBin,
		resultsDir: resultsDir,
		workDir:    workDir,
	}, nil
}

func prepareExecutionWorkDir(ctx context.Context, mf *manifest, makeBin, cacheDir, preparedArchivePath string) (string, error) {
	var workDir string
	var err error
	if preparedArchivePath != "" {
		workDir, err = prepareWorkDirFromPreparedArchive(ctx, cacheDir, mf.GNUVersion, preparedArchivePath)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gbash-gnu: prepared build archive unavailable (%v); falling back to full build\n", err)
		}
	}
	if workDir != "" {
		return workDir, nil
	}
	if builtDir, err := findPreviousBuild(cacheDir, mf.GNUVersion); err == nil {
		workDir, err = prepareWorkDir(cacheDir, mf.GNUVersion, builtDir)
		if err != nil {
			return "", err
		}
		if err := relocatePreparedBuild(ctx, workDir); err != nil {
			return "", err
		}
		return workDir, nil
	}

	sourceDir, err := ensureSourceCache(ctx, mf, cacheDir)
	if err != nil {
		return "", err
	}
	workDir, err = prepareWorkDir(cacheDir, mf.GNUVersion, sourceDir)
	if err != nil {
		return "", err
	}
	if err := configureAndBuild(ctx, makeBin, workDir); err != nil {
		_ = os.RemoveAll(workDir)
		return "", err
	}
	return workDir, nil
}

func prepareExecutionPlan(ctx context.Context, mf *manifest, env *executionEnv, opts *options) (*executionPlan, error) {
	programs, err := listGNUPrograms(ctx, env.workDir)
	if err != nil {
		return nil, err
	}
	selectedUtilities, err := selectUtilities(mf, opts.utils)
	if err != nil {
		return nil, err
	}
	runTargets := append([]utilityManifest(nil), selectedUtilities...)
	explicitTests := parseList(opts.tests)
	if len(explicitTests) != 0 {
		runTargets = []utilityManifest{{Name: "explicit-tests"}}
	}
	supportedSet := implementedGNUProgramSet()
	if err := prepareProgramDir(env.workDir, env.gbashBin, programs, supportedSet); err != nil {
		return nil, err
	}
	configShell, err := compatConfigShellPath(env.workDir)
	if err != nil {
		return nil, err
	}
	if err := disableCheckRebuild(env.workDir); err != nil {
		return nil, err
	}

	return &executionPlan{
		programs:          programs,
		selectedUtilities: selectedUtilities,
		runTargets:        runTargets,
		explicitTests:     explicitTests,
		supportedSet:      supportedSet,
		configShell:       configShell,
	}, nil
}

func executeCompatibilityRun(ctx context.Context, makeBin string, mf *manifest, env *executionEnv, plan *executionPlan) (runSummary, bool, error) {
	summary := runSummary{
		GNUVersion: mf.GNUVersion,
		WorkDir:    env.workDir,
		ResultsDir: env.resultsDir,
	}
	utilityRuns, err := resolveUtilityRuns(env.workDir, plan.runTargets, mf.SkipPatterns, plan.explicitTests)
	if err != nil {
		return runSummary{}, false, err
	}

	results, hadFailure, err := executeUtilityRuns(ctx, makeBin, env, plan.configShell, utilityRuns)
	if err != nil {
		return runSummary{}, false, err
	}
	summary.Utilities = append(summary.Utilities, results...)

	if len(plan.explicitTests) == 0 {
		summary.Utilities = completeUtilityResults(summary.Utilities, plan.programs, mf.Utilities, plan.selectedUtilities, plan.supportedSet)
	}
	return summary, hadFailure, nil
}

func resolveUtilityRuns(workDir string, runTargets []utilityManifest, globalSkips []skipPattern, explicitTests []string) ([]utilityRun, error) {
	runs := make([]utilityRun, 0, len(runTargets))
	for _, utility := range runTargets {
		tests, skipped, err := resolveUtilityTests(workDir, utility, globalSkips, explicitTests)
		if err != nil {
			return nil, err
		}
		runs = append(runs, utilityRun{
			Utility: utility,
			Tests:   tests,
			Skipped: skipped,
		})
	}
	return runs, nil
}

func executeUtilityRuns(ctx context.Context, makeBin string, env *executionEnv, configShell string, runs []utilityRun) ([]utilityResult, bool, error) {
	if len(runs) <= 1 {
		return executeUtilityRunsIndividually(ctx, makeBin, env, configShell, runs)
	}
	return executeUtilityRunsBatched(ctx, makeBin, env, configShell, runs)
}

func executeUtilityRunsIndividually(ctx context.Context, makeBin string, env *executionEnv, configShell string, runs []utilityRun) ([]utilityResult, bool, error) {
	results := make([]utilityResult, 0, len(runs))
	hadFailure := false
	for _, run := range runs {
		result := utilityResult{
			Name:    run.Utility.Name,
			Tests:   run.Tests,
			Skipped: run.Skipped,
			Summary: summarizeTestResults(nil, len(run.Skipped), 0),
		}
		if len(run.Tests) == 0 {
			result.Reason = "no runnable GNU tests matched after applying skip filters"
			results = append(results, result)
			continue
		}

		logFile := run.Utility.Name + ".log"
		logPath := filepath.Join(env.resultsDir, logFile)
		makeCheckResult, err := runMakeCheck(ctx, makeBin, env.workDir, configShell, run.Tests, logPath)
		if err != nil {
			return nil, false, err
		}
		result.TestResults, result.ExtraResults = parseReportedTestResults(makeCheckResult.Output, run.Tests)
		result.Summary = summarizeTestResults(result.TestResults, len(run.Skipped), len(result.ExtraResults))
		result.LogFile = logFile
		result.LogPath = logPath
		result.ExitCode = makeCheckResult.ExitCode
		result.Passed = makeCheckResult.ExitCode == 0
		if makeCheckResult.ExitCode != 0 {
			hadFailure = true
		}
		results = append(results, result)
		fmt.Printf("%s: %d tests, exit=%d, pass=%s\n", run.Utility.Name, len(run.Tests), makeCheckResult.ExitCode, formatPercent(result.Summary.PassPctSelected))
	}
	return results, hadFailure, nil
}

func executeUtilityRunsBatched(ctx context.Context, makeBin string, env *executionEnv, configShell string, runs []utilityRun) ([]utilityResult, bool, error) {
	combinedTests := combinedTestsForRuns(runs)
	if len(combinedTests) == 0 {
		results := make([]utilityResult, 0, len(runs))
		for _, run := range runs {
			result := utilityResult{
				Name:    run.Utility.Name,
				Tests:   run.Tests,
				Skipped: run.Skipped,
				Summary: summarizeTestResults(nil, len(run.Skipped), 0),
				Reason:  "no runnable GNU tests matched after applying skip filters",
			}
			results = append(results, result)
		}
		return results, false, nil
	}

	logFile := "compat.log"
	logPath := filepath.Join(env.resultsDir, logFile)
	makeCheckResult, err := runMakeCheck(ctx, makeBin, env.workDir, configShell, combinedTests, logPath)
	if err != nil {
		return nil, false, err
	}

	results := buildBatchedUtilityResults(runs, makeCheckResult, logFile, logPath)
	hadFailure := false
	for i := range results {
		result := &results[i]
		if !result.Passed && len(result.Tests) != 0 {
			hadFailure = true
		}
		fmt.Printf("%s: %d tests, exit=%d, pass=%s\n", result.Name, len(result.Tests), result.ExitCode, formatPercent(result.Summary.PassPctSelected))
	}
	return results, hadFailure, nil
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

func buildBatchedUtilityResults(runs []utilityRun, makeCheckResult makeCheckResult, logFile, logPath string) []utilityResult {
	combinedTests := combinedTestsForRuns(runs)
	combinedResults, combinedExtras := parseReportedTestResults(makeCheckResult.Output, combinedTests)
	resultByName := make(map[string]testResult, len(combinedResults))
	for _, result := range combinedResults {
		resultByName[result.Name] = result
	}

	results := make([]utilityResult, 0, len(runs))
	for _, run := range runs {
		result := utilityResult{
			Name:    run.Utility.Name,
			Tests:   run.Tests,
			Skipped: run.Skipped,
			Summary: summarizeTestResults(nil, len(run.Skipped), 0),
		}
		if len(run.Tests) == 0 {
			result.Reason = "no runnable GNU tests matched after applying skip filters"
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
	return results
}

func extraResultsForUtility(utility utilityManifest, extras []testResult) []testResult {
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

func utilityMatchesTestPath(utility utilityManifest, rel string) bool {
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
