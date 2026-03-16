package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestBuildBenchmarkFixture(t *testing.T) {
	fixture := buildBenchmarkFixture()

	if fixture.Summary.FileCount == 0 {
		t.Fatal("fixture file count = 0, want non-zero")
	}
	if fixture.Summary.TotalBytes == 0 {
		t.Fatal("fixture total bytes = 0, want non-zero")
	}
	if fixture.SearchHitCount == 0 {
		t.Fatal("fixture search hit count = 0, want non-zero")
	}
	if fixture.BaseRoot != baseWorkspaceRoot {
		t.Fatalf("BaseRoot = %q, want %q", fixture.BaseRoot, baseWorkspaceRoot)
	}
	if fixture.AgenticRoot != agenticWorkspaceRoot {
		t.Fatalf("AgenticRoot = %q, want %q", fixture.AgenticRoot, agenticWorkspaceRoot)
	}
	if fixture.BaseSummary.FileCount != 252 {
		t.Fatalf("BaseSummary.FileCount = %d, want 252", fixture.BaseSummary.FileCount)
	}
	if fixture.AgenticSummary.FileCount != 300 {
		t.Fatalf("AgenticSummary.FileCount = %d, want 300", fixture.AgenticSummary.FileCount)
	}
	if fixture.AgenticMatchCount != 60 {
		t.Fatalf("AgenticMatchCount = %d, want 60", fixture.AgenticMatchCount)
	}
	if fixture.AgenticBadJobs != 24 {
		t.Fatalf("AgenticBadJobs = %d, want 24", fixture.AgenticBadJobs)
	}
	if fixture.AgenticTier1Services != 8 {
		t.Fatalf("AgenticTier1Services = %d, want 8", fixture.AgenticTier1Services)
	}
	if _, ok := fixture.Files[fixture.RenameSource]; !ok {
		t.Fatalf("fixture missing rename source %q", fixture.RenameSource)
	}
	if _, ok := fixture.Files[fixture.RewriteTarget]; !ok {
		t.Fatalf("fixture missing rewrite target %q", fixture.RewriteTarget)
	}
	if _, ok := fixture.Files[fixture.RemoveTarget]; !ok {
		t.Fatalf("fixture missing remove target %q", fixture.RemoveTarget)
	}
	if _, ok := fixture.Files[fixture.AgenticManifestPath]; !ok {
		t.Fatalf("fixture missing agentic manifest %q", fixture.AgenticManifestPath)
	}
}

func TestPrepareBenchmarkEnvSeedsSQLiteTemplate(t *testing.T) {
	env, err := prepareBenchmarkEnv(context.Background(), t.TempDir(), &options{FixtureProfile: fixtureProfileSynthetic})
	if err != nil {
		t.Fatalf("prepareBenchmarkEnv() error = %v", err)
	}

	factory, cleanup, err := benchmarkBackends()[2].Prepare(context.Background(), env)
	if err != nil {
		t.Fatalf("sqlite Prepare() error = %v", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("cleanup() error = %v", err)
		}
	}()

	fsys, err := factory.New(context.Background())
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}
	defer func() {
		if err := closeIfPossible(fsys); err != nil {
			t.Fatalf("closeIfPossible() error = %v", err)
		}
	}()

	got, err := readSandboxText(context.Background(), fsys, mutationRewritePath)
	if err != nil {
		t.Fatalf("readSandboxText() error = %v", err)
	}
	want := string(env.fixture.Files[mutationRewritePath].Content)
	if got != want {
		t.Fatalf("template read = %q, want %q", got, want)
	}
}

