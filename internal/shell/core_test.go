package shell

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/trace"
)

func TestCoreRunExpandsAliasesAcrossCompleteCommands(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	result, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"shopt -s expand_aliases",
			`alias both='echo one && echo two'`,
			"both",
			"",
		}, "\n"),
		Stdout: &stdout,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "one\ntwo\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if result.ShellExited {
		t.Fatalf("ShellExited = true, want false")
	}
}

func TestCoreRunAliasTrailingBlankDoesNotReachRedirectionTargets(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "echo", "printf", "test")
	var stdout strings.Builder

	result, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"shopt -s expand_aliases",
			"alias pre='echo '",
			"alias target='bad'",
			"pre > target",
			"test -f target",
			`printf 'target=%d\n' "$?"`,
			"test -f bad",
			`printf 'bad=%d\n' "$?"`,
			"",
		}, "\n"),
		Env:      map[string]string{"PATH": "/bin"},
		Registry: registry,
		FS:       fsys,
		Stdout:   &stdout,
		Stderr:   io.Discard,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "target=0\nbad=1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if result.ShellExited {
		t.Fatalf("ShellExited = true, want false")
	}
}

func TestCoreRunPreservesLineContinuationsAcrossChunks(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	result, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"printf '%s %s\\n' one \\",
			"two",
			"",
		}, "\n"),
		Stdout: &stdout,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "one two\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if result.ShellExited {
		t.Fatalf("ShellExited = true, want false")
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

func TestCoreRunWaitsForOutputProcessSubstitution(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "printf", "sed")
	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script:   "printf '%s\\n' alpha beta > >(sed 's/^/ps:/')\n",
		Env:      map[string]string{"PATH": "/bin"},
		Registry: registry,
		FS:       fsys,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "ps:alpha\nps:beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCoreRunCallerBuiltin(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "cat", "echo")
	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			`cat > caller-lib.sh <<'EOF'`,
			`inner() {`,
			`  caller 0`,
			`  echo "status=$?"`,
			`  caller 1`,
			`  echo "status=$?"`,
			`  caller 2`,
			`  echo "status=$?"`,
			`}`,
			`outer() {`,
			`  inner`,
			`}`,
			`EOF`,
			`. ./caller-lib.sh`,
			`outer`,
			"",
		}, "\n"),
		Env:      map[string]string{"PATH": "/bin"},
		Registry: registry,
		FS:       fsys,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if got, want := len(lines), 4; got != want {
		t.Fatalf("output lines = %d, want %d: %q", got, want, stdout.String())
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 3 {
		t.Fatalf("caller output fields = %q, want 3 fields", lines[0])
	}
	if got, want := fields[1], "outer"; got != want {
		t.Fatalf("caller function = %q, want %q", got, want)
	}
	if !strings.HasSuffix(fields[2], "caller-lib.sh") {
		t.Fatalf("caller source = %q, want suffix %q", fields[2], "caller-lib.sh")
	}
	if got, want := lines[1], "status=0"; got != want {
		t.Fatalf("first caller status = %q, want %q", got, want)
	}
	if got, want := lines[2], "status=1"; got != want {
		t.Fatalf("second caller status = %q, want %q", got, want)
	}
	if got, want := lines[3], "status=1"; got != want {
		t.Fatalf("third caller status = %q, want %q", got, want)
	}
}

func TestCoreRunCallerBuiltinFromSourcedTopLevel(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "cat")
	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			`cat > caller-inner.sh <<'EOF'`,
			`caller 0`,
			`EOF`,
			`cat > caller-outer.sh <<'EOF'`,
			`. ./caller-inner.sh`,
			`EOF`,
			`. ./caller-outer.sh`,
			"",
		}, "\n"),
		Env:      map[string]string{"PATH": "/bin"},
		Registry: registry,
		FS:       fsys,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}

	line := strings.TrimSpace(stdout.String())
	fields := strings.Fields(line)
	if len(fields) != 3 {
		t.Fatalf("caller output fields = %q, want 3 fields", line)
	}
	if got, want := fields[0], "1"; got != want {
		t.Fatalf("caller line = %q, want %q", got, want)
	}
	if got, want := fields[1], "source"; got != want {
		t.Fatalf("caller function = %q, want %q", got, want)
	}
	if got := fields[2]; got != "./caller-outer.sh" && !strings.HasSuffix(got, "/caller-outer.sh") {
		t.Fatalf("caller source = %q, want caller-outer.sh", got)
	}
}

func TestCoreRunCallerBuiltinIncludesMainFrame(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "echo")
	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			`inner() {`,
			`  caller 0`,
			`  echo "s0=$?"`,
			`  caller 1`,
			`  echo "s1=$?"`,
			`}`,
			`outer() {`,
			`  inner`,
			`}`,
			`outer`,
			"",
		}, "\n"),
		ScriptPath: "/tmp/caller-main.sh",
		Env:        map[string]string{"PATH": "/bin"},
		Registry:   registry,
		FS:         fsys,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if got, want := len(lines), 4; got != want {
		t.Fatalf("output lines = %d, want %d: %q", got, want, stdout.String())
	}
	fields0 := strings.Fields(lines[0])
	if got, want := len(fields0), 3; got != want {
		t.Fatalf("caller 0 fields = %q, want 3 fields", lines[0])
	}
	if got, want := fields0[0], "8"; got != want {
		t.Fatalf("caller 0 line = %q, want %q", got, want)
	}
	if got, want := fields0[1], "outer"; got != want {
		t.Fatalf("caller 0 function = %q, want %q", got, want)
	}
	if got, want := fields0[2], "/tmp/caller-main.sh"; got != want {
		t.Fatalf("caller 0 source = %q, want %q", got, want)
	}
	if got, want := lines[1], "s0=0"; got != want {
		t.Fatalf("caller 0 status = %q, want %q", got, want)
	}

	fields1 := strings.Fields(lines[2])
	if got, want := len(fields1), 3; got != want {
		t.Fatalf("caller 1 fields = %q, want 3 fields", lines[2])
	}
	if got, want := fields1[0], "10"; got != want {
		t.Fatalf("caller 1 line = %q, want %q", got, want)
	}
	if got, want := fields1[1], "main"; got != want {
		t.Fatalf("caller 1 function = %q, want %q", got, want)
	}
	if got, want := fields1[2], "/tmp/caller-main.sh"; got != want {
		t.Fatalf("caller 1 source = %q, want %q", got, want)
	}
	if got, want := lines[3], "s1=0"; got != want {
		t.Fatalf("caller 1 status = %q, want %q", got, want)
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
