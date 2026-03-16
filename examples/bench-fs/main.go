package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/examples/internal/blevefs"
	"github.com/ewhauser/gbash/examples/internal/sqlitefs"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/searchadapter"
	"github.com/ulikunitz/xz"
)

const (
	defaultRuns               = 5
	virtualWorkspace          = "/workspace"
	baseWorkspaceRoot         = "/workspace/base"
	agenticWorkspaceRoot      = "/workspace/agentic"
	kernelWorkspaceRoot       = "/workspace/linux"
	searchLiteral             = "fulltext-benchmark-needle"
	agenticTimeoutTerm        = "timeout"
	agenticRollbackTerm       = "rollback"
	fixtureProfileSynthetic   = "synthetic"
	fixtureProfileLinuxKernel = "linux-kernel"
	kernelCacheModeStream     = "stream"
	linuxKernelArchiveName    = "linux-6.19.8.tar.xz"
	linuxKernelArchiveURL     = "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-6.19.8.tar.xz"
	linuxKernelArchiveSHA256  = "aada4722db8bcfa0b9732851856d405082b6a4fa2e3ab067be8db17cdd115b38"
	linuxKernelTargetBytes    = 200 * 1024 * 1024
	mutationCreatePath        = "/workspace/base/scratch/session/output.txt"
	mutationCreateBody        = "created in benchmark mutation\n"
	mutationRewritePath       = "/workspace/base/docs/runbook00.md"
	mutationRewriteBody       = "rewritten benchmark doc\n"
	mutationRenameSrc         = "/workspace/base/reports/summary00.csv"
	mutationRenameDst         = "/workspace/base/reports/summary00-renamed.csv"
	mutationRemovePath        = "/workspace/base/logs/batch00.log"
)

type options struct {
	Runs           int
	JSONOut        string
	FixtureProfile string
	KernelCacheDir string
}

type fixtureSummary struct {
	FileCount  int   `json:"file_count"`
	TotalBytes int64 `json:"total_bytes"`
}

type latencyStats struct {
	MinNanos    int64 `json:"min_nanos"`
	MedianNanos int64 `json:"median_nanos"`
	P95Nanos    int64 `json:"p95_nanos"`
}

type machineInfo struct {
	Model     string `json:"model"`
	Chip      string `json:"chip"`
	Cores     string `json:"cores"`
	Memory    string `json:"memory"`
	OS        string `json:"os"`
	GoVersion string `json:"go_version"`
}

type backendInfo struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	Description  string `json:"description"`
	Experimental bool   `json:"experimental,omitempty"`
}

type scenarioResult struct {
	Backend      string       `json:"backend"`
	SuccessCount int          `json:"success_count"`
	FailureCount int          `json:"failure_count"`
	SearchMode   string       `json:"search_mode,omitempty"`
	Stats        latencyStats `json:"stats"`
}

type scenarioReport struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Results     []scenarioResult `json:"results"`
}

type benchmarkReport struct {
	GeneratedAt string           `json:"generated_at"`
	Runs        int              `json:"runs"`
	Machine     machineInfo      `json:"machine"`
	Fixture     fixtureSummary   `json:"fixture"`
	Backends    []backendInfo    `json:"backends"`
	Scenarios   []scenarioReport `json:"scenarios"`
}

type benchmarkFixture struct {
	Files                gbfs.InitialFiles
	Summary              fixtureSummary
	BaseRoot             string
	BaseSummary          fixtureSummary
	BaseDirectoryCount   int
	BaseBulkReadPaths    []string
	BaseBulkReadBytes    int64
	SearchLiteral        string
	SearchHitCount       int
	RenameSource         string
	RenameTarget         string
	RenameBody           string
	RewriteTarget        string
	RewriteBody          string
	RemoveTarget         string
	CreateTarget         string
	CreateBody           string
	AgenticRoot          string
	AgenticSummary       fixtureSummary
	AgenticTimeoutTerm   string
	AgenticRollbackTerm  string
	AgenticMatchCount    int
	AgenticLogPaths      []string
	AgenticBadJobs       int
	AgenticManifestPath  string
	AgenticTier1Services int
	KernelRoot           string
	KernelSummary        fixtureSummary
	KernelQueries        []literalBenchmarkQuery
}

type literalBenchmarkQuery struct {
	Label   string
	Literal string
	Count   int
}

type benchmarkEnv struct {
	tmpRoot          string
	fixture          benchmarkFixture
	hostFixtureRoot  string
	sqliteTemplateDB string
}

type workloadObservation struct {
	SearchMode string
}

type workloadTimingMode string

const (
	workloadTimingCold workloadTimingMode = "cold"
	workloadTimingWarm workloadTimingMode = "warm"
)

type workloadConfig struct {
	Name        string
	Description string
	Timing      workloadTimingMode
	Run         func(context.Context, gbfs.FileSystem, *benchmarkFixture) (workloadObservation, error)
}

type backendConfig struct {
	Info              backendInfo
	ExpectIndexedMode bool
	Prepare           func(context.Context, *benchmarkEnv) (gbfs.Factory, func() error, error)
}

func main() {
	if err := runMain(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "bench-fs: %v\n", err)
		os.Exit(1)
	}
}