func TestEnsurePreparedArtifactsBuildsSQLiteAndBleve(t *testing.T) {
	files := gbfs.InitialFiles{
		"/workspace/linux/kernel/a.c": {Content: []byte("module_init\n")},
		"/workspace/linux/kernel/b.c": {Content: []byte("EXPORT_SYMBOL_GPL\n")},
	}
	env := &benchmarkEnv{
		tmpRoot:      t.TempDir(),
		artifactRoot: t.TempDir(),
		fixture: benchmarkFixture{
			Files:       files,
			Fingerprint: mustFixtureFingerprint(files),
			Summary: fixtureSummary{
				FileCount:  len(files),
				TotalBytes: int64(len(files["/workspace/linux/kernel/a.c"].Content) + len(files["/workspace/linux/kernel/b.c"].Content)),
			},
			KernelRoot: kernelWorkspaceRoot,
			KernelSummary: fixtureSummary{
				FileCount: len(files),
			},
		},
	}

	artifactDir, err := env.ensurePreparedArtifacts(context.Background())
	if err != nil {
		t.Fatalf("ensurePreparedArtifacts() error = %v", err)
	}
	if !fileExists(filepath.Join(artifactDir, "sqlite-template.db")) {
		t.Fatalf("sqlite artifact missing under %q", artifactDir)
	}
	if !fileExists(filepath.Join(artifactDir, "bleve-index")) {
		t.Fatalf("bleve artifact missing under %q", artifactDir)
	}

	manifest, err := readBenchmarkArtifactManifest(filepath.Join(artifactDir, "manifest.json"))
	if err != nil {
		t.Fatalf("readBenchmarkArtifactManifest() error = %v", err)
	}
	if manifest.FixtureFingerprint != env.fixture.Fingerprint {
		t.Fatalf("manifest fingerprint = %q, want %q", manifest.FixtureFingerprint, env.fixture.Fingerprint)
	}

	factory, cleanup, err := benchmarkBackends()[3].Prepare(context.Background(), env)
	if err != nil {
		t.Fatalf("bleve Prepare() error = %v", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("cleanup() error = %v", err)
		}
	}()

	fsys, err := factory.New(context.Background())
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}
	defer func() {
		if err := closeIfPossible(fsys); err != nil {
			t.Fatalf("closeIfPossible() error = %v", err)
		}
	}()

	result, mode, err := countLiteralHits(context.Background(), fsys, kernelWorkspaceRoot, "module_init")
	if err != nil {
		t.Fatalf("countLiteralHits() error = %v", err)
	}
	if result != 1 {
		t.Fatalf("countLiteralHits() = %d, want 1", result)
	}
	if mode != "index" {
		t.Fatalf("countLiteralHits() mode = %q, want index", mode)
	}
}

func TestWorkloadsPassOnMemoryBackend(t *testing.T) {
	fixture := buildBenchmarkFixture()
	factory := gbfs.SeededMemory(fixture.Files)

	for _, scenario := range benchmarkScenarios(fixtureProfileSynthetic) {
		fsys, err := factory.New(context.Background())
		if err != nil {
			t.Fatalf("factory.New() error = %v", err)
		}
		observation, err := scenario.Run(context.Background(), fsys, &fixture)
		if err != nil {
			t.Fatalf("%s Run() error = %v", scenario.Name, err)
		}
		if (scenario.Name == "literal_search" || scenario.Name == "agentic_search") && observation.SearchMode != "scan" {
			t.Fatalf("%s SearchMode = %q, want scan", scenario.Name, observation.SearchMode)
		}
	}
}

func TestBenchmarkScenariosLinuxKernelProfile(t *testing.T) {
	scenarios := benchmarkScenarios(fixtureProfileLinuxKernel)
	if len(scenarios) != 2 {
		t.Fatalf("linux kernel scenario count = %d, want 2", len(scenarios))
	}
	if scenarios[0].Name != "kernel_search_cold" {
		t.Fatalf("linux kernel scenario[0] name = %q, want kernel_search_cold", scenarios[0].Name)
	}
	if scenarios[0].Timing != workloadTimingCold {
		t.Fatalf("linux kernel scenario[0] timing = %q, want %q", scenarios[0].Timing, workloadTimingCold)
	}
	if scenarios[1].Name != "kernel_search_warm" {
		t.Fatalf("linux kernel scenario[1] name = %q, want kernel_search_warm", scenarios[1].Name)
	}
	if scenarios[1].Timing != workloadTimingWarm {
		t.Fatalf("linux kernel scenario[1] timing = %q, want %q", scenarios[1].Timing, workloadTimingWarm)
	}
}

