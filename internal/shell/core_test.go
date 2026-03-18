package shell

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/trace"
)

func TestCoreRunParsesScriptOnce(t *testing.T) {
	t.Parallel()

	parser := syntax.NewParser()
	parseCount := 0
	m := &core{
		parseFunc: func(name, script string) (*syntax.File, error) {
			parseCount++
			return parser.Parse(strings.NewReader(script), name)
		},
	}

	if _, err := m.Run(context.Background(), &Execution{Script: "true\n"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := parseCount, 1; got != want {
		t.Fatalf("parse count = %d, want %d", got, want)
	}
}

func TestCoreRunTracePreservesUserLineNumbers(t *testing.T) {
	t.Parallel()

	recorder := trace.NewBuffer()
	_, err := Run(context.Background(), &Execution{
		Script: "true\ntrue\n",
		Trace:  recorder,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var positions []string
	for _, event := range recorder.Snapshot() {
		if event.Kind != trace.EventCallExpanded || event.Command == nil || event.Command.Name != "true" {
			continue
		}
		positions = append(positions, event.Command.Position)
	}
	if got, want := strings.Join(positions, ","), "1:1,2:1"; got != want {
		t.Fatalf("positions = %q, want %q", got, want)
	}
}

func TestCoreRunPreservesLastpipeBehavior(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "printf")

	defaultResult, err := Run(context.Background(), &Execution{
		Script:   "printf 'value\\n' | read line\n",
		Env:      map[string]string{"PATH": "/bin"},
		Registry: registry,
		FS:       fsys,
	})
	if err != nil {
		t.Fatalf("default Run() error = %v", err)
	}
	if got := defaultResult.FinalEnv["line"]; got != "" {
		t.Fatalf("default lastpipe final line = %q, want empty", got)
	}

	lastpipeResult, err := Run(context.Background(), &Execution{
		Script:   "shopt -s lastpipe\nprintf 'value\\n' | read line\n",
		Env:      map[string]string{"PATH": "/bin"},
		Registry: registry,
		FS:       fsys,
	})
	if err != nil {
		t.Fatalf("lastpipe Run() error = %v", err)
	}
	if got, want := lastpipeResult.FinalEnv["line"], "value"; got != want {
		t.Fatalf("lastpipe final line = %q, want %q", got, want)
	}
}

func TestCoreRunSyncsShellStateWithoutBootstrapEval(t *testing.T) {
	t.Parallel()

	stateProbe := commands.DefineCommand("stateprobe", func(ctx context.Context, inv *commands.Invocation) error {
		assignments := shellstate.ShellVarAssignmentsFromContext(ctx)
		if assignments == nil {
			t.Fatal("ShellVarAssignmentsFromContext() = nil")
		}
		assignments.Set("FOO", "shell-value")
		inv.Env[shellHistoryEnvVar] = `["stateprobe"]`
		inv.Env[umaskEnvVar] = "0077"
		return nil
	})

	registry := newShellTestRegistry(t, stateProbe)
	fsys := newShellTestFS(t, "stateprobe", "printf")
	var stdout strings.Builder

	result, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"stateprobe",
			`printf '%s|%s|%s\n' "$FOO" "$BASH_HISTORY" "$GBASH_UMASK"`,
			"",
		}, "\n"),
		Env:      map[string]string{"PATH": "/bin"},
		Registry: registry,
		FS:       fsys,
		Stdout:   &stdout,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), `shell-value|["stateprobe"]|0077`+"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.FinalEnv["FOO"], "shell-value"; got != want {
		t.Fatalf("FinalEnv[FOO] = %q, want %q", got, want)
	}
	if got, want := result.FinalEnv[shellHistoryEnvVar], `["stateprobe"]`; got != want {
		t.Fatalf("FinalEnv[%s] = %q, want %q", shellHistoryEnvVar, got, want)
	}
	if got, want := result.FinalEnv[umaskEnvVar], "0077"; got != want {
		t.Fatalf("FinalEnv[%s] = %q, want %q", umaskEnvVar, got, want)
	}
}

func newShellTestRegistry(t testing.TB, extras ...commands.Command) *commands.Registry {
	t.Helper()

	registry := builtins.DefaultRegistry()
	for _, cmd := range extras {
		if err := registry.Register(cmd); err != nil {
			t.Fatalf("Register(%s) error = %v", cmd.Name(), err)
		}
	}
	return registry
}

func newShellTestFS(t testing.TB, names ...string) gbfs.FileSystem {
	t.Helper()

	fsys := gbfs.NewMemory()
	if err := fsys.MkdirAll(context.Background(), "/bin", 0o755); err != nil {
		t.Fatalf("MkdirAll(/bin) error = %v", err)
	}
	for _, name := range names {
		file, err := fsys.OpenFile(context.Background(), "/bin/"+name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			t.Fatalf("OpenFile(%s) error = %v", name, err)
		}
		_ = file.Close()
	}
	return fsys
}
