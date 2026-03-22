package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"time"

	"github.com/ewhauser/gbash"
)

const (
	defaultRuns          = 100
	defaultJustBashSpec  = "just-bash@2.13.0"
	buildTimeout         = 2 * time.Minute
	primeTimeout         = 2 * time.Minute
	trialTimeout         = 30 * time.Second
	justBashWorkspace    = "/home/user/project"
	runtimeStatusSkipped = "skipped"

	requiresJQSkipReason    = "requires jq; runtime does not bundle it"
	hostBashJQMissingReason = "requires jq; host bash environment does not provide it"
)

const expansionStressCommand = `set -eu
base='pkg02/section01/file019.txt'
ref=base
printf 'indirect=%s\n' "${!ref}"

globbed=(./pkg0[0-3]/section0[0-2]/file0[0-3][0-9].txt)
printf 'glob=%s\n' "${#globbed[@]}"

trimmed=("${globbed[@]#./}")
patched=("${trimmed[@]/#pkg/pkg-root}")
patched=("${patched[@]/%.txt/.data}")
last=$(( ${#patched[@]} - 1 ))
printf 'replace=%s|%s\n' "${patched[0]}" "${patched[$last]}"

mixed=('left' 'two words' 'middle:bits')
set -- pre"${mixed[@]:1:2}"post
printf 'quoted=%s|%s|%s\n' "$#" "$1" "$2"

old_ifs=$IFS
IFS=':ç'
split_src='red:blue::greençomega'
set -- $split_src
IFS=$old_ifs
printf 'ifs=%s|%s|%s|%s\n' "$#" "$1" "$3" "$5"

unset maybe || true
set -- ${maybe:-alpha beta gamma}
printf 'default=%s|%s|%s\n' "$#" "$1" "$3"

slice_src=(zero one two three four)
set -- "${slice_src[@]:1:3}"
printf 'slice=%s|%s|%s|%s\n' "$#" "$1" "$2" "$3"

path='/root/archive/program.tar.gz'
printf 'strip=%s|%s\n' "${path#*/}" "${path%%.*}"

literal_path='./literal*dir/[meta]?/report.txt'
if [ -f "$literal_path" ]; then
  printf 'literal=%s\n' "$literal_path"
else
  printf 'literal=missing\n'
fi

scratch='./.bench-redir'
mkdir -p "$scratch"
(
  printf '%s\n' 'subshell-a'
  printf '%s\n' "${patched[0]}"
) > "$scratch/out.txt"
(
  printf '%s\n' 'subshell-b'
) >> "$scratch/out.txt"
(
  printf '%s\n' 'stderr-a' >&2
  printf '%s\n' 'stderr-b' >&2
) 2> "$scratch/err.txt"

{
  IFS= read -r redir_first
  IFS= read -r redir_second
  IFS= read -r redir_third
} < "$scratch/out.txt"
printf 'redir=%s|%s|%s\n' "$redir_first" "$redir_second" "$redir_third"

exec 3< "$scratch/out.txt"
IFS= read -r fd_first <&3
exec 3<&-
printf 'fd=%s\n' "$fd_first"

err_join=''
while IFS= read -r line; do
  if [ -n "$err_join" ]; then
    err_join="$err_join,$line"
  else
    err_join=$line
  fi
done < "$scratch/err.txt"
printf 'stderr=%s\n' "$err_join"
`

const expansionStressExpectedStdout = "" +
	"indirect=pkg02/section01/file019.txt\n" +
	"glob=480\n" +
	"replace=pkg-root00/section00/file000.data|pkg-root03/section02/file039.data\n" +
	"quoted=2|pretwo words|middle:bitspost\n" +
	"ifs=5|red||omega\n" +
	"default=3|alpha|gamma\n" +
	"slice=3|one|two|three\n" +
	"strip=root/archive/program.tar.gz|/root/archive/program\n" +
	"literal=./literal*dir/[meta]?/report.txt\n" +
	"redir=subshell-a|pkg-root00/section00/file000.data|subshell-b\n" +
	"fd=subshell-a\n" +
	"stderr=stderr-a,stderr-b\n"

type options struct {
	Runs         int
	JSONOut      string
	JustBashSpec string
}

