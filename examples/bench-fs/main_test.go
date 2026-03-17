package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestBuildBenchmarkFixture(t *testing.T) {
	t.Parallel()
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
	if _, ok := fixture.Files[fixture.RenameSource]; !ok {
		t.Fatalf("fixture missing rename source %q", fixture.RenameSource)
	}
	if _, ok := fixture.Files[fixture.RewriteTarget]; !ok {
		t.Fatalf("fixture missing rewrite target %q", fixture.RewriteTarget)
	}
	if _, ok := fixture.Files[fixture.RemoveTarget]; !ok {
		t.Fatalf("fixture missing remove target %q", fixture.RemoveTarget)
	}
}

func TestPrepareBenchmarkEnvSeedsSQLiteTemplate(t *testing.T) {
	t.Parallel()
	env, err := prepareBenchmarkEnv(context.Background(), t.TempDir())
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

func TestWorkloadsPassOnMemoryBackend(t *testing.T) {
	t.Parallel()
	fixture := buildBenchmarkFixture()
	factory := gbfs.SeededMemory(fixture.Files)

	for _, scenario := range benchmarkScenarios() {
		fsys, err := factory.New(context.Background())
		if err != nil {
			t.Fatalf("factory.New() error = %v", err)
		}
		observation, err := scenario.Run(context.Background(), fsys, &fixture)
		if err != nil {
			t.Fatalf("%s Run() error = %v", scenario.Name, err)
		}
		if scenario.Name == "literal_search" && observation.SearchMode != "scan" {
			t.Fatalf("%s SearchMode = %q, want scan", scenario.Name, observation.SearchMode)
		}
	}
}

func TestRunMainWritesJSONReport(t *testing.T) {
	t.Parallel()
	jsonPath := filepath.Join(t.TempDir(), "filesystem-bench.json")
	if err := runMain(context.Background(), []string{"--runs", "1", "--json-out", jsonPath}, io.Discard, io.Discard); err != nil {
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
	if len(report.Backends) != 3 {
		t.Fatalf("backend count = %d, want 3", len(report.Backends))
	}
	if len(report.Scenarios) != 4 {
		t.Fatalf("scenario count = %d, want 4", len(report.Scenarios))
	}
}
