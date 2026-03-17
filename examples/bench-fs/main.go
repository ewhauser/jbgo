package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/examples/internal/sqlitefs"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/searchadapter"
)

const (
	defaultRuns         = 50
	virtualWorkspace    = "/workspace"
	searchLiteral       = "fulltext-benchmark-needle"
	mutationCreatePath  = "/workspace/scratch/session/output.txt"
	mutationCreateBody  = "created in benchmark mutation\n"
	mutationRewritePath = "/workspace/docs/runbook00.md"
	mutationRewriteBody = "rewritten benchmark doc\n"
	mutationRenameSrc   = "/workspace/reports/summary00.csv"
	mutationRenameDst   = "/workspace/reports/summary00-renamed.csv"
	mutationRemovePath  = "/workspace/logs/batch00.log"
)

type options struct {
	Runs    int
	JSONOut string
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
	Files          gbfs.InitialFiles
	Summary        fixtureSummary
	DirectoryCount int
	BulkReadPaths  []string
	BulkReadBytes  int64
	SearchLiteral  string
	SearchHitCount int
	RenameSource   string
	RenameTarget   string
	RenameBody     string
	RewriteTarget  string
	RewriteBody    string
	RemoveTarget   string
	CreateTarget   string
	CreateBody     string
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

type workloadConfig struct {
	Name        string
	Description string
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

	env, err := prepareBenchmarkEnv(ctx, tmpRoot)
	if err != nil {
		return err
	}
	backends := benchmarkBackends()
	scenarios := benchmarkScenarios()

	report := benchmarkReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Runs:        opts.Runs,
		Machine:     collectMachineInfo(ctx),
		Fixture:     env.fixture.Summary,
	}
	for _, backend := range backends {
		report.Backends = append(report.Backends, backend.Info)
	}
	for _, scenario := range scenarios {
		scenarioReport := scenarioReport{
			Name:        scenario.Name,
			Description: scenario.Description,
		}
		for _, backend := range backends {
			result, err := runScenarioTrials(ctx, backend, scenario, env, opts.Runs)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", scenario.Name, backend.Info.Name, err)
			}
			scenarioReport.Results = append(scenarioReport.Results, result)
		}
		report.Scenarios = append(report.Scenarios, scenarioReport)
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
	return opts, nil
}

func prepareBenchmarkEnv(ctx context.Context, tmpRoot string) (*benchmarkEnv, error) {
	fixture := buildBenchmarkFixture()

	hostFixtureRoot := filepath.Join(tmpRoot, "host-fixture")
	if err := materializeHostFixture(hostFixtureRoot, fixture.Files); err != nil {
		return nil, fmt.Errorf("materialize host fixture: %w", err)
	}

	sqliteTemplateDB := filepath.Join(tmpRoot, "sqlite-template.db")
	if err := sqlitefs.SeedTemplate(ctx, sqliteTemplateDB, fixture.Files); err != nil {
		return nil, fmt.Errorf("seed sqlite template: %w", err)
	}

	return &benchmarkEnv{
		tmpRoot:          tmpRoot,
		fixture:          fixture,
		hostFixtureRoot:  hostFixtureRoot,
		sqliteTemplateDB: sqliteTemplateDB,
	}, nil
}