func runMain(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	tmpRoot, err := os.MkdirTemp("", "gbash-bench-fs-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpRoot); err != nil {
			_, _ = fmt.Fprintf(stderr, "bench-fs: remove temp dir: %v\n", err)
		}
	}()

	env, err := prepareBenchmarkEnv(ctx, tmpRoot, opts)
	if err != nil {
		return err
	}
	backends := benchmarkBackends()
	scenarios := benchmarkScenarios(opts.FixtureProfile)

	report := benchmarkReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Runs:        opts.Runs,
		Machine:     collectMachineInfo(ctx),
		Fixture:     env.fixture.Summary,
	}
	for _, backend := range backends {
		report.Backends = append(report.Backends, backend.Info)
	}
	report.Scenarios = make([]scenarioReport, 0, len(scenarios))
	for _, scenario := range scenarios {
		report.Scenarios = append(report.Scenarios, scenarioReport{
			Name:        scenario.Name,
			Description: scenario.Description,
		})
	}
	for _, backend := range backends {
		for i, scenario := range scenarios {
			result, err := runScenarioTrials(ctx, backend, scenario, env, opts.Runs)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", scenario.Name, backend.Info.Name, err)
			}
			report.Scenarios[i].Results = append(report.Scenarios[i].Results, result)
		}
		if err := env.releaseBackendResources(backend.Info.Name); err != nil {
			return fmt.Errorf("release %s resources: %w", backend.Info.Name, err)
		}
	}

	if _, err := io.WriteString(stdout, renderTextReport(&report)); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if opts.JSONOut != "" {
		if err := writeJSONReport(opts.JSONOut, &report); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "wrote JSON report to %s\n", opts.JSONOut)
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("bench-fs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&opts.Runs, "runs", defaultRuns, "number of timed trials per backend and scenario")
	fs.StringVar(&opts.JSONOut, "json-out", "", "optional path to write a JSON report")
	fs.StringVar(&opts.FixtureProfile, "fixture-profile", fixtureProfileSynthetic, "fixture profile: synthetic or linux-kernel")
	fs.StringVar(&opts.KernelCacheDir, "kernel-cache-dir", "", "optional cache directory for the pinned Linux kernel archive")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if opts.Runs <= 0 {
		return options{}, fmt.Errorf("--runs must be greater than zero")
	}
	opts.JSONOut = strings.TrimSpace(opts.JSONOut)
	opts.FixtureProfile = strings.TrimSpace(opts.FixtureProfile)
	opts.KernelCacheDir = strings.TrimSpace(opts.KernelCacheDir)
	switch opts.FixtureProfile {
	case fixtureProfileSynthetic, fixtureProfileLinuxKernel:
	default:
		return options{}, fmt.Errorf("--fixture-profile must be %q or %q", fixtureProfileSynthetic, fixtureProfileLinuxKernel)
	}
	if opts.FixtureProfile == fixtureProfileLinuxKernel && opts.KernelCacheDir == "" {
		opts.KernelCacheDir = defaultKernelCacheDir()
	}
	return opts, nil
}

func prepareBenchmarkEnv(ctx context.Context, tmpRoot string, opts options) (*benchmarkEnv, error) {
	fixture, err := buildFixtureForProfile(ctx, opts)
	if err != nil {
		return nil, err
	}

	return &benchmarkEnv{
		tmpRoot: tmpRoot,
		fixture: fixture,
	}, nil
}

func (env *benchmarkEnv) ensureHostFixture() (string, error) {
	if env.hostFixtureRoot != "" {
		return env.hostFixtureRoot, nil
	}
	hostFixtureRoot := filepath.Join(env.tmpRoot, "host-fixture")
	if err := materializeHostFixture(hostFixtureRoot, env.fixture.Files); err != nil {
		return "", fmt.Errorf("materialize host fixture: %w", err)
	}
	env.hostFixtureRoot = hostFixtureRoot
	return hostFixtureRoot, nil
}

func (env *benchmarkEnv) ensureSQLiteTemplate(ctx context.Context) (string, error) {
	if env.sqliteTemplateDB != "" {
		return env.sqliteTemplateDB, nil
	}
	sqliteTemplateDB := filepath.Join(env.tmpRoot, "sqlite-template.db")
	if err := sqlitefs.SeedTemplate(ctx, sqliteTemplateDB, env.fixture.Files); err != nil {
		return "", fmt.Errorf("seed sqlite template: %w", err)
	}
	env.sqliteTemplateDB = sqliteTemplateDB
	return sqliteTemplateDB, nil
}

func (env *benchmarkEnv) releaseBackendResources(backend string) error {
	switch backend {
	case "overlay":
		if env.hostFixtureRoot == "" {
			return nil
		}
		if err := os.RemoveAll(env.hostFixtureRoot); err != nil {
			return err
		}
		env.hostFixtureRoot = ""
	case "sqlite":
		if env.sqliteTemplateDB == "" {
			return nil
		}
		if err := os.Remove(env.sqliteTemplateDB); err != nil && !os.IsNotExist(err) {
			return err
		}
		env.sqliteTemplateDB = ""
	}
	return nil
}

func buildFixtureForProfile(ctx context.Context, opts options) (benchmarkFixture, error) {
	switch opts.FixtureProfile {
	case fixtureProfileSynthetic:
		return buildBenchmarkFixture(), nil
	case fixtureProfileLinuxKernel:
		return buildLinuxKernelBenchmarkFixture(ctx, opts.KernelCacheDir)
	default:
		return benchmarkFixture{}, fmt.Errorf("unsupported fixture profile %q", opts.FixtureProfile)
	}
}

func benchmarkBackends() []backendConfig {
	return []backendConfig{
		{
			Info: backendInfo{
				Name:        "memory",
				Label:       "memory",
				Description: "Core in-memory sandbox filesystem seeded with the benchmark fixture.",
			},
			Prepare: func(_ context.Context, env *benchmarkEnv) (gbfs.Factory, func() error, error) {
				return gbfs.SeededMemory(env.fixture.Files), noopCleanup, nil
			},
		},
		{
			Info: backendInfo{
				Name:        "overlay",
				Label:       "overlay",
				Description: "Core read-only host workspace with an in-memory writable upper layer.",
			},
			Prepare: func(_ context.Context, env *benchmarkEnv) (gbfs.Factory, func() error, error) {
				hostFixtureRoot, err := env.ensureHostFixture()
				if err != nil {
					return nil, nil, err
				}
				return gbfs.Overlay(gbfs.Host(gbfs.HostOptions{
					Root:        hostFixtureRoot,
					VirtualRoot: virtualWorkspace,
				})), noopCleanup, nil
			},
		},
		{
			Info: backendInfo{
				Name:         "sqlite",
				Label:        "sqlite",
				Description:  "Experimental SQLite-backed example filesystem using a host database file.",
				Experimental: true,
			},
			Prepare: func(ctx context.Context, env *benchmarkEnv) (gbfs.Factory, func() error, error) {
				templatePath, err := env.ensureSQLiteTemplate(ctx)
				if err != nil {
					return nil, nil, err
				}
				dbPath, err := cloneSQLiteTemplate(ctx, env.tmpRoot, templatePath)
				if err != nil {
					return nil, nil, err
				}
				return sqlitefs.Factory{DBPath: dbPath}, func() error { return os.Remove(dbPath) }, nil
			},
		},
		{
			Info: backendInfo{
				Name:         "bleve",
				Label:        "bleve",
				Description:  "Experimental Bleve-backed example filesystem over seeded in-memory data.",
				Experimental: true,
			},
			ExpectIndexedMode: true,
			Prepare: func(_ context.Context, env *benchmarkEnv) (gbfs.Factory, func() error, error) {
				return blevefs.NewFactory(gbfs.SeededMemory(env.fixture.Files)), noopCleanup, nil
			},
		},
	}
}

