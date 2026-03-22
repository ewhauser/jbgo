package interp

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

type subshellBenchProfile struct {
	name        string
	env         int
	funcs       int
	aliases     int
	commandHash int
	extraFDs    int
	frames      int
	dirStack    int
}

func BenchmarkSubshellSnapshot(b *testing.B) {
	for _, profile := range []subshellBenchProfile{
		{name: "medium", env: 200, funcs: 64, aliases: 64, commandHash: 128, extraFDs: 16, frames: 8, dirStack: 8},
		{name: "large", env: 1000, funcs: 256, aliases: 128, commandHash: 256, extraFDs: 32, frames: 16, dirStack: 16},
	} {
		base := newSubshellBenchRunner(b, profile)
		b.Run(profile.name, func(b *testing.B) {
			b.Run("foreground_readonly", func(b *testing.B) {
				benchmarkSubshellSnapshotCase(b, base, false, func(r *Runner) {
					_ = r.lookupVar("BENCH_ENV_0000").String()
					_, _ = r.funcInfo("bench_func_000")
					_, _ = r.commandHashLookup("bench_cmd_000")
					_ = r.getFD(10)
					_ = len(r.frames)
					_ = len(r.dirStack)
				})
			})
			b.Run("background_readonly", func(b *testing.B) {
				benchmarkSubshellSnapshotCase(b, base, true, func(r *Runner) {
					_ = r.lookupVar("BENCH_ENV_0000").String()
					_, _ = r.funcInfo("bench_func_000")
					_, _ = r.commandHashLookup("bench_cmd_000")
					_ = r.getFD(10)
					_ = len(r.frames)
					_ = len(r.dirStack)
				})
			})
			b.Run("background_first_env_write", func(b *testing.B) {
				benchmarkSubshellSnapshotCase(b, base, true, func(r *Runner) {
					r.setVarString("BENCH_MUTATED_ENV", "value")
				})
			})
			b.Run("background_first_shellstate_write", func(b *testing.B) {
				benchmarkSubshellSnapshotCase(b, base, true, func(r *Runner) {
					r.setFuncInfo("bench_func_mutated", funcInfo{definitionSource: "benchmark"})
				})
			})
			b.Run("background_first_fd_write", func(b *testing.B) {
				benchmarkSubshellSnapshotCase(b, base, true, func(r *Runner) {
					r.setFD(99, newShellOutputFD(io.Discard))
				})
			})
		})
	}
}

func BenchmarkPipelineBurst(b *testing.B) {
	ctx := context.Background()
	file := parseSubshellBenchFile(b, strings.Repeat(": | : | :\n", 64))
	for _, profile := range []subshellBenchProfile{
		{name: "medium", env: 200, funcs: 64, aliases: 64, commandHash: 128, extraFDs: 16, frames: 8, dirStack: 8},
		{name: "large", env: 1000, funcs: 256, aliases: 128, commandHash: 256, extraFDs: 32, frames: 16, dirStack: 16},
	} {
		base := newSubshellBenchRunner(b, profile)
		b.Run(profile.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runner := base.subshell(false)
				if err := runner.Run(ctx, file); err != nil {
					b.Fatalf("Run() error = %v", err)
				}
			}
		})
	}
}

func benchmarkSubshellSnapshotCase(b *testing.B, base *Runner, background bool, body func(*Runner)) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runner := base.subshell(background)
		body(runner)
	}
}

func newSubshellBenchRunner(tb testing.TB, profile subshellBenchProfile) *Runner {
	tb.Helper()

	envPairs := make([]string, 0, profile.env+1)
	envPairs = append(envPairs, "HOME=/tmp")
	for i := 0; i < profile.env; i++ {
		envPairs = append(envPairs, fmt.Sprintf("BENCH_ENV_%04d=value_%04d", i, i))
	}
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Env:    expand.ListEnviron(envPairs...),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		tb.Fatalf("NewRunner() error = %v", err)
	}
	runner.Reset()
	runner.fillExpandConfig(context.Background())

	if profile.funcs > 0 {
		runner.funcs = make(map[string]funcInfo, profile.funcs)
		for i := 0; i < profile.funcs; i++ {
			runner.funcs[fmt.Sprintf("bench_func_%03d", i)] = funcInfo{definitionSource: "benchmark"}
		}
	}
	if profile.aliases > 0 {
		runner.alias = make(map[string]alias, profile.aliases)
		for i := 0; i < profile.aliases; i++ {
			runner.alias[fmt.Sprintf("bench_alias_%03d", i)] = alias{value: "echo alias"}
		}
	}
	if profile.commandHash > 0 {
		runner.commandHash = make(map[string]commandHashEntry, profile.commandHash)
		for i := 0; i < profile.commandHash; i++ {
			runner.commandHash[fmt.Sprintf("bench_cmd_%03d", i)] = commandHashEntry{path: "/bin/true", hits: i}
		}
	}
	for i := 0; i < profile.extraFDs; i++ {
		runner.setFD(shellNamedFDStart+i, newShellOutputFD(io.Discard))
	}
	runner.frames = make([]execFrame, profile.frames)
	for i := 0; i < profile.frames; i++ {
		runner.frames[i] = execFrame{kind: frameKindFunction, label: fmt.Sprintf("frame_%02d", i)}
	}
	runner.dirStack = runner.dirBootstrap[:0]
	for i := 0; i < profile.dirStack; i++ {
		runner.dirStack = append(runner.dirStack, fmt.Sprintf("/tmp/stack/%02d", i))
	}
	return runner
}

func parseSubshellBenchFile(tb testing.TB, src string) *syntax.File {
	tb.Helper()
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "subshell-benchmark.sh")
	if err != nil {
		tb.Fatalf("Parse() error = %v", err)
	}
	return file
}