type fixtureSummary struct {
	Root       string `json:"root,omitempty"`
	FileCount  int    `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
}

type latencyStats struct {
	MinNanos    int64 `json:"min_nanos"`
	MedianNanos int64 `json:"median_nanos"`
	P95Nanos    int64 `json:"p95_nanos"`
}

type trialResult struct {
	Index         int    `json:"index"`
	DurationNanos int64  `json:"duration_nanos"`
	ExitCode      int    `json:"exit_code"`
	Success       bool   `json:"success"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	Error         string `json:"error,omitempty"`
}

type runtimeReport struct {
	Name              string        `json:"name"`
	ArtifactSizeBytes int64         `json:"artifact_size_bytes,omitempty"`
	Status            string        `json:"status,omitempty"`
	SkipReason        string        `json:"skip_reason,omitempty"`
	SuccessCount      int           `json:"success_count"`
	FailureCount      int           `json:"failure_count"`
	Stats             *latencyStats `json:"stats,omitempty"`
	Trials            []trialResult `json:"trials"`
}

type scenarioReport struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Command        string          `json:"command"`
	ExpectedStdout string          `json:"expected_stdout"`
	Workspace      bool            `json:"workspace"`
	Fixture        *fixtureSummary `json:"fixture,omitempty"`
	Results        []runtimeReport `json:"results"`
}

type benchmarkReport struct {
	GeneratedAt  string           `json:"generated_at"`
	Runs         int              `json:"runs"`
	JustBashSpec string           `json:"just_bash_spec"`
	Scenarios    []scenarioReport `json:"scenarios"`
}

type scenarioConfig struct {
	Name           string
	Description    string
	Command        string
	ExpectedStdout string
	RequiresJQ     bool
	Workspace      bool
	Fixture        *fixtureSummary
	WarmupScript   string
}

type runtimeConfig struct {
	Name              string
	ArtifactSizeBytes int64
	Command           func(context.Context, *scenarioConfig) *exec.Cmd
	SkipReason        func(context.Context, *scenarioConfig) (string, error)
}

type commandResult struct {
	Duration time.Duration
	ExitCode int
	Stdout   string
	Stderr   string
	Error    string
}

func main() {
	if err := runMain(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "bench-compare: %v\n", err)
		os.Exit(1)
	}
}