func benchmarkScenarios(profile string) []workloadConfig {
	if profile == fixtureProfileLinuxKernel {
		return []workloadConfig{
			{
				Name:        "kernel_search_cold",
				Description: "Cold Linux kernel search benchmark over a pinned Linux 6.19.8 source subset built from approximately 200 MiB of text files. The timed window includes filesystem creation plus any index build or load cost.",
				Timing:      workloadTimingCold,
				Run:         runKernelSearch,
			},
			{
				Name:        "kernel_search_warm",
				Description: "Warm Linux kernel search benchmark over the same corpus. Filesystem creation and any index build or load happen before timing, so the timed window measures only the inventory and literal searches.",
				Timing:      workloadTimingWarm,
				Run:         runKernelSearch,
			},
		}
	}

	return []workloadConfig{
		{
			Name:        "metadata_walk",
			Description: "Recursive ReadDir and Lstat traversal across the synthetic benchmark tree at /workspace/base.",
			Timing:      workloadTimingCold,
			Run:         runMetadataWalk,
		},
		{
			Name:        "bulk_read",
			Description: "Open and read the full synthetic benchmark workload under /workspace/base, verifying total bytes.",
			Timing:      workloadTimingCold,
			Run:         runBulkRead,
		},
		{
			Name:        "mutation_roundtrip",
			Description: "Create, overwrite, rename, and remove files inside /workspace/base with verification.",
			Timing:      workloadTimingCold,
			Run:         runMutationRoundTrip,
		},
		{
			Name:        "literal_search",
			Description: "Indexed-or-scan literal search over /workspace/base using the full-text example path when available.",
			Timing:      workloadTimingCold,
			Run:         runLiteralSearch,
		},
		{
			Name:        "agentic_search",
			Description: "Agentic-style mixed workload under /workspace/agentic: inventory, indexed literal search, JSONL status counts, and manifest inspection.",
			Timing:      workloadTimingCold,
			Run:         runAgenticSearch,
		},
	}
}

func runScenarioTrials(ctx context.Context, backend backendConfig, scenario workloadConfig, env *benchmarkEnv, runs int) (scenarioResult, error) {
	durations := make([]time.Duration, 0, runs)
	searchMode := ""

	for range runs {
		factory, cleanup, err := backend.Prepare(ctx, env)
		if err != nil {
			return scenarioResult{}, err
		}
		if cleanup == nil {
			cleanup = noopCleanup
		}

		var (
			fsys        gbfs.FileSystem
			observation workloadObservation
			runErr      error
			start       time.Time
		)
		switch scenario.Timing {
		case "", workloadTimingCold:
			start = time.Now()
			fsys, err = factory.New(ctx)
			if err != nil {
				_ = cleanup()
				return scenarioResult{}, err
			}
			observation, runErr = scenario.Run(ctx, fsys, &env.fixture)
		case workloadTimingWarm:
			fsys, err = factory.New(ctx)
			if err != nil {
				_ = cleanup()
				return scenarioResult{}, err
			}
			start = time.Now()
			observation, runErr = scenario.Run(ctx, fsys, &env.fixture)
		default:
			_ = cleanup()
			return scenarioResult{}, fmt.Errorf("unsupported workload timing mode %q", scenario.Timing)
		}
		duration := time.Since(start)
		closeErr := closeIfPossible(fsys)
		cleanupErr := cleanup()

		if runErr != nil {
			return scenarioResult{}, runErr
		}
		if closeErr != nil {
			return scenarioResult{}, closeErr
		}
		if cleanupErr != nil {
			return scenarioResult{}, cleanupErr
		}

		if observation.SearchMode != "" {
			if observation.SearchMode == "index" && !backend.ExpectIndexedMode {
				return scenarioResult{}, fmt.Errorf("unexpected indexed search mode")
			}
			if observation.SearchMode == "scan" && backend.ExpectIndexedMode {
				return scenarioResult{}, fmt.Errorf("expected indexed search mode")
			}
			if searchMode == "" {
				searchMode = observation.SearchMode
			} else if searchMode != observation.SearchMode {
				return scenarioResult{}, fmt.Errorf("search mode changed across trials: %q vs %q", searchMode, observation.SearchMode)
			}
		}

		durations = append(durations, duration)
	}

	stats, ok := summarizeDurations(durations)
	if !ok {
		return scenarioResult{}, fmt.Errorf("no durations recorded")
	}
	return scenarioResult{
		Backend:      backend.Info.Name,
		SuccessCount: runs,
		FailureCount: 0,
		SearchMode:   searchMode,
		Stats:        stats,
	}, nil
}

