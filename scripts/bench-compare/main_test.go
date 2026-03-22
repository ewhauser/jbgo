package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash"
)

func TestCreateWorkspaceFixture(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "workspace")
	summary, err := createWorkspaceFixture(root)
	if err != nil {
		t.Fatalf("createWorkspaceFixture() error = %v", err)
	}
	if summary.FileCount != 300 {
		t.Fatalf("FileCount = %d, want 300", summary.FileCount)
	}
	assertFixtureSummaryMatchesWalk(t, root, summary)
}

func TestCreateAgenticSearchFixture(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "workspace")
	summary, err := createAgenticSearchFixture(root)
	if err != nil {
		t.Fatalf("createAgenticSearchFixture() error = %v", err)
	}
	if summary.FileCount != 300 {
		t.Fatalf("FileCount = %d, want 300", summary.FileCount)
	}
	assertFixtureSummaryMatchesWalk(t, root, summary)

	extCounts := map[string]int{}
	keywordMatches := 0
	badJobs := 0
	var tier1 int

	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		extCounts[filepath.Ext(path)]++

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "timeout") || strings.Contains(line, "rollback") {
				keywordMatches++
			}
			if filepath.Ext(path) == ".jsonl" {
				var record struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					return err
				}
				if record.Status != "ok" {
					badJobs++
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		if filepath.Base(path) == "services.json" {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var manifest struct {
				Services []struct {
					Tier int `json:"tier"`
				} `json:"services"`
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return err
			}
			for _, service := range manifest.Services {
				if service.Tier == 1 {
					tier1++
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
	if got, want := extCounts[".go"], 240; got != want {
		t.Fatalf(".go file count = %d, want %d", got, want)
	}
	if got, want := extCounts[".md"], 24; got != want {
		t.Fatalf(".md file count = %d, want %d", got, want)
	}
	if got, want := extCounts[".csv"], 12; got != want {
		t.Fatalf(".csv file count = %d, want %d", got, want)
	}
	if got, want := extCounts[".jsonl"], 12; got != want {
		t.Fatalf(".jsonl file count = %d, want %d", got, want)
	}
	if got, want := extCounts[".json"], 12; got != want {
		t.Fatalf(".json file count = %d, want %d", got, want)
	}
	if got, want := keywordMatches, 60; got != want {
		t.Fatalf("keyword match count = %d, want %d", got, want)
	}
	if got, want := badJobs, 24; got != want {
		t.Fatalf("bad job count = %d, want %d", got, want)
	}
	if got, want := tier1, 8; got != want {
		t.Fatalf("tier1 service count = %d, want %d", got, want)
	}
}

func TestCreateExpansionStressFixture(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "workspace")
	summary, err := createExpansionStressFixture(root)
	if err != nil {
		t.Fatalf("createExpansionStressFixture() error = %v", err)
	}
	if summary.FileCount != 481 {
		t.Fatalf("FileCount = %d, want 481", summary.FileCount)
	}
	assertFixtureSummaryMatchesWalk(t, root, summary)

	matches, err := filepath.Glob(filepath.Join(root, "pkg0[0-3]", "section0[0-2]", "file0[0-3][0-9].txt"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if got, want := len(matches), 480; got != want {
		t.Fatalf("glob match count = %d, want %d", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "literal*dir", "[meta]?", "report.txt")); err != nil {
		t.Fatalf("Stat(literal metachar path) error = %v", err)
	}
}

func TestExpansionStressScenarioMatchesExpectedStdoutUnderBash(t *testing.T) {
	t.Parallel()

	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	root := filepath.Join(t.TempDir(), "workspace")
	summary, err := createExpansionStressFixture(root)
	if err != nil {
		t.Fatalf("createExpansionStressFixture() error = %v", err)
	}

	cmd := exec.CommandContext(context.Background(), bashPath, "--noprofile", "--norc", "-c", expansionStressCommand)
	cmd.Dir = summary.Root
	cmd.Env = append(os.Environ(), "BASH_ENV=")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash expansion stress command failed: %v\noutput=%s", err, output)
	}
	if got, want := string(output), expansionStressExpectedStdout; got != want {
		t.Fatalf("bash expansion stress stdout = %q, want %q", got, want)
	}
}

func TestSummarizeDurations(t *testing.T) {
	t.Parallel()
	stats, ok := summarizeDurations([]time.Duration{
		7 * time.Millisecond,
		1 * time.Millisecond,
		5 * time.Millisecond,
		9 * time.Millisecond,
		3 * time.Millisecond,
	})
	if !ok {
		t.Fatalf("summarizeDurations() ok = false, want true")
	}
	if got, want := time.Duration(stats.MinNanos), 1*time.Millisecond; got != want {
		t.Fatalf("Min = %s, want %s", got, want)
	}
	if got, want := time.Duration(stats.MedianNanos), 5*time.Millisecond; got != want {
		t.Fatalf("Median = %s, want %s", got, want)
	}
	if got, want := time.Duration(stats.P95Nanos), 9*time.Millisecond; got != want {
		t.Fatalf("P95 = %s, want %s", got, want)
	}
}

func TestBenchmarkScenarios(t *testing.T) {
	t.Parallel()
	workspaceFixture := fixtureSummary{
		Root:       filepath.Join(string(filepath.Separator), "tmp", "workspace"),
		FileCount:  300,
		TotalBytes: 17100,
	}
	agenticFixture := fixtureSummary{
		Root:       filepath.Join(string(filepath.Separator), "tmp", "agentic"),
		FileCount:  300,
		TotalBytes: 24120,
	}
	expansionFixture := fixtureSummary{
		Root:       filepath.Join(string(filepath.Separator), "tmp", "expansion"),
		FileCount:  481,
		TotalBytes: 30293,
	}

	scenarios := benchmarkScenarios(workspaceFixture, agenticFixture, expansionFixture)
	if got, want := len(scenarios), 4; got != want {
		t.Fatalf("len(benchmarkScenarios()) = %d, want %d", got, want)
	}

	gotNames := make([]string, 0, len(scenarios))
	for _, scenario := range scenarios {
		gotNames = append(gotNames, scenario.Name)
	}
	wantNames := []string{
		"startup_echo",
		"workspace_inventory",
		"agentic_search",
		"expansion_stress",
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("scenario names = %q, want %q", gotNames, wantNames)
	}

	for _, index := range []int{0, 1, 2, 3} {
		if got, want := scenarios[index].WarmupScript, "true\n"; got != want {
			t.Fatalf("scenarios[%d].WarmupScript = %q, want %q", index, got, want)
		}
	}
	if !scenarios[1].Workspace {
		t.Fatalf("workspace scenario should mount the generated fixture")
	}
	if got, want := scenarios[1].ExpectedStdout, "300\n"; got != want {
		t.Fatalf("workspace ExpectedStdout = %q, want %q", got, want)
	}
	if scenarios[1].Fixture == nil {
		t.Fatalf("workspace Fixture = nil, want fixture summary")
	}
	if got, want := scenarios[1].Fixture.Root, workspaceFixture.Root; got != want {
		t.Fatalf("workspace Fixture.Root = %q, want %q", got, want)
	}
	if !scenarios[2].Workspace {
		t.Fatalf("agentic_search scenario should mount the mixed fixture")
	}
	if !scenarios[2].RequiresJQ {
		t.Fatalf("agentic_search should require jq")
	}
	if got, want := scenarios[2].ExpectedStdout, "files=300\nmatches=60\nbad_jobs=24\ntier1=8\n"; got != want {
		t.Fatalf("agentic_search ExpectedStdout = %q, want %q", got, want)
	}
	if scenarios[2].Fixture == nil {
		t.Fatalf("agentic_search Fixture = nil, want fixture summary")
	}
	if got, want := scenarios[2].Fixture.Root, agenticFixture.Root; got != want {
		t.Fatalf("agentic_search Fixture.Root = %q, want %q", got, want)
	}
	if !strings.Contains(scenarios[2].Command, "jq -s") {
		t.Fatalf("agentic_search command = %q, want jq usage", scenarios[2].Command)
	}
	if !scenarios[3].Workspace {
		t.Fatalf("expansion_stress should mount the generated fixture")
	}
	if scenarios[3].RequiresJQ {
		t.Fatalf("expansion_stress should not require jq")
	}
	if got, want := scenarios[3].ExpectedStdout, expansionStressExpectedStdout; got != want {
		t.Fatalf("expansion_stress ExpectedStdout = %q, want %q", got, want)
	}
	if scenarios[3].Fixture == nil {
		t.Fatalf("expansion_stress Fixture = nil, want fixture summary")
	}
	if got, want := scenarios[3].Fixture.Root, expansionFixture.Root; got != want {
		t.Fatalf("expansion_stress Fixture.Root = %q, want %q", got, want)
	}
	if !strings.Contains(scenarios[3].Command, `IFS=':ç'`) {
		t.Fatalf("expansion_stress command = %q, want multibyte IFS coverage", scenarios[3].Command)
	}
	if !strings.Contains(scenarios[3].Command, `globbed=(./pkg0[0-3]/section0[0-2]/file0[0-3][0-9].txt)`) {
		t.Fatalf("expansion_stress command = %q, want glob coverage", scenarios[3].Command)
	}
	if !strings.Contains(scenarios[3].Command, `) > "$scratch/out.txt"`) {
		t.Fatalf("expansion_stress command = %q, want subshell stdout redirection coverage", scenarios[3].Command)
	}
	if !strings.Contains(scenarios[3].Command, `2> "$scratch/err.txt"`) {
		t.Fatalf("expansion_stress command = %q, want stderr redirection coverage", scenarios[3].Command)
	}
	if !strings.Contains(scenarios[3].Command, `exec 3< "$scratch/out.txt"`) {
		t.Fatalf("expansion_stress command = %q, want explicit fd redirection coverage", scenarios[3].Command)
	}
}

func TestRenderTextReportAndJSON(t *testing.T) {
	t.Parallel()
	report := benchmarkReport{
		GeneratedAt:  "2026-03-13T00:00:00Z",
		Runs:         2,
		JustBashSpec: "just-bash@2.13.0",
		Scenarios: []scenarioReport{
			{
				Name:           "startup_echo",
				Description:    "Process start plus one simple command.",
				Command:        "echo benchmark",
				ExpectedStdout: "benchmark\n",
				Results: []runtimeReport{
					{
						Name:              "gbash",
						ArtifactSizeBytes: 5 << 20,
						SuccessCount:      2,
						Stats: &latencyStats{
							MinNanos:    int64(time.Millisecond),
							MedianNanos: int64(2 * time.Millisecond),
							P95Nanos:    int64(3 * time.Millisecond),
						},
						Trials: []trialResult{
							{Index: 1, DurationNanos: int64(time.Millisecond), ExitCode: 0, Success: true, Stdout: "benchmark\n"},
							{Index: 2, DurationNanos: int64(3 * time.Millisecond), ExitCode: 0, Success: true, Stdout: "benchmark\n"},
						},
					},
					{
						Name:              "gbash-node-wasm",
						ArtifactSizeBytes: 7 << 20,
						Status:            runtimeStatusSkipped,
						SkipReason:        requiresJQSkipReason,
					},
				},
			},
		},
	}

	rendered := renderTextReport(report)
	if !strings.Contains(rendered, "Independent shell benchmark") {
		t.Fatalf("renderTextReport() missing report title:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[startup_echo]") {
		t.Fatalf("renderTextReport() missing scenario header:\n%s", rendered)
	}
	if !strings.Contains(rendered, "gbash: 2/2 successful") {
		t.Fatalf("renderTextReport() missing runtime summary:\n%s", rendered)
	}
	if !strings.Contains(rendered, "size=5.0 MiB") {
		t.Fatalf("renderTextReport() missing artifact size:\n%s", rendered)
	}
	if !strings.Contains(rendered, "median=2ms") {
		t.Fatalf("renderTextReport() missing latency stats:\n%s", rendered)
	}
	if !strings.Contains(rendered, "gbash-node-wasm: skipped ("+requiresJQSkipReason+")") {
		t.Fatalf("renderTextReport() missing skipped runtime summary:\n%s", rendered)
	}

	jsonPath := filepath.Join(t.TempDir(), "report.json")
	if err := writeJSONReport(jsonPath, report); err != nil {
		t.Fatalf("writeJSONReport() error = %v", err)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "\"startup_echo\"") {
		t.Fatalf("JSON output missing scenario: %s", string(data))
	}
	if !strings.Contains(string(data), "\"artifact_size_bytes\": 5242880") {
		t.Fatalf("JSON output missing artifact size: %s", string(data))
	}
	if !strings.Contains(string(data), "\"just_bash_spec\": \"just-bash@2.13.0\"") {
		t.Fatalf("JSON output missing just-bash spec: %s", string(data))
	}
	if !strings.Contains(string(data), "\"status\": \"skipped\"") {
		t.Fatalf("JSON output missing skipped status: %s", string(data))
	}
	if !strings.Contains(string(data), "\"skip_reason\": \""+requiresJQSkipReason+"\"") {
		t.Fatalf("JSON output missing skip reason: %s", string(data))
	}
}

func TestRunTrialsSkipsRuntime(t *testing.T) {
	t.Parallel()
	called := false
	report := runTrials(context.Background(), runtimeConfig{
		Name:              "gbash",
		ArtifactSizeBytes: 1234,
		SkipReason: func(ctx context.Context, scenario *scenarioConfig) (string, error) {
			return requiresJQSkipReason, nil
		},
		Command: func(ctx context.Context, scenario *scenarioConfig) *exec.Cmd {
			called = true
			return nil
		},
	}, &scenarioConfig{Name: "agentic_search", RequiresJQ: true}, 2)
	if called {
		t.Fatalf("runTrials() invoked Command for skipped runtime")
	}
	if got, want := report.Status, runtimeStatusSkipped; got != want {
		t.Fatalf("report.Status = %q, want %q", got, want)
	}
	if got, want := report.SkipReason, requiresJQSkipReason; got != want {
		t.Fatalf("report.SkipReason = %q, want %q", got, want)
	}
	if report.FailureCount != 0 || report.SuccessCount != 0 {
		t.Fatalf("skipped report counts = %d/%d, want 0/0", report.SuccessCount, report.FailureCount)
	}
	if len(report.Trials) != 0 {
		t.Fatalf("len(report.Trials) = %d, want 0", len(report.Trials))
	}
}

func TestHasFailuresIgnoresSkippedResults(t *testing.T) {
	t.Parallel()
	report := benchmarkReport{
		Scenarios: []scenarioReport{
			{
				Name: "agentic_search",
				Results: []runtimeReport{
					{
						Name:       "gbash",
						Status:     runtimeStatusSkipped,
						SkipReason: requiresJQSkipReason,
					},
				},
			},
		},
	}
	if report.HasFailures() {
		t.Fatalf("HasFailures() = true, want false for skipped-only report")
	}

	report.Scenarios[0].Results = append(report.Scenarios[0].Results, runtimeReport{
		Name:         "just-bash",
		FailureCount: 1,
		Trials:       []trialResult{{Index: 1, Error: "boom"}},
	})
	if !report.HasFailures() {
		t.Fatalf("HasFailures() = false, want true when a runtime failed")
	}
}

func TestGbashNodeWasmRuntime(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join(string(filepath.Separator), "repo")
	assetDir := filepath.Join(string(filepath.Separator), "tmp", "gbash-wasm")
	runtime := gbashNodeWasmRuntime(repoRoot, assetDir, 1234)

	cmd := runtime.Command(context.Background(), &scenarioConfig{
		Command: "echo benchmark\n",
	})
	if got, want := cmd.Dir, repoRoot; got != want {
		t.Fatalf("cmd.Dir = %q, want %q", got, want)
	}
	if got, want := filepath.Base(cmd.Path), "node"; got != want {
		t.Fatalf("cmd.Path = %q, want %q", got, want)
	}
	if got, want := runtime.ArtifactSizeBytes, int64(1234); got != want {
		t.Fatalf("runtime.ArtifactSizeBytes = %d, want %d", got, want)
	}
	wantArgs := []string{
		"node",
		filepath.Join(repoRoot, "scripts", "bench-compare", "gbash-wasm-runner.mjs"),
		"--wasm", filepath.Join(assetDir, "gbash.wasm"),
		"--wasm-exec", filepath.Join(assetDir, "wasm_exec.js"),
		"-c", "echo benchmark\n",
	}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("cmd.Args = %q, want %q", cmd.Args, wantArgs)
	}
}

func TestGNUBashRuntime(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join(string(filepath.Separator), "repo")
	bashPath := filepath.Join(string(filepath.Separator), "bin", "bash")
	runtime := gnuBashRuntime(repoRoot, bashPath, 4321)

	cmd := runtime.Command(context.Background(), &scenarioConfig{
		Command: "echo benchmark\n",
	})
	if got, want := cmd.Path, bashPath; got != want {
		t.Fatalf("cmd.Path = %q, want %q", got, want)
	}
	if got, want := cmd.Dir, repoRoot; got != want {
		t.Fatalf("cmd.Dir = %q, want %q", got, want)
	}
	if got, want := runtime.ArtifactSizeBytes, int64(4321); got != want {
		t.Fatalf("runtime.ArtifactSizeBytes = %d, want %d", got, want)
	}
	wantArgs := []string{
		bashPath,
		"--noprofile",
		"--norc",
		"-c",
		"echo benchmark\n",
	}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("cmd.Args = %q, want %q", cmd.Args, wantArgs)
	}
}

func TestGNUBashRuntimeWorkspace(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join(string(filepath.Separator), "repo")
	bashPath := filepath.Join(string(filepath.Separator), "bin", "bash")
	runtime := gnuBashRuntime(repoRoot, bashPath, 4321)

	fixtureRoot := filepath.Join(string(filepath.Separator), "tmp", "fixture")
	cmd := runtime.Command(context.Background(), &scenarioConfig{
		Command:   "find . -type f\n",
		Workspace: true,
		Fixture: &fixtureSummary{
			Root: fixtureRoot,
		},
	})
	if got, want := cmd.Dir, fixtureRoot; got != want {
		t.Fatalf("cmd.Dir = %q, want %q", got, want)
	}
}

func TestGbashExtrasRuntime(t *testing.T) {
	t.Parallel()
	helperPath := filepath.Join(string(filepath.Separator), "tmp", "gbash-extras")
	runtime := gbashExtrasRuntime(helperPath, 5678)

	cmd := runtime.Command(context.Background(), &scenarioConfig{
		Command: "echo benchmark\n",
	})
	if got, want := cmd.Path, helperPath; got != want {
		t.Fatalf("cmd.Path = %q, want %q", got, want)
	}
	if got, want := runtime.ArtifactSizeBytes, int64(5678); got != want {
		t.Fatalf("runtime.ArtifactSizeBytes = %d, want %d", got, want)
	}
	wantArgs := []string{
		helperPath,
		"-c", "echo benchmark\n",
	}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("cmd.Args = %q, want %q", cmd.Args, wantArgs)
	}
}

func TestGbashExtrasRuntimeWorkspace(t *testing.T) {
	t.Parallel()
	helperPath := filepath.Join(string(filepath.Separator), "tmp", "gbash-extras")
	runtime := gbashExtrasRuntime(helperPath, 5678)

	cmd := runtime.Command(context.Background(), &scenarioConfig{
		Command:   "find . -type f\n",
		Workspace: true,
		Fixture: &fixtureSummary{
			Root: filepath.Join(string(filepath.Separator), "tmp", "fixture"),
		},
	})
	wantArgs := []string{
		helperPath,
		"--root", filepath.Join(string(filepath.Separator), "tmp", "fixture"),
		"--cwd", gbash.DefaultWorkspaceMountPoint,
		"-c", "find . -type f\n",
	}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("cmd.Args = %q, want %q", cmd.Args, wantArgs)
	}
}