func runMain(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	repoRoot, err := findModuleRoot(".")
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "gbash-bench-compare-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			_, _ = fmt.Fprintf(stderr, "bench-compare: remove temp dir: %v\n", err)
		}
	}()

	helperPath := filepath.Join(tmpDir, executableName("gbash-runner"))
	if err := buildGbashRunner(ctx, repoRoot, helperPath); err != nil {
		return err
	}
	helperSize, err := fileSize(helperPath)
	if err != nil {
		return err
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("locate bash executable: %w", err)
	}
	bashSize, err := fileSize(bashPath)
	if err != nil {
		return err
	}
	extrasPath := filepath.Join(tmpDir, executableName("gbash-extras"))
	if err := buildGbashExtrasCLI(ctx, repoRoot, extrasPath); err != nil {
		return err
	}
	extrasSize, err := fileSize(extrasPath)
	if err != nil {
		return err
	}
	wasmAssetDir := filepath.Join(tmpDir, "gbash-wasm")
	if err := buildGbashWasmAssets(ctx, repoRoot, wasmAssetDir); err != nil {
		return err
	}
	wasmSize, err := fileSize(filepath.Join(wasmAssetDir, "gbash.wasm"))
	if err != nil {
		return err
	}
	if err := primeJustBash(ctx, opts.JustBashSpec); err != nil {
		return err
	}
	justBashSize, err := measureJustBashArtifactFootprint(ctx, opts.JustBashSpec, filepath.Join(tmpDir, "just-bash-install"))
	if err != nil {
		return err
	}

	workspaceRoot := filepath.Join(tmpDir, "workspace")
	workspaceFixture, err := createWorkspaceFixture(workspaceRoot)
	if err != nil {
		return err
	}
	agenticSearchRoot := filepath.Join(tmpDir, "agentic-search-workspace")
	agenticSearchFixture, err := createAgenticSearchFixture(agenticSearchRoot)
	if err != nil {
		return err
	}
	expansionStressRoot := filepath.Join(tmpDir, "expansion-stress-workspace")
	expansionStressFixture, err := createExpansionStressFixture(expansionStressRoot)
	if err != nil {
		return err
	}

	runtimes := []runtimeConfig{
		gbashRuntime(helperPath, helperSize),
		gnuBashRuntime(repoRoot, bashPath, bashSize),
		gbashExtrasRuntime(extrasPath, extrasSize),
		gbashNodeWasmRuntime(repoRoot, wasmAssetDir, wasmSize),
		justBashRuntime(opts.JustBashSpec, justBashSize),
	}
	scenarios := benchmarkScenarios(workspaceFixture, agenticSearchFixture, expansionStressFixture)

	report := benchmarkReport{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Runs:         opts.Runs,
		JustBashSpec: opts.JustBashSpec,
	}
	for i := range scenarios {
		scenario := &scenarios[i]
		scenarioReport := scenarioReport{
			Name:           scenario.Name,
			Description:    scenario.Description,
			Command:        strings.TrimSpace(scenario.Command),
			ExpectedStdout: scenario.ExpectedStdout,
			Workspace:      scenario.Workspace,
			Fixture:        scenario.Fixture,
		}
		for _, runtime := range runtimes {
			scenarioReport.Results = append(scenarioReport.Results, runTrials(ctx, runtime, scenario, opts.Runs))
		}
		report.Scenarios = append(report.Scenarios, scenarioReport)
	}

	if _, err := io.WriteString(stdout, renderTextReport(report)); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if opts.JSONOut != "" {
		if err := writeJSONReport(opts.JSONOut, report); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "wrote JSON report to %s\n", opts.JSONOut)
	}
	if report.HasFailures() {
		return errors.New("one or more benchmark trials failed")
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("bench-compare", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.IntVar(&opts.Runs, "runs", defaultRuns, "number of timed sequential trials per runtime and scenario")
	fs.StringVar(&opts.JSONOut, "json-out", "", "optional path to write a JSON report")
	fs.StringVar(&opts.JustBashSpec, "just-bash-spec", defaultJustBashSpec, "npm package spec used for npx just-bash invocations")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if opts.Runs <= 0 {
		return options{}, fmt.Errorf("--runs must be greater than zero")
	}
	opts.JustBashSpec = strings.TrimSpace(opts.JustBashSpec)
	if opts.JustBashSpec == "" {
		return options{}, fmt.Errorf("--just-bash-spec must not be empty")
	}
	opts.JSONOut = strings.TrimSpace(opts.JSONOut)
	return opts, nil
}

func findModuleRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod above %s", start)
		}
		dir = parent
	}
}

func buildGbashRunner(ctx context.Context, repoRoot, helperPath string) error {
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(buildCtx, "go", "build", "-o", helperPath, "./scripts/bench-compare/gbash-runner")
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build gbash benchmark helper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func buildGbashExtrasCLI(ctx context.Context, repoRoot, outputPath string) error {
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(buildCtx, "go", "build", "-o", outputPath, "./contrib/extras/cmd/gbash-extras")
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build gbash-extras benchmark helper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func buildGbashWasmAssets(ctx context.Context, repoRoot, assetDir string) error {
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return fmt.Errorf("create gbash wasm asset dir: %w", err)
	}

	wasmPath := filepath.Join(assetDir, "gbash.wasm")
	cmd := exec.CommandContext(buildCtx, "go", "build", "-o", wasmPath, "./packages/gbash-wasm/wasm")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build gbash wasm benchmark assets: %w: %s", err, combineOutput(stdout.String(), stderr.String()))
	}

	goRoot, err := goEnv(buildCtx, "GOROOT")
	if err != nil {
		return err
	}
	wasmExecSrc := filepath.Join(goRoot, "lib", "wasm", "wasm_exec.js")
	wasmExecDst := filepath.Join(assetDir, "wasm_exec.js")
	if err := copyFile(wasmExecDst, wasmExecSrc); err != nil {
		return fmt.Errorf("copy wasm_exec.js: %w", err)
	}
	return nil
}

func goEnv(ctx context.Context, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "env", key)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("resolve go env %s: %w: %s", key, err, combineOutput(stdout.String(), stderr.String()))
	}
	value := strings.TrimSpace(stdout.String())
	if value == "" {
		return "", fmt.Errorf("resolve go env %s: returned empty value", key)
	}
	return value, nil
}