func buildBenchmarkFixture() benchmarkFixture {
	const (
		sourceFiles      = 180
		docsFiles        = 24
		reportFiles      = 12
		logFiles         = 24
		noteFiles        = 12
		agenticCodeFiles = 240
		agenticDocsFiles = 24
		agenticCSVFiles  = 12
		agenticLogFiles  = 12
		agenticJSONFiles = 12
		timeoutHits      = 36
		rollbackHits     = 12
		badJobsPerLog    = 2
	)

	files := make(gbfs.InitialFiles, sourceFiles+docsFiles+reportFiles+logFiles+noteFiles+agenticCodeFiles+agenticDocsFiles+agenticCSVFiles+agenticLogFiles+agenticJSONFiles)
	summary := fixtureSummary{}
	baseSummary := fixtureSummary{}
	agenticSummary := fixtureSummary{}
	baseDirs := map[string]struct{}{baseWorkspaceRoot: {}}
	modTime := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	searchHitCount := 0
	agenticMatchCount := 0

	addFile := func(name, body string, subtreeSummary *fixtureSummary, subtreeRoot string, subtreeDirs map[string]struct{}) {
		files[name] = gbfs.InitialFile{
			Content: []byte(body),
			Mode:    0o644,
			ModTime: modTime,
		}
		summary.FileCount++
		summary.TotalBytes += int64(len(body))
		subtreeSummary.FileCount++
		subtreeSummary.TotalBytes += int64(len(body))
		if subtreeDirs == nil {
			return
		}
		for dir := path.Dir(name); dir != "/" && dir != "."; dir = path.Dir(dir) {
			subtreeDirs[dir] = struct{}{}
			if dir == subtreeRoot {
				break
			}
		}
	}

	for i := range sourceFiles {
		var body strings.Builder
		fmt.Fprintf(&body, "package pkg%02d\n\n", i%12)
		if i < 36 {
			fmt.Fprintf(&body, "// %s source-%03d\n", searchLiteral, i)
			searchHitCount++
		}
		for line := range 14 {
			fmt.Fprintf(&body, "func File%03dLine%02d() string { return \"benchmark-%03d-%02d\" }\n", i, line, i, line)
		}
		addFile(fmt.Sprintf("%s/src/pkg%02d/file%03d.go", baseWorkspaceRoot, i%12, i), body.String(), &baseSummary, baseWorkspaceRoot, baseDirs)
	}

	for i := range docsFiles {
		body := fmt.Sprintf("# Runbook %02d\nowner=team-%02d\nnote=follow-up-%02d\n", i, i%6, i)
		if i < 12 {
			body += fmt.Sprintf("search=%s-doc-%02d\n", searchLiteral, i)
			searchHitCount++
		}
		body += strings.Repeat("stability-check\n", 10)
		addFile(fmt.Sprintf("%s/docs/runbook%02d.md", baseWorkspaceRoot, i), body, &baseSummary, baseWorkspaceRoot, baseDirs)
	}

	for i := range reportFiles {
		var body strings.Builder
		body.WriteString("service,owner,tier,score\n")
		for row := range 10 {
			fmt.Fprintf(&body, "svc-%02d,team-%02d,%d,%d\n", i*10+row, (i+row)%4, 1+(row%3), 80+row)
		}
		addFile(fmt.Sprintf("%s/reports/summary%02d.csv", baseWorkspaceRoot, i), body.String(), &baseSummary, baseWorkspaceRoot, baseDirs)
	}

	for i := range logFiles {
		var body strings.Builder
		for row := range 30 {
			fmt.Fprintf(&body, "ts=%02d:%02d level=info batch=%02d row=%02d message=steady-state\n", i, row, i, row)
		}
		if i < 12 {
			fmt.Fprintf(&body, "level=warn marker=%s-log-%02d\n", searchLiteral, i)
			searchHitCount++
		}
		addFile(fmt.Sprintf("%s/logs/batch%02d.log", baseWorkspaceRoot, i), body.String(), &baseSummary, baseWorkspaceRoot, baseDirs)
	}

	for i := range noteFiles {
		body := fmt.Sprintf("note-%02d\n%s\n", i, strings.Repeat("analysis ", 30))
		addFile(fmt.Sprintf("%s/notes/topic%02d.txt", baseWorkspaceRoot, i), body, &baseSummary, baseWorkspaceRoot, baseDirs)
	}

	for i := range agenticCodeFiles {
		content := fmt.Sprintf("package pkg%02d\n", i%12)
		if i < timeoutHits {
			content += fmt.Sprintf("// TODO: investigate timeout regression %03d\n", i)
			agenticMatchCount++
		}
		content += fmt.Sprintf("func File%03d() int { return %d }\n", i, i)
		addFile(fmt.Sprintf("%s/src/pkg%02d/file%03d.go", agenticWorkspaceRoot, i%12, i), content, &agenticSummary, agenticWorkspaceRoot, nil)
	}

	for i := range agenticDocsFiles {
		content := fmt.Sprintf("# Runbook %02d\n", i)
		if i < rollbackHits {
			content += fmt.Sprintf("rollback dep-%04d after on-call review\n", 1000+i)
			agenticMatchCount++
		} else {
			content += "normal steady-state notes\n"
		}
		addFile(fmt.Sprintf("%s/docs/runbook%02d.md", agenticWorkspaceRoot, i), content, &agenticSummary, agenticWorkspaceRoot, nil)
	}

	for i := range agenticCSVFiles {
		content := fmt.Sprintf("service,owner,tier\nsvc-%02d,team-%02d,%d\n", i, i%4, 2+(i%2))
		addFile(fmt.Sprintf("%s/reports/summary%02d.csv", agenticWorkspaceRoot, i), content, &agenticSummary, agenticWorkspaceRoot, nil)
	}

	agenticLogPaths := make([]string, 0, agenticLogFiles)
	for i := range agenticLogFiles {
		var body strings.Builder
		for j := range 5 {
			status := "ok"
			errMsg := ""
			if j < badJobsPerLog {
				status = "failed"
				if j == 0 {
					errMsg = "database timeout"
					agenticMatchCount++
				} else {
					status = "slow"
					errMsg = "retry budget warning"
				}
			}
			fmt.Fprintf(
				&body,
				"{\"service\":\"svc-%02d\",\"job\":\"job-%d\",\"status\":\"%s\",\"error\":\"%s\",\"duration_ms\":%d}\n",
				i,
				j,
				status,
				errMsg,
				1000+(i*10)+j,
			)
		}
		logPath := fmt.Sprintf("%s/logs/batch%02d.jsonl", agenticWorkspaceRoot, i)
		addFile(logPath, body.String(), &agenticSummary, agenticWorkspaceRoot, nil)
		agenticLogPaths = append(agenticLogPaths, logPath)
	}

	services := `{"services":[
{"name":"checkout","tier":1},
{"name":"billing","tier":1},
{"name":"search","tier":1},
{"name":"worker","tier":1},
{"name":"catalog","tier":1},
{"name":"gateway","tier":1},
{"name":"auth","tier":1},
{"name":"payments","tier":1},
{"name":"profiles","tier":2},
{"name":"analytics","tier":2},
{"name":"notifier","tier":3},
{"name":"support","tier":3}
]}
`
	agenticManifestPath := agenticWorkspaceRoot + "/manifests/services.json"
	addFile(agenticManifestPath, services, &agenticSummary, agenticWorkspaceRoot, nil)

	for i := 1; i < agenticJSONFiles; i++ {
		content := fmt.Sprintf("{\"manifest\":\"meta-%02d\",\"team\":\"ops\",\"enabled\":%t}\n", i, i%2 == 0)
		addFile(fmt.Sprintf("%s/manifests/meta%02d.json", agenticWorkspaceRoot, i), content, &agenticSummary, agenticWorkspaceRoot, nil)
	}

	bulkReadPaths := make([]string, 0, baseSummary.FileCount)
	for name := range files {
		if strings.HasPrefix(name, baseWorkspaceRoot+"/") {
			bulkReadPaths = append(bulkReadPaths, name)
		}
	}
	slices.Sort(bulkReadPaths)
	slices.Sort(agenticLogPaths)

	return benchmarkFixture{
		Files:                files,
		Summary:              summary,
		BaseRoot:             baseWorkspaceRoot,
		BaseSummary:          baseSummary,
		BaseDirectoryCount:   len(baseDirs),
		BaseBulkReadPaths:    bulkReadPaths,
		BaseBulkReadBytes:    baseSummary.TotalBytes,
		SearchLiteral:        searchLiteral,
		SearchHitCount:       searchHitCount,
		RenameSource:         mutationRenameSrc,
		RenameTarget:         mutationRenameDst,
		RenameBody:           string(files[mutationRenameSrc].Content),
		RewriteTarget:        mutationRewritePath,
		RewriteBody:          mutationRewriteBody,
		RemoveTarget:         mutationRemovePath,
		CreateTarget:         mutationCreatePath,
		CreateBody:           mutationCreateBody,
		AgenticRoot:          agenticWorkspaceRoot,
		AgenticSummary:       agenticSummary,
		AgenticTimeoutTerm:   agenticTimeoutTerm,
		AgenticRollbackTerm:  agenticRollbackTerm,
		AgenticMatchCount:    agenticMatchCount,
		AgenticLogPaths:      agenticLogPaths,
		AgenticBadJobs:       agenticLogFiles * badJobsPerLog,
		AgenticManifestPath:  agenticManifestPath,
		AgenticTier1Services: 8,
	}
}