func benchmarkBackends() []backendConfig {
	seededFactory := gbfs.SeededMemory(buildBenchmarkFixture().Files)
	return []backendConfig{
		{
			Info: backendInfo{
				Name:        "memory",
				Label:       "memory",
				Description: "Core in-memory sandbox filesystem seeded with the benchmark fixture.",
			},
			Prepare: func(context.Context, *benchmarkEnv) (gbfs.Factory, func() error, error) {
				return seededFactory, noopCleanup, nil
			},
		},
		{
			Info: backendInfo{
				Name:        "overlay",
				Label:       "overlay",
				Description: "Core read-only host workspace with an in-memory writable upper layer.",
			},
			Prepare: func(_ context.Context, env *benchmarkEnv) (gbfs.Factory, func() error, error) {
				return gbfs.Overlay(gbfs.Host(gbfs.HostOptions{
					Root:        env.hostFixtureRoot,
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
				dbPath, err := cloneSQLiteTemplate(ctx, env.tmpRoot, env.sqliteTemplateDB)
				if err != nil {
					return nil, nil, err
				}
				return sqlitefs.Factory{DBPath: dbPath}, func() error { return os.Remove(dbPath) }, nil
			},
		},
	}
}

func benchmarkScenarios() []workloadConfig {
	return []workloadConfig{
		{
			Name:        "metadata_walk",
			Description: "Recursive ReadDir and Lstat traversal across the canonical /workspace fixture.",
			Run:         runMetadataWalk,
		},
		{
			Name:        "bulk_read",
			Description: "Open and read the full fixture workload, verifying total bytes.",
			Run:         runBulkRead,
		},
		{
			Name:        "mutation_roundtrip",
			Description: "Create, overwrite, rename, and remove files inside /workspace with verification.",
			Run:         runMutationRoundTrip,
		},
		{
			Name:        "literal_search",
			Description: "Literal search over /workspace, using indexed search when a backend provides it.",
			Run:         runLiteralSearch,
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

		start := time.Now()
		fsys, err := factory.New(ctx)
		if err != nil {
			_ = cleanup()
			return scenarioResult{}, err
		}
		observation, runErr := scenario.Run(ctx, fsys, &env.fixture)
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
		sourceFiles = 180
		docsFiles   = 24
		reportFiles = 12
		logFiles    = 24
		noteFiles   = 12
	)

	files := make(gbfs.InitialFiles, sourceFiles+docsFiles+reportFiles+logFiles+noteFiles)
	dirs := map[string]struct{}{virtualWorkspace: {}}
	summary := fixtureSummary{}
	modTime := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	searchHitCount := 0

	addFile := func(name, body string) {
		files[name] = gbfs.InitialFile{
			Content: []byte(body),
			Mode:    0o644,
			ModTime: modTime,
		}
		summary.FileCount++
		summary.TotalBytes += int64(len(body))
		for dir := path.Dir(name); dir != "/" && dir != "."; dir = path.Dir(dir) {
			dirs[dir] = struct{}{}
			if dir == virtualWorkspace {
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
		addFile(fmt.Sprintf("/workspace/src/pkg%02d/file%03d.go", i%12, i), body.String())
	}

	for i := range docsFiles {
		body := fmt.Sprintf("# Runbook %02d\nowner=team-%02d\nnote=follow-up-%02d\n", i, i%6, i)
		if i < 12 {
			body += fmt.Sprintf("search=%s-doc-%02d\n", searchLiteral, i)
			searchHitCount++
		}
		body += strings.Repeat("stability-check\n", 10)
		addFile(fmt.Sprintf("/workspace/docs/runbook%02d.md", i), body)
	}

	for i := range reportFiles {
		var body strings.Builder
		body.WriteString("service,owner,tier,score\n")
		for row := range 10 {
			fmt.Fprintf(&body, "svc-%02d,team-%02d,%d,%d\n", i*10+row, (i+row)%4, 1+(row%3), 80+row)
		}
		addFile(fmt.Sprintf("/workspace/reports/summary%02d.csv", i), body.String())
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
		addFile(fmt.Sprintf("/workspace/logs/batch%02d.log", i), body.String())
	}

	for i := range noteFiles {
		body := fmt.Sprintf("note-%02d\n%s\n", i, strings.Repeat("analysis ", 30))
		addFile(fmt.Sprintf("/workspace/notes/topic%02d.txt", i), body)
	}

	bulkReadPaths := make([]string, 0, len(files))
	for name := range files {
		bulkReadPaths = append(bulkReadPaths, name)
	}
	slices.Sort(bulkReadPaths)

	return benchmarkFixture{
		Files:          files,
		Summary:        summary,
		DirectoryCount: len(dirs),
		BulkReadPaths:  bulkReadPaths,
		BulkReadBytes:  summary.TotalBytes,
		SearchLiteral:  searchLiteral,
		SearchHitCount: searchHitCount,
		RenameSource:   mutationRenameSrc,
		RenameTarget:   mutationRenameDst,
		RenameBody:     string(files[mutationRenameSrc].Content),
		RewriteTarget:  mutationRewritePath,
		RewriteBody:    mutationRewriteBody,
		RemoveTarget:   mutationRemovePath,
		CreateTarget:   mutationCreatePath,
		CreateBody:     mutationCreateBody,
	}
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
	files, dirs, totalBytes, err := walkWorkspace(ctx, fsys, virtualWorkspace)
	if err != nil {
		return workloadObservation{}, err
	}
	if files != fixture.Summary.FileCount {
		return workloadObservation{}, fmt.Errorf("walk file count = %d, want %d", files, fixture.Summary.FileCount)
	}
	if dirs != fixture.DirectoryCount {
		return workloadObservation{}, fmt.Errorf("walk dir count = %d, want %d", dirs, fixture.DirectoryCount)
	}
	if totalBytes != fixture.Summary.TotalBytes {
		return workloadObservation{}, fmt.Errorf("walk bytes = %d, want %d", totalBytes, fixture.Summary.TotalBytes)
	}
	return workloadObservation{}, nil
}

func runBulkRead(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	var total int64
	for _, name := range fixture.BulkReadPaths {
		data, err := readSandboxFile(ctx, fsys, name)
		if err != nil {
			return workloadObservation{}, err
		}
		total += int64(len(data))
	}
	if total != fixture.BulkReadBytes {
		return workloadObservation{}, fmt.Errorf("bulk read bytes = %d, want %d", total, fixture.BulkReadBytes)
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
		return workloadObservation{}, fmt.Errorf("stat old rename path error = %w, want not exist", err)
	}
	if got, err := readSandboxText(ctx, fsys, fixture.RenameTarget); err != nil || got != fixture.RenameBody {
		if err != nil {
			return workloadObservation{}, err
		}
		return workloadObservation{}, fmt.Errorf("renamed file = %q, want original body", got)
	}
	if _, err := fsys.Stat(ctx, fixture.RemoveTarget); !isNotExist(err) {
		return workloadObservation{}, fmt.Errorf("stat removed file error = %w, want not exist", err)
	}

	files, _, _, err := walkWorkspace(ctx, fsys, virtualWorkspace)
	if err != nil {
		return workloadObservation{}, err
	}
	if files != fixture.Summary.FileCount {
		return workloadObservation{}, fmt.Errorf("post-mutation file count = %d, want %d", files, fixture.Summary.FileCount)
	}
	return workloadObservation{}, nil
}

func runLiteralSearch(ctx context.Context, fsys gbfs.FileSystem, fixture *benchmarkFixture) (workloadObservation, error) {
	result, err := searchadapter.Search(ctx, fsys, &searchadapter.Query{
		Roots:         []string{virtualWorkspace},
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

func formatBytes(bytes int64) string {
	switch {
	case bytes >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/(1024*1024*1024))
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
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