func primeJustBash(ctx context.Context, spec string) error {
	primeCtx, cancel := context.WithTimeout(ctx, primeTimeout)
	defer cancel()

	cmd := exec.CommandContext(primeCtx, "npx", "--yes", spec, "--version")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("prime just-bash cache: %w: %s", err, combineOutput(stdout.String(), stderr.String()))
	}
	return nil
}

func gbashRuntime(helperPath string, artifactSizeBytes int64) runtimeConfig {
	return runtimeConfig{
		Name:              "gbash",
		ArtifactSizeBytes: artifactSizeBytes,
		Command: func(ctx context.Context, scenario *scenarioConfig) *exec.Cmd {
			args := make([]string, 0, 6)
			if scenario.Workspace && scenario.Fixture != nil {
				args = append(args, "--workspace", scenario.Fixture.Root, "--cwd", gbash.DefaultWorkspaceMountPoint)
			}
			args = append(args, "-c", scenario.Command)
			return exec.CommandContext(ctx, helperPath, args...)
		},
		SkipReason: func(ctx context.Context, scenario *scenarioConfig) (string, error) {
			if scenario.RequiresJQ {
				return requiresJQSkipReason, nil
			}
			return "", nil
		},
	}
}

func gnuBashRuntime(repoRoot, bashPath string, artifactSizeBytes int64) runtimeConfig {
	return runtimeConfig{
		Name:              "GNU bash",
		ArtifactSizeBytes: artifactSizeBytes,
		Command: func(ctx context.Context, scenario *scenarioConfig) *exec.Cmd {
			cmd := exec.CommandContext(ctx, bashPath, "--noprofile", "--norc", "-c", scenario.Command)
			cmd.Dir = repoRoot
			if scenario.Workspace && scenario.Fixture != nil {
				cmd.Dir = scenario.Fixture.Root
			}
			cmd.Env = append(os.Environ(), "BASH_ENV=")
			return cmd
		},
		SkipReason: func(ctx context.Context, scenario *scenarioConfig) (string, error) {
			if !scenario.RequiresJQ {
				return "", nil
			}
			cmd := exec.CommandContext(ctx, bashPath, "--noprofile", "--norc", "-c", "command -v jq >/dev/null 2>&1")
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "BASH_ENV=")
			if err := cmd.Run(); err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
					return hostBashJQMissingReason, nil
				}
				return "", fmt.Errorf("check GNU bash jq availability: %w", err)
			}
			return "", nil
		},
	}
}

func gbashExtrasRuntime(helperPath string, artifactSizeBytes int64) runtimeConfig {
	return runtimeConfig{
		Name:              "gbash-extras",
		ArtifactSizeBytes: artifactSizeBytes,
		Command: func(ctx context.Context, scenario *scenarioConfig) *exec.Cmd {
			args := make([]string, 0, 6)
			if scenario.Workspace && scenario.Fixture != nil {
				args = append(args, "--root", scenario.Fixture.Root, "--cwd", gbash.DefaultWorkspaceMountPoint)
			}
			args = append(args, "-c", scenario.Command)
			return exec.CommandContext(ctx, helperPath, args...)
		},
	}
}

func gbashNodeWasmRuntime(repoRoot, assetDir string, artifactSizeBytes int64) runtimeConfig {
	runnerPath := filepath.Join(repoRoot, "scripts", "bench-compare", "gbash-wasm-runner.mjs")
	return runtimeConfig{
		Name:              "gbash-node-wasm",
		ArtifactSizeBytes: artifactSizeBytes,
		Command: func(ctx context.Context, scenario *scenarioConfig) *exec.Cmd {
			args := []string{
				runnerPath,
				"--wasm", filepath.Join(assetDir, "gbash.wasm"),
				"--wasm-exec", filepath.Join(assetDir, "wasm_exec.js"),
			}
			if scenario.Workspace && scenario.Fixture != nil {
				args = append(args, "--workspace", scenario.Fixture.Root, "--cwd", gbash.DefaultWorkspaceMountPoint)
			}
			args = append(args, "-c", scenario.Command)
			cmd := exec.CommandContext(ctx, "node", args...)
			cmd.Dir = repoRoot
			return cmd
		},
		SkipReason: func(ctx context.Context, scenario *scenarioConfig) (string, error) {
			if scenario.RequiresJQ {
				return requiresJQSkipReason, nil
			}
			return "", nil
		},
	}
}