func buildLinuxKernelBenchmarkFixture(ctx context.Context, cacheDir string) (benchmarkFixture, error) {
	archiveReader, err := openLinuxKernelArchive(ctx, cacheDir)
	if err != nil {
		return benchmarkFixture{}, err
	}
	defer func() { _ = archiveReader.Close() }()

	xzReader, err := xz.NewReader(archiveReader)
	if err != nil {
		return benchmarkFixture{}, fmt.Errorf("open xz reader: %w", err)
	}

	tarReader := tar.NewReader(xzReader)
	modTime := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	fixture := benchmarkFixture{
		Files:         make(gbfs.InitialFiles),
		KernelRoot:    kernelWorkspaceRoot,
		KernelQueries: linuxKernelQueryTemplates(),
	}

	for fixture.KernelSummary.TotalBytes < linuxKernelTargetBytes {
		if err := ctx.Err(); err != nil {
			return benchmarkFixture{}, err
		}

		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return benchmarkFixture{}, fmt.Errorf("read kernel tar entry: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}

		relative, ok := linuxKernelRelativePath(header.Name)
		if !ok || relative == "" {
			continue
		}

		data, err := io.ReadAll(tarReader)
		if err != nil {
			return benchmarkFixture{}, fmt.Errorf("read kernel file %q: %w", header.Name, err)
		}
		if !looksLikeSearchableText(data) {
			continue
		}
		if fixture.KernelSummary.TotalBytes+int64(len(data)) > linuxKernelTargetBytes && fixture.KernelSummary.FileCount > 0 {
			continue
		}

		virtualPath := path.Join(kernelWorkspaceRoot, relative)
		mode := os.FileMode(header.Mode).Perm()
		if mode == 0 {
			mode = 0o644
		}
		fixture.Files[virtualPath] = gbfs.InitialFile{
			Content: data,
			Mode:    mode,
			ModTime: modTime,
		}
		fixture.Summary.FileCount++
		fixture.Summary.TotalBytes += int64(len(data))
		fixture.KernelSummary.FileCount++
		fixture.KernelSummary.TotalBytes += int64(len(data))
		for i := range fixture.KernelQueries {
			if bytes.Contains(data, []byte(fixture.KernelQueries[i].Literal)) {
				fixture.KernelQueries[i].Count++
			}
		}
	}

	if fixture.KernelSummary.FileCount == 0 {
		return benchmarkFixture{}, fmt.Errorf("kernel fixture is empty after filtering %s", linuxKernelArchiveName)
	}
	return fixture, nil
}

func linuxKernelQueryTemplates() []literalBenchmarkQuery {
	return []literalBenchmarkQuery{
		{Label: "module_init", Literal: "module_init"},
		{Label: "EXPORT_SYMBOL_GPL", Literal: "EXPORT_SYMBOL_GPL"},
		{Label: "spin_lock_irqsave", Literal: "spin_lock_irqsave"},
	}
}

func linuxKernelRelativePath(name string) (string, bool) {
	cleaned := strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(name)), "/")
	parts := strings.Split(cleaned, "/")
	if len(parts) < 2 {
		return "", false
	}
	return path.Join(parts[1:]...), true
}

func looksLikeSearchableText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sample := data
	if len(sample) > 8*1024 {
		sample = sample[:8*1024]
	}
	controlBytes := 0
	for _, b := range sample {
		if b == 0 {
			return false
		}
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			controlBytes++
		}
	}
	return controlBytes*20 <= len(sample)
}

func openLinuxKernelArchive(ctx context.Context, cacheDir string) (io.ReadCloser, error) {
	if cacheDir == kernelCacheModeStream {
		return openStreamingLinuxKernelArchive(ctx)
	}

	archivePath, err := ensureLinuxKernelArchive(ctx, cacheDir)
	if err != nil {
		return nil, err
	}
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open kernel archive: %w", err)
	}
	return archiveFile, nil
}

func ensureLinuxKernelArchive(ctx context.Context, cacheDir string) (string, error) {
	if cacheDir == "" {
		cacheDir = defaultKernelCacheDir()
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create kernel cache dir: %w", err)
	}

	archivePath := filepath.Join(cacheDir, linuxKernelArchiveName)
	ok, err := fileHasSHA256(archivePath, linuxKernelArchiveSHA256)
	if err == nil && ok {
		return archivePath, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	tmpPath := archivePath + ".tmp"
	if err := downloadFileWithSHA256(ctx, linuxKernelArchiveURL, tmpPath, linuxKernelArchiveSHA256); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, archivePath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename kernel archive: %w", err)
	}
	return archivePath, nil
}

