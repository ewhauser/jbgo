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
	hadFailure := false
	for _, utility := range plan.runTargets {
		tests, skipped, err := resolveUtilityTests(env.workDir, utility, mf.SkipPatterns, plan.explicitTests)
		if err != nil {
			return runSummary{}, false, err
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
		logPath := filepath.Join(env.resultsDir, logFile)
		makeCheckResult, err := runMakeCheck(ctx, makeBin, env.workDir, plan.configShell, tests, logPath)
		if err != nil {
			return runSummary{}, false, err
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

	if len(plan.explicitTests) == 0 {
		summary.Utilities = completeUtilityResults(summary.Utilities, plan.programs, mf.Utilities, plan.selectedUtilities, plan.supportedSet)
	}
	return summary, hadFailure, nil
}