func justBashRuntime(spec string, artifactSizeBytes int64) runtimeConfig {
	return runtimeConfig{
		Name:              "just-bash",
		ArtifactSizeBytes: artifactSizeBytes,
		Command: func(ctx context.Context, scenario *scenarioConfig) *exec.Cmd {
			args := []string{"--yes", spec}
			if scenario.Workspace && scenario.Fixture != nil {
				args = append(args, "--root", scenario.Fixture.Root, "--cwd", justBashWorkspace)
			}
			args = append(args, "-c", scenario.Command)
			return exec.CommandContext(ctx, "npx", args...)
		},
	}
}

func benchmarkScenarios(workspaceFixture, agenticSearchFixture, expansionStressFixture fixtureSummary) []scenarioConfig {
	return []scenarioConfig{
		{
			Name:           "startup_echo",
			Description:    "Process start plus one simple command.",
			Command:        "echo benchmark\n",
			ExpectedStdout: "benchmark\n",
			WarmupScript:   "true\n",
		},
		{
			Name:           "workspace_inventory",
			Description:    "Process start plus a pipe-free workspace inventory.",
			Command:        "set -- $(find . -type f); echo $#\n",
			ExpectedStdout: fmt.Sprintf("%d\n", workspaceFixture.FileCount),
			Workspace:      true,
			Fixture:        &workspaceFixture,
			WarmupScript:   "true\n",
		},
		{
			Name:        "agentic_search",
			Description: "Recursive search across mixed workspace files plus jq summaries.",
			Command: "" +
				"printf 'files=%s\\nmatches=%s\\nbad_jobs=%s\\ntier1=%s\\n' " +
				"\"$(find . -type f | grep -c '^')\" " +
				"\"$(grep -RniE 'timeout|rollback' . | grep -c '^')\" " +
				"\"$(jq -s 'map(select(.status != \"ok\")) | length' logs/*.jsonl)\" " +
				"\"$(jq '.services | map(select(.tier == 1)) | length' manifests/services.json)\"\n",
			ExpectedStdout: "files=300\nmatches=60\nbad_jobs=24\ntier1=8\n",
			RequiresJQ:     true,
			Workspace:      true,
			Fixture:        &agenticSearchFixture,
			WarmupScript:   "true\n",
		},
		{
			Name:           "expansion_stress",
			Description:    "Shell-heavy end-to-end script stressing arrays, slices, quoted expansion, custom IFS, globs, and literal metachar paths.",
			Command:        expansionStressCommand,
			ExpectedStdout: expansionStressExpectedStdout,
			Workspace:      true,
			Fixture:        &expansionStressFixture,
			WarmupScript:   "true\n",
		},
	}
}

func createWorkspaceFixture(root string) (fixtureSummary, error) {
	const (
		packages         = 12
		filesPerPackage  = 25
		sectionsPerGroup = 5
	)

	if err := os.MkdirAll(root, 0o755); err != nil {
		return fixtureSummary{}, fmt.Errorf("create workspace root: %w", err)
	}

	summary := fixtureSummary{Root: root}
	for pkg := range packages {
		for file := range filesPerPackage {
			section := file % sectionsPerGroup
			dir := filepath.Join(root, fmt.Sprintf("pkg%02d", pkg), fmt.Sprintf("section%02d", section))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fixtureSummary{}, fmt.Errorf("create workspace dir: %w", err)
			}
			content := fmt.Sprintf(
				"package=%02d\nsection=%02d\nfile=%03d\nbenchmark fixture payload\n",
				pkg,
				section,
				file,
			)
			target := filepath.Join(dir, fmt.Sprintf("file%03d.txt", file))
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				return fixtureSummary{}, fmt.Errorf("write fixture file %s: %w", target, err)
			}
			summary.FileCount++
			summary.TotalBytes += int64(len(content))
		}
	}
	return summary, nil
}