func TestRunScenarioTrialsWarmExcludesFactoryNew(t *testing.T) {
	const (
		newDelay = 35 * time.Millisecond
		runDelay = 5 * time.Millisecond
	)

	backend := backendConfig{
		Info: backendInfo{Name: "test"},
		Prepare: func(_ context.Context, _ *benchmarkEnv) (gbfs.Factory, func() error, error) {
			return gbfs.FactoryFunc(func(ctx context.Context) (gbfs.FileSystem, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(newDelay):
				}
				return gbfs.Memory().New(ctx)
			}), noopCleanup, nil
		},
	}
	scenario := workloadConfig{
		Name:   "test_search",
		Timing: workloadTimingCold,
		Run: func(ctx context.Context, _ gbfs.FileSystem, _ *benchmarkFixture) (workloadObservation, error) {
			select {
			case <-ctx.Done():
				return workloadObservation{}, ctx.Err()
			case <-time.After(runDelay):
			}
			return workloadObservation{SearchMode: "scan"}, nil
		},
	}

	cold, err := runScenarioTrials(context.Background(), backend, scenario, &benchmarkEnv{}, 1)
	if err != nil {
		t.Fatalf("cold runScenarioTrials() error = %v", err)
	}

	scenario.Timing = workloadTimingWarm
	warm, err := runScenarioTrials(context.Background(), backend, scenario, &benchmarkEnv{}, 1)
	if err != nil {
		t.Fatalf("warm runScenarioTrials() error = %v", err)
	}

	coldMedian := time.Duration(cold.Stats.MedianNanos)
	warmMedian := time.Duration(warm.Stats.MedianNanos)
	if coldMedian <= warmMedian {
		t.Fatalf("cold median = %v, want greater than warm median %v", coldMedian, warmMedian)
	}
	if coldMedian-warmMedian < newDelay/2 {
		t.Fatalf("cold-warm delta = %v, want at least %v", coldMedian-warmMedian, newDelay/2)
	}
}

func TestBleveUsesIndexedSearch(t *testing.T) {
	fixture := buildBenchmarkFixture()
	factory, cleanup, err := benchmarkBackends()[3].Prepare(context.Background(), &benchmarkEnv{fixture: fixture})
	if err != nil {
		t.Fatalf("bleve Prepare() error = %v", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("cleanup() error = %v", err)
		}
	}()

	fsys, err := factory.New(context.Background())
	if err != nil {
		t.Fatalf("factory.New() error = %v", err)
	}
	for _, scenario := range []struct {
		name string
		run  func(context.Context, gbfs.FileSystem, *benchmarkFixture) (workloadObservation, error)
	}{
		{name: "literal_search", run: runLiteralSearch},
		{name: "agentic_search", run: runAgenticSearch},
	} {
		observation, err := scenario.run(context.Background(), fsys, &fixture)
		if err != nil {
			t.Fatalf("%s error = %v", scenario.name, err)
		}
		if observation.SearchMode != "index" {
			t.Fatalf("%s SearchMode = %q, want index", scenario.name, observation.SearchMode)
		}
	}
}

func TestRunMainWritesJSONReport(t *testing.T) {
	jsonPath := filepath.Join(t.TempDir(), "filesystem-bench.json")
	if err := runMain(context.Background(), []string{"--runs", "1", "--fixture-profile", fixtureProfileSynthetic, "--json-out", jsonPath}, io.Discard, io.Discard); err != nil {
		t.Fatalf("runMain() error = %v", err)
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", jsonPath, err)
	}
	var report benchmarkReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(report.Backends) != 4 {
		t.Fatalf("backend count = %d, want 4", len(report.Backends))
	}
	if len(report.Scenarios) != 5 {
		t.Fatalf("scenario count = %d, want 5", len(report.Scenarios))
	}
}