func openStreamingLinuxKernelArchive(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, linuxKernelArchiveURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create kernel download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download kernel archive: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download kernel archive: unexpected status %s", resp.Status)
	}
	return &sha256VerifyingReadCloser{
		src:  resp.Body,
		hash: sha256.New(),
		want: linuxKernelArchiveSHA256,
	}, nil
}

func downloadFileWithSHA256(ctx context.Context, url, target, wantSHA256 string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("create kernel download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download kernel archive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download kernel archive: unexpected status %s", resp.Status)
	}

	file, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("create kernel archive temp file: %w", err)
	}
	defer func() { _ = file.Close() }()

	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, digest), resp.Body); err != nil {
		return fmt.Errorf("write kernel archive: %w", err)
	}
	gotSHA256 := hex.EncodeToString(digest.Sum(nil))
	if gotSHA256 != wantSHA256 {
		return fmt.Errorf("kernel archive sha256 = %s, want %s", gotSHA256, wantSHA256)
	}
	return nil
}

func fileHasSHA256(pathValue, wantSHA256 string) (bool, error) {
	file, err := os.Open(pathValue)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return false, fmt.Errorf("hash %s: %w", pathValue, err)
	}
	return hex.EncodeToString(digest.Sum(nil)) == wantSHA256, nil
}

type sha256VerifyingReadCloser struct {
	src      io.ReadCloser
	hash     hash.Hash
	want     string
	eofSeen  bool
	finalErr error
}

func (r *sha256VerifyingReadCloser) Read(p []byte) (int, error) {
	if r.finalErr != nil {
		err := r.finalErr
		r.finalErr = nil
		return 0, err
	}
	if r.eofSeen {
		return 0, io.EOF
	}

	n, err := r.src.Read(p)
	if n > 0 {
		_, _ = r.hash.Write(p[:n])
	}
	if err == io.EOF {
		r.eofSeen = true
		got := hex.EncodeToString(r.hash.Sum(nil))
		if got != r.want {
			r.finalErr = fmt.Errorf("kernel archive sha256 = %s, want %s", got, r.want)
			if n > 0 {
				return n, nil
			}
			err := r.finalErr
			r.finalErr = nil
			return 0, err
		}
	}
	return n, err
}

func (r *sha256VerifyingReadCloser) Close() error {
	return r.src.Close()
}

func defaultKernelCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		return filepath.Join(os.TempDir(), "gbash-bench-fs-cache")
	}
	return filepath.Join(cacheDir, "gbash", "bench-fs")
}

