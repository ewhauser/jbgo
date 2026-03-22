package interp

import (
	"fmt"
	"io"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/expand"
)

var (
	benchmarkOverlayEachSink int
	benchmarkShellEnvSink    map[string]string
)

type envBenchCase struct {
	size  int
	depth int
}

func benchmarkEnvCases() []envBenchCase {
	return []envBenchCase{
		{size: 64, depth: 1},
		{size: 64, depth: 4},
		{size: 64, depth: 8},
		{size: 256, depth: 1},
		{size: 256, depth: 4},
		{size: 256, depth: 8},
		{size: 512, depth: 1},
		{size: 512, depth: 4},
		{size: 512, depth: 8},
	}
}

func BenchmarkOverlayEachVisible(b *testing.B) {
	for _, tc := range benchmarkEnvCases() {
		env := benchmarkOverlayFixture(tc.size, tc.depth)
		b.Run(fmt.Sprintf("n=%d/depth=%d", tc.size, tc.depth), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				sum := 0
				for name, vr := range env.Each() {
					sum += len(name)
					if vr.IsSet() {
						sum += len(vr.String())
					}
				}
				benchmarkOverlayEachSink = sum
			}
		})
	}
}

func BenchmarkRunnerShellEnv(b *testing.B) {
	for _, tc := range benchmarkEnvCases() {
		runner := benchmarkRunnerFixture(tc.size, tc.depth)
		b.Run(fmt.Sprintf("n=%d/depth=%d", tc.size, tc.depth), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				benchmarkShellEnvSink = runner.ShellEnv()
			}
		})
	}
}

func BenchmarkRunnerPrintSetVars(b *testing.B) {
	for _, tc := range benchmarkEnvCases() {
		runner := benchmarkRunnerFixture(tc.size, tc.depth)
		b.Run(fmt.Sprintf("n=%d/depth=%d", tc.size, tc.depth), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				runner.printSetVars()
			}
		})
	}
}

func benchmarkRunnerFixture(size, depth int) *Runner {
	runner := newRunnerBase()
	runner.didReset = true
	runner.stdout = io.Discard
	runner.writeEnv = benchmarkOverlayFixture(size, depth)
	return runner
}

func benchmarkOverlayFixture(size, depth int) expand.WriteEnviron {
	pairs := make([]string, 0, size+4)
	for i := range size {
		pairs = append(pairs, fmt.Sprintf("VAR_%03d=base_%03d", i, i))
	}
	pairs = append(pairs,
		"PATH=/usr/bin:/bin",
		"PWD=/tmp/bench",
		"SHELLOPTS=hashall:interactive-comments",
		"BASHOPTS=checkwinsize",
	)

	var env expand.Environ = expand.ListEnviron(pairs...)
	for level := range depth {
		overlay := &overlayEnviron{parent: env}
		for i := range size {
			name := fmt.Sprintf("VAR_%03d", i)
			switch {
			case i%11 == 0:
				benchmarkMustSetVar(overlay, name, expand.Variable{})
			case i%5 == 0:
				benchmarkMustSetVar(overlay, name, expand.Variable{
					Set:  true,
					Kind: expand.String,
					Str:  fmt.Sprintf("local_%d_%03d", level, i),
				})
			default:
				benchmarkMustSetVar(overlay, name, expand.Variable{
					Set:      true,
					Exported: true,
					Kind:     expand.String,
					Str:      fmt.Sprintf("export_%d_%03d", level, i),
				})
			}
		}
		for i := 0; i < size/8; i++ {
			name := fmt.Sprintf("DEPTH_%d_ONLY_%03d", level, i)
			vr := expand.Variable{
				Set:  true,
				Kind: expand.String,
				Str:  fmt.Sprintf("depth_%d_only_%03d", level, i),
			}
			if (level+i)%2 == 0 {
				vr.Exported = true
			}
			benchmarkMustSetVar(overlay, name, vr)
		}
		env = overlay
	}

	top := &overlayEnviron{parent: env}
	benchmarkMustSetVar(top, "SHELLOPTS", expand.Variable{
		Set:      true,
		Exported: true,
		ReadOnly: true,
		Kind:     expand.String,
		Str:      "braceexpand:hashall:interactive-comments",
	})
	benchmarkMustSetVar(top, "BASHOPTS", expand.Variable{
		Set:      true,
		Exported: true,
		ReadOnly: true,
		Kind:     expand.String,
		Str:      "checkwinsize:cmdhist",
	})
	return top
}

func benchmarkMustSetVar(env expand.WriteEnviron, name string, vr expand.Variable) {
	if err := env.Set(name, vr); err != nil {
		panic(fmt.Sprintf("benchmark fixture Set(%q): %v", name, err))
	}
}