func TestGbashNodeWasmRuntimeWorkspace(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join(string(filepath.Separator), "repo")
	assetDir := filepath.Join(string(filepath.Separator), "tmp", "gbash-wasm")
	runtime := gbashNodeWasmRuntime(repoRoot, assetDir, 1234)

	cmd := runtime.Command(context.Background(), &scenarioConfig{
		Command:   "find . -type f\n",
		Workspace: true,
		Fixture: &fixtureSummary{
			Root: filepath.Join(string(filepath.Separator), "tmp", "fixture"),
		},
	})
	wantArgs := []string{
		"node",
		filepath.Join(repoRoot, "scripts", "bench-compare", "gbash-wasm-runner.mjs"),
		"--wasm", filepath.Join(assetDir, "gbash.wasm"),
		"--wasm-exec", filepath.Join(assetDir, "wasm_exec.js"),
		"--workspace", filepath.Join(string(filepath.Separator), "tmp", "fixture"),
		"--cwd", gbash.DefaultWorkspaceMountPoint,
		"-c", "find . -type f\n",
	}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("cmd.Args = %q, want %q", cmd.Args, wantArgs)
	}
}

func TestFormatArtifactSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value int64
		want  string
	}{
		{0, "n/a"},
		{999, "999 B"},
		{1024, "1.0 KiB"},
		{5 << 20, "5.0 MiB"},
	}
	for _, tt := range tests {
		if got := formatArtifactSize(tt.value); got != tt.want {
			t.Fatalf("formatArtifactSize(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestDirectorySize(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatalf("WriteFile(a.txt) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("Mkdir(nested) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "b.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile(b.txt) error = %v", err)
	}
	got, err := directorySize(root)
	if err != nil {
		t.Fatalf("directorySize() error = %v", err)
	}
	if want := int64(len("abc") + len("hello")); got != want {
		t.Fatalf("directorySize() = %d, want %d", got, want)
	}
}

func assertFixtureSummaryMatchesWalk(t *testing.T, root string, summary fixtureSummary) {
	t.Helper()

	var countedFiles int
	var countedBytes int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		countedFiles++
		countedBytes += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
	if countedFiles != summary.FileCount {
		t.Fatalf("walked files = %d, want %d", countedFiles, summary.FileCount)
	}
	if countedBytes != summary.TotalBytes {
		t.Fatalf("walked bytes = %d, want %d", countedBytes, summary.TotalBytes)
	}
}