func materializeHostFixture(root string, files gbfs.InitialFiles) error {
	for name, initial := range files {
		rel := strings.TrimPrefix(name, virtualWorkspace)
		if rel == "" || rel == name {
			return fmt.Errorf("fixture path %q is outside %s", name, virtualWorkspace)
		}
		target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(rel, "/")))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := initial.Mode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(target, initial.Content, mode); err != nil {
			return err
		}
		if !initial.ModTime.IsZero() {
			if err := os.Chtimes(target, initial.ModTime, initial.ModTime); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneSQLiteTemplate(ctx context.Context, tmpRoot, templatePath string) (string, error) {
	file, err := os.CreateTemp(tmpRoot, "sqlite-bench-*.db")
	if err != nil {
		return "", err
	}
	dbPath := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := copyFile(dbPath, templatePath); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return dbPath, nil
}

func runMetadataWalk(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	files, dirs, totalBytes, err := walkWorkspace(ctx, fsys, fixture.BaseRoot)
	if err != nil {
		return workloadObservation{}, err
	}
	if files != fixture.BaseSummary.FileCount {
		return workloadObservation{}, fmt.Errorf("walk file count = %d, want %d", files, fixture.BaseSummary.FileCount)
	}
	if dirs != fixture.BaseDirectoryCount {
		return workloadObservation{}, fmt.Errorf("walk dir count = %d, want %d", dirs, fixture.BaseDirectoryCount)
	}
	if totalBytes != fixture.BaseSummary.TotalBytes {
		return workloadObservation{}, fmt.Errorf("walk bytes = %d, want %d", totalBytes, fixture.BaseSummary.TotalBytes)
	}
	return workloadObservation{}, nil
}

func runBulkRead(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	var total int64
	for _, name := range fixture.BaseBulkReadPaths {
		data, err := readSandboxFile(ctx, fsys, name)
		if err != nil {
			return workloadObservation{}, err
		}
		total += int64(len(data))
	}
	if total != fixture.BaseBulkReadBytes {
		return workloadObservation{}, fmt.Errorf("bulk read bytes = %d, want %d", total, fixture.BaseBulkReadBytes)
	}
	return workloadObservation{}, nil
}

func runMutationRoundTrip(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	if err := writeSandboxFile(ctx, fsys, fixture.CreateTarget, fixture.CreateBody); err != nil {
		return workloadObservation{}, err
	}
	if err := writeSandboxFile(ctx, fsys, fixture.RewriteTarget, fixture.RewriteBody); err != nil {
		return workloadObservation{}, err
	}
	if err := fsys.Rename(ctx, fixture.RenameSource, fixture.RenameTarget); err != nil {
		return workloadObservation{}, err
	}
	if err := fsys.Remove(ctx, fixture.RemoveTarget, false); err != nil {
		return workloadObservation{}, err
	}

	if got, err := readSandboxText(ctx, fsys, fixture.CreateTarget); err != nil || got != fixture.CreateBody {
		if err != nil {
			return workloadObservation{}, err
		}
		return workloadObservation{}, fmt.Errorf("created file = %q, want %q", got, fixture.CreateBody)
	}
	if got, err := readSandboxText(ctx, fsys, fixture.RewriteTarget); err != nil || got != fixture.RewriteBody {
		if err != nil {
			return workloadObservation{}, err
		}
		return workloadObservation{}, fmt.Errorf("rewritten file = %q, want %q", got, fixture.RewriteBody)
	}
	if _, err := fsys.Stat(ctx, fixture.RenameSource); !isNotExist(err) {
		return workloadObservation{}, fmt.Errorf("stat old rename path error = %v, want not exist", err)
	}
	if got, err := readSandboxText(ctx, fsys, fixture.RenameTarget); err != nil || got != fixture.RenameBody {
		if err != nil {
			return workloadObservation{}, err
		}
		return workloadObservation{}, fmt.Errorf("renamed file = %q, want original body", got)
	}
	if _, err := fsys.Stat(ctx, fixture.RemoveTarget); !isNotExist(err) {
		return workloadObservation{}, fmt.Errorf("stat removed file error = %v, want not exist", err)
	}

	files, _, _, err := walkWorkspace(ctx, fsys, fixture.BaseRoot)
	if err != nil {
		return workloadObservation{}, err
	}
	if files != fixture.BaseSummary.FileCount {
		return workloadObservation{}, fmt.Errorf("post-mutation file count = %d, want %d", files, fixture.BaseSummary.FileCount)
	}
	return workloadObservation{}, nil
}

func runLiteralSearch(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	result, err := searchadapter.Search(ctx, fsys, &searchadapter.Query{
		Roots:         []string{fixture.BaseRoot},
		Literal:       fixture.SearchLiteral,
		IndexEligible: true,
	}, nil)
	if err != nil {
		return workloadObservation{}, err
	}
	if len(result.Hits) != fixture.SearchHitCount {
		return workloadObservation{}, fmt.Errorf("search hit count = %d, want %d", len(result.Hits), fixture.SearchHitCount)
	}
	mode := "scan"
	if result.UsedIndex {
		mode = "index"
	}
	return workloadObservation{SearchMode: mode}, nil
}

func runAgenticSearch(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	files, _, _, err := walkWorkspace(ctx, fsys, fixture.AgenticRoot)
	if err != nil {
		return workloadObservation{}, err
	}
	if files != fixture.AgenticSummary.FileCount {
		return workloadObservation{}, fmt.Errorf("agentic file count = %d, want %d", files, fixture.AgenticSummary.FileCount)
	}

	timeoutMatches, searchMode, err := countLiteralHits(ctx, fsys, fixture.AgenticRoot, fixture.AgenticTimeoutTerm)
	if err != nil {
		return workloadObservation{}, err
	}
	rollbackMatches, rollbackMode, err := countLiteralHits(ctx, fsys, fixture.AgenticRoot, fixture.AgenticRollbackTerm)
	if err != nil {
		return workloadObservation{}, err
	}
	if searchMode != rollbackMode {
		return workloadObservation{}, fmt.Errorf("agentic search mode mismatch: %q vs %q", searchMode, rollbackMode)
	}
	if timeoutMatches+rollbackMatches != fixture.AgenticMatchCount {
		return workloadObservation{}, fmt.Errorf("agentic match count = %d, want %d", timeoutMatches+rollbackMatches, fixture.AgenticMatchCount)
	}

	badJobs, err := countBadJobs(ctx, fsys, fixture.AgenticLogPaths)
	if err != nil {
		return workloadObservation{}, err
	}
	if badJobs != fixture.AgenticBadJobs {
		return workloadObservation{}, fmt.Errorf("agentic bad jobs = %d, want %d", badJobs, fixture.AgenticBadJobs)
	}

	tier1, err := countTier1Services(ctx, fsys, fixture.AgenticManifestPath)
	if err != nil {
		return workloadObservation{}, err
	}
	if tier1 != fixture.AgenticTier1Services {
		return workloadObservation{}, fmt.Errorf("agentic tier1 services = %d, want %d", tier1, fixture.AgenticTier1Services)
	}

	return workloadObservation{SearchMode: searchMode}, nil
}

func runKernelSearch(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	files, _, totalBytes, err := walkWorkspace(ctx, fsys, fixture.KernelRoot)
	if err != nil {
		return workloadObservation{}, err
	}
	if files != fixture.KernelSummary.FileCount {
		return workloadObservation{}, fmt.Errorf("kernel file count = %d, want %d", files, fixture.KernelSummary.FileCount)
	}
	if totalBytes != fixture.KernelSummary.TotalBytes {
		return workloadObservation{}, fmt.Errorf("kernel bytes = %d, want %d", totalBytes, fixture.KernelSummary.TotalBytes)
	}

	searchMode := ""
	for _, query := range fixture.KernelQueries {
		hits, mode, err := countLiteralHits(ctx, fsys, fixture.KernelRoot, query.Literal)
		if err != nil {
			return workloadObservation{}, err
		}
		if hits != query.Count {
			return workloadObservation{}, fmt.Errorf("%s hit count = %d, want %d", query.Label, hits, query.Count)
		}
		if searchMode == "" {
			searchMode = mode
			continue
		}
		if searchMode != mode {
			return workloadObservation{}, fmt.Errorf("kernel search mode mismatch: %q vs %q", searchMode, mode)
		}
	}

	return workloadObservation{SearchMode: searchMode}, nil
}

func countLiteralHits(ctx context.Context, fsys gbfs.FileSystem, root, literal string) (hitCount int, mode string, err error) {
	result, err := searchadapter.Search(ctx, fsys, &searchadapter.Query{
		Roots:         []string{root},
		Literal:       literal,
		IndexEligible: true,
	}, nil)
	if err != nil {
		return 0, "", err
	}
	mode = "scan"
	if result.UsedIndex {
		mode = "index"
	}
	return len(result.Hits), mode, nil
}

func countBadJobs(ctx context.Context, fsys gbfs.FileSystem, logPaths []string) (int, error) {
	type jobRecord struct {
		Status string `json:"status"`
	}

	badJobs := 0
	for _, logPath := range logPaths {
		data, err := readSandboxFile(ctx, fsys, logPath)
		if err != nil {
			return 0, err
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var record jobRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				return 0, fmt.Errorf("parse %s: %w", logPath, err)
			}
			if record.Status != "ok" {
				badJobs++
			}
		}
		if err := scanner.Err(); err != nil {
			return 0, err
		}
	}
	return badJobs, nil
}

func countTier1Services(ctx context.Context, fsys gbfs.FileSystem, manifestPath string) (int, error) {
	var manifest struct {
		Services []struct {
			Tier int `json:"tier"`
		} `json:"services"`
	}

	data, err := readSandboxFile(ctx, fsys, manifestPath)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return 0, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	tier1 := 0
	for _, service := range manifest.Services {
		if service.Tier == 1 {
			tier1++
		}
	}
	return tier1, nil
}