func createAgenticSearchFixture(root string) (fixtureSummary, error) {
	const (
		codeFiles     = 240
		docsFiles     = 24
		csvFiles      = 12
		logFiles      = 12
		jsonFiles     = 12
		timeoutHits   = 36
		rollbackHits  = 12
		badJobsPerLog = 2
	)

	if err := os.MkdirAll(root, 0o755); err != nil {
		return fixtureSummary{}, fmt.Errorf("create agentic search root: %w", err)
	}

	summary := fixtureSummary{Root: root}
	for i := range codeFiles {
		content := fmt.Sprintf("package pkg%02d\n", i%12)
		if i < timeoutHits {
			content += fmt.Sprintf("// TODO: investigate timeout regression %03d\n", i)
		}
		content += fmt.Sprintf("func File%03d() int { return %d }\n", i, i)
		if err := writeFixtureFile(root, fmt.Sprintf("src/pkg%02d/file%03d.go", i%12, i), content, &summary); err != nil {
			return fixtureSummary{}, err
		}
	}
	for i := range docsFiles {
		content := fmt.Sprintf("# Runbook %02d\n", i)
		if i < rollbackHits {
			content += fmt.Sprintf("rollback dep-%04d after on-call review\n", 1000+i)
		} else {
			content += "normal steady-state notes\n"
		}
		if err := writeFixtureFile(root, fmt.Sprintf("docs/runbook%02d.md", i), content, &summary); err != nil {
			return fixtureSummary{}, err
		}
	}
	for i := range csvFiles {
		content := fmt.Sprintf("service,owner,tier\nsvc-%02d,team-%02d,%d\n", i, i%4, 2+(i%2))
		if err := writeFixtureFile(root, fmt.Sprintf("reports/summary%02d.csv", i), content, &summary); err != nil {
			return fixtureSummary{}, err
		}
	}
	for i := range logFiles {
		var body strings.Builder
		for j := range 5 {
			status := "ok"
			errMsg := ""
			if j < badJobsPerLog {
				status = "failed"
				if j == 0 {
					errMsg = "database timeout"
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
		if err := writeFixtureFile(root, fmt.Sprintf("logs/batch%02d.jsonl", i), body.String(), &summary); err != nil {
			return fixtureSummary{}, err
		}
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
	if err := writeFixtureFile(root, "manifests/services.json", services, &summary); err != nil {
		return fixtureSummary{}, err
	}
	for i := 1; i < jsonFiles; i++ {
		content := fmt.Sprintf("{\"manifest\":\"meta-%02d\",\"team\":\"ops\",\"enabled\":%t}\n", i, i%2 == 0)
		if err := writeFixtureFile(root, fmt.Sprintf("manifests/meta%02d.json", i), content, &summary); err != nil {
			return fixtureSummary{}, err
		}
	}

	return summary, nil
}

func createExpansionStressFixture(root string) (fixtureSummary, error) {
	const (
		packages        = 4
		sections        = 3
		filesPerSection = 40
	)

	if err := os.MkdirAll(root, 0o755); err != nil {
		return fixtureSummary{}, fmt.Errorf("create expansion stress root: %w", err)
	}

	summary := fixtureSummary{Root: root}
	for pkg := range packages {
		for section := range sections {
			for file := range filesPerSection {
				content := fmt.Sprintf(
					"package=%02d\nsection=%02d\nfile=%03d\nmode=expansion-stress\n",
					pkg,
					section,
					file,
				)
				relPath := fmt.Sprintf("pkg%02d/section%02d/file%03d.txt", pkg, section, file)
				if err := writeFixtureFile(root, relPath, content, &summary); err != nil {
					return fixtureSummary{}, err
				}
			}
		}
	}
	if err := writeFixtureFile(root, "literal*dir/[meta]?/report.txt", "literal metachar path\n", &summary); err != nil {
		return fixtureSummary{}, err
	}
	return summary, nil
}

func writeFixtureFile(root, relPath, content string, summary *fixtureSummary) error {
	target := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create fixture dir for %s: %w", target, err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write fixture file %s: %w", target, err)
	}
	summary.FileCount++
	summary.TotalBytes += int64(len(content))
	return nil
}

func runTrials(ctx context.Context, runtime runtimeConfig, scenario *scenarioConfig, runs int) runtimeReport {
	report := runtimeReport{
		Name:              runtime.Name,
		ArtifactSizeBytes: runtime.ArtifactSizeBytes,
		Trials:            make([]trialResult, 0, runs),
	}
	if reason, err := runtimeSkipReason(ctx, runtime, scenario); err != nil {
		appendRuntimeErrorTrials(&report, runs, fmt.Errorf("runtime preflight failed: %w", err))
		return report
	} else if reason != "" {
		report.Status = runtimeStatusSkipped
		report.SkipReason = reason
		return report
	}
	if err := warmRuntimeForScenario(ctx, runtime, scenario); err != nil {
		appendRuntimeErrorTrials(&report, runs, err)
		return report
	}
	successDurations := make([]time.Duration, 0, runs)
	for i := range runs {
		result := runCommand(ctx, runtime.Command, scenario)
		trial := trialResult{
			Index:         i + 1,
			DurationNanos: result.Duration.Nanoseconds(),
			ExitCode:      result.ExitCode,
			Stdout:        result.Stdout,
			Stderr:        result.Stderr,
		}
		switch {
		case result.Error != "":
			trial.Error = result.Error
			report.FailureCount++
		case result.ExitCode != 0:
			trial.Error = fmt.Sprintf("unexpected exit code %d", result.ExitCode)
			report.FailureCount++
		case result.Stdout != scenario.ExpectedStdout:
			trial.Error = fmt.Sprintf("unexpected stdout: got %q want %q", result.Stdout, scenario.ExpectedStdout)
			report.FailureCount++
		default:
			trial.Success = true
			report.SuccessCount++
			successDurations = append(successDurations, result.Duration)
		}
		report.Trials = append(report.Trials, trial)
	}
	if stats, ok := summarizeDurations(successDurations); ok {
		report.Stats = &stats
	}
	return report
}

func runtimeSkipReason(ctx context.Context, runtime runtimeConfig, scenario *scenarioConfig) (string, error) {
	if runtime.SkipReason == nil {
		return "", nil
	}
	return runtime.SkipReason(ctx, scenario)
}

func appendRuntimeErrorTrials(report *runtimeReport, runs int, err error) {
	for i := range runs {
		report.FailureCount++
		report.Trials = append(report.Trials, trialResult{
			Index: i + 1,
			Error: err.Error(),
		})
	}
}

func warmRuntimeForScenario(ctx context.Context, runtime runtimeConfig, scenario *scenarioConfig) error {
	warmup := strings.TrimSpace(scenario.WarmupScript)
	if warmup == "" {
		return nil
	}
	warmScenario := *scenario
	warmScenario.Command = scenario.WarmupScript
	result := runCommand(ctx, runtime.Command, &warmScenario)
	switch {
	case result.Error != "":
		return fmt.Errorf("warmup failed: %s", result.Error)
	case result.ExitCode != 0:
		return fmt.Errorf("warmup failed: unexpected exit code %d", result.ExitCode)
	case result.Stdout != "":
		return fmt.Errorf("warmup failed: unexpected stdout %q", result.Stdout)
	default:
		return nil
	}
}

func runCommand(ctx context.Context, build func(context.Context, *scenarioConfig) *exec.Cmd, scenario *scenarioConfig) commandResult {
	trialCtx, cancel := context.WithTimeout(ctx, trialTimeout)
	defer cancel()

	cmd := build(trialCtx, scenario)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	err := cmd.Run()
	duration := time.Since(started)

	result := commandResult{
		Duration: duration,
		ExitCode: exitCode(err),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	if err == nil {
		return result
	}

	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		if trialCtx.Err() != nil {
			result.Error = "command timed out"
		}
		return result
	case errors.Is(err, context.DeadlineExceeded), errors.Is(trialCtx.Err(), context.DeadlineExceeded):
		result.Error = "command timed out"
	default:
		result.Error = err.Error()
	}
	return result
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
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

func renderTextReport(report benchmarkReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Independent shell benchmark\n")
	fmt.Fprintf(&b, "Generated: %s\n", report.GeneratedAt)
	fmt.Fprintf(&b, "Runs per scenario: %d\n", report.Runs)
	fmt.Fprintf(&b, "just-bash spec: %s\n", report.JustBashSpec)
	for _, scenario := range report.Scenarios {
		fmt.Fprintf(&b, "\n[%s]\n", scenario.Name)
		fmt.Fprintf(&b, "%s\n", scenario.Description)
		fmt.Fprintf(&b, "command: %s\n", scenario.Command)
		if scenario.Fixture != nil {
			fmt.Fprintf(&b, "fixture: %d files, %d bytes\n", scenario.Fixture.FileCount, scenario.Fixture.TotalBytes)
		}
		for _, result := range scenario.Results {
			if result.Status == runtimeStatusSkipped {
				fmt.Fprintf(&b, "%s: skipped (%s)", result.Name, result.SkipReason)
				fmt.Fprintf(&b, " size=%s", formatArtifactSize(result.ArtifactSizeBytes))
				fmt.Fprintln(&b)
				continue
			}
			fmt.Fprintf(&b, "%s: %d/%d successful", result.Name, result.SuccessCount, result.SuccessCount+result.FailureCount)
			fmt.Fprintf(&b, " size=%s", formatArtifactSize(result.ArtifactSizeBytes))
			if result.Stats != nil {
				fmt.Fprintf(
					&b,
					" min=%s median=%s p95=%s",
					formatNanos(result.Stats.MinNanos),
					formatNanos(result.Stats.MedianNanos),
					formatNanos(result.Stats.P95Nanos),
				)
			}
			fmt.Fprintln(&b)
			if failure := firstFailure(result.Trials); failure != nil {
				fmt.Fprintf(
					&b,
					"  first failure: trial=%d exit=%d error=%s\n",
					failure.Index,
					failure.ExitCode,
					failure.Error,
				)
			}
		}
	}
	if report.HasFailures() {
		fmt.Fprintf(&b, "\nstatus: failed\n")
	} else {
		fmt.Fprintf(&b, "\nstatus: ok\n")
	}
	return b.String()
}

func firstFailure(trials []trialResult) *trialResult {
	for i := range trials {
		if !trials[i].Success {
			return &trials[i]
		}
	}
	return nil
}

func formatNanos(value int64) string {
	return time.Duration(value).String()
}

func writeJSONReport(path string, report benchmarkReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal benchmark report: %w", err)
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create JSON report directory: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	return nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return info.Size(), nil
}

func measureJustBashArtifactFootprint(ctx context.Context, spec, installRoot string) (int64, error) {
	installCtx, cancel := context.WithTimeout(ctx, primeTimeout)
	defer cancel()

	if err := os.MkdirAll(installRoot, 0o755); err != nil {
		return 0, fmt.Errorf("create just-bash install dir: %w", err)
	}

	initCmd := exec.CommandContext(installCtx, "npm", "init", "-y")
	initCmd.Dir = installRoot
	var initStdout, initStderr bytes.Buffer
	initCmd.Stdout = &initStdout
	initCmd.Stderr = &initStderr
	if err := initCmd.Run(); err != nil {
		return 0, fmt.Errorf("init just-bash install workspace: %w: %s", err, combineOutput(initStdout.String(), initStderr.String()))
	}

	installCmd := exec.CommandContext(installCtx, "npm", "install", "--no-save", "--package-lock=false", spec)
	installCmd.Dir = installRoot
	var stdout, stderr bytes.Buffer
	installCmd.Stdout = &stdout
	installCmd.Stderr = &stderr
	if err := installCmd.Run(); err != nil {
		return 0, fmt.Errorf("install just-bash benchmark package: %w: %s", err, combineOutput(stdout.String(), stderr.String()))
	}

	size, err := directorySize(filepath.Join(installRoot, "node_modules"))
	if err != nil {
		return 0, err
	}
	nodeSize, err := nodeExecutableSize()
	if err != nil {
		return 0, err
	}
	return size + nodeSize, nil
}

func nodeExecutableSize() (int64, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return 0, fmt.Errorf("locate node executable: %w", err)
	}
	size, err := fileSize(nodePath)
	if err != nil {
		return 0, err
	}
	return size, nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure directory size %s: %w", root, err)
	}
	return total, nil
}

func copyFile(dst, src string) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()

	output, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func formatArtifactSize(value int64) string {
	if value <= 0 {
		return "n/a"
	}
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

func executableName(base string) string {
	if goruntime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func combineOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + " " + stderr
	}
}

func (report benchmarkReport) HasFailures() bool {
	for _, scenario := range report.Scenarios {
		for _, result := range scenario.Results {
			if result.FailureCount > 0 {
				return true
			}
		}
	}
	return false
}