func walkWorkspace(ctx context.Context, fsys gbfs.FileSystem, root string) (files, dirs int, totalBytes int64, err error) {
	var walk func(string) error
	walk = func(current string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := fsys.Lstat(ctx, current)
		if err != nil {
			return err
		}
		if info.IsDir() {
			dirs++
			entries, err := fsys.ReadDir(ctx, current)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := walk(path.Join(current, entry.Name())); err != nil {
					return err
				}
			}
			return nil
		}
		files++
		totalBytes += info.Size()
		return nil
	}
	if err := walk(root); err != nil {
		return 0, 0, 0, err
	}
	return files, dirs, totalBytes, nil
}

func writeSandboxFile(ctx context.Context, fsys gbfs.FileSystem, name, body string) error {
	if err := fsys.MkdirAll(ctx, path.Dir(name), 0o755); err != nil {
		return err
	}
	file, err := fsys.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readSandboxFile(ctx context.Context, fsys gbfs.FileSystem, name string) ([]byte, error) {
	file, err := fsys.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func readSandboxText(ctx context.Context, fsys gbfs.FileSystem, name string) (string, error) {
	data, err := readSandboxFile(ctx, fsys, name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func isNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func closeIfPossible(fsys gbfs.FileSystem) error {
	type closer interface {
		Close() error
	}
	c, ok := fsys.(closer)
	if !ok {
		return nil
	}
	return c.Close()
}

func summarizeDurations(durations []time.Duration) (latencyStats, bool) {
	if len(durations) == 0 {
		return latencyStats{}, false
	}

	values := make([]time.Duration, len(durations))
	copy(values, durations)
	slices.Sort(values)

	stats := latencyStats{
		MinNanos: values[0].Nanoseconds(),
		P95Nanos: values[percentileIndex(len(values), 0.95)].Nanoseconds(),
	}
	mid := len(values) / 2
	if len(values)%2 == 0 {
		stats.MedianNanos = (values[mid-1] + values[mid]).Nanoseconds() / 2
	} else {
		stats.MedianNanos = values[mid].Nanoseconds()
	}
	return stats, true
}

func percentileIndex(length int, percentile float64) int {
	if length <= 1 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(length))) - 1
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}

func renderTextReport(report *benchmarkReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Filesystem backend benchmark\n")
	fmt.Fprintf(&b, "Generated: %s\n", report.GeneratedAt)
	fmt.Fprintf(&b, "Runs per scenario: %d\n", report.Runs)
	fmt.Fprintf(&b, "Fixture: %d files, %s\n", report.Fixture.FileCount, formatBytes(report.Fixture.TotalBytes))
	for _, scenario := range report.Scenarios {
		fmt.Fprintf(&b, "\n[%s]\n", scenario.Name)
		fmt.Fprintf(&b, "%s\n", scenario.Description)
		for _, result := range scenario.Results {
			fmt.Fprintf(
				&b,
				"  %-8s min=%-8s median=%-8s p95=%-8s",
				result.Backend,
				formatNanos(result.Stats.MinNanos),
				formatNanos(result.Stats.MedianNanos),
				formatNanos(result.Stats.P95Nanos),
			)
			if result.SearchMode != "" {
				fmt.Fprintf(&b, " search=%s", result.SearchMode)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func formatNanos(nanos int64) string {
	switch {
	case nanos >= 1_000_000_000:
		return fmt.Sprintf("%.2fs", float64(nanos)/1_000_000_000)
	case nanos >= 1_000_000:
		return fmt.Sprintf("%.1fms", float64(nanos)/1_000_000)
	case nanos >= 1_000:
		return fmt.Sprintf("%.1fµs", float64(nanos)/1_000)
	default:
		return fmt.Sprintf("%dns", nanos)
	}
}

func formatBytes(byteCount int64) string {
	switch {
	case byteCount >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GiB", float64(byteCount)/(1024*1024*1024))
	case byteCount >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(byteCount)/(1024*1024))
	case byteCount >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(byteCount)/1024)
	default:
		return fmt.Sprintf("%d B", byteCount)
	}
}

func writeJSONReport(target string, report *benchmarkReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create json dir: %w", err)
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	return nil
}

func collectMachineInfo(ctx context.Context) machineInfo {
	info := machineInfo{
		Model:     "Unknown",
		Chip:      "Unknown",
		Cores:     strconv.Itoa(goruntime.NumCPU()),
		Memory:    "Unknown",
		OS:        fmt.Sprintf("%s/%s", goruntime.GOOS, goruntime.GOARCH),
		GoVersion: fmt.Sprintf("%s %s/%s", goruntime.Version(), goruntime.GOOS, goruntime.GOARCH),
	}

	if goruntime.GOOS == "darwin" {
		if model := strings.TrimSpace(runCommand(ctx, "sysctl", "-n", "hw.model")); model != "" {
			info.Model = model
		}
		if chip := strings.TrimSpace(runCommand(ctx, "sysctl", "-n", "machdep.cpu.brand_string")); chip != "" {
			info.Chip = chip
		}
		if perf := parseInt(runCommand(ctx, "sysctl", "-n", "hw.perflevel0.physicalcpu")); perf > 0 {
			eff := parseInt(runCommand(ctx, "sysctl", "-n", "hw.perflevel1.physicalcpu"))
			total := perf + eff
			info.Cores = fmt.Sprintf("%d (%d performance + %d efficiency)", total, perf, eff)
		}
		if memBytes := parseInt64(runCommand(ctx, "sysctl", "-n", "hw.memsize")); memBytes > 0 {
			info.Memory = fmt.Sprintf("%d GB", memBytes/(1024*1024*1024))
		}
		productName := strings.TrimSpace(runCommand(ctx, "sw_vers", "-productName"))
		productVersion := strings.TrimSpace(runCommand(ctx, "sw_vers", "-productVersion"))
		if productName != "" || productVersion != "" {
			info.OS = strings.TrimSpace(productName + " " + productVersion)
		}
	}

	return info
}

func runCommand(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(output)
}

func parseInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func parseInt64(value string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return n
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

func noopCleanup() error { return nil }
