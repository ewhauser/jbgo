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
	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/trace"
)

type testEnvMapEnv []struct {
	name string
	vr   expand.Variable
}

func (e testEnvMapEnv) Get(name string) expand.Variable {
	for i := len(e) - 1; i >= 0; i-- {
		if e[i].name == name {
			return e[i].vr
		}
	}
	return expand.Variable{}
}

func (e testEnvMapEnv) Each(fn func(name string, vr expand.Variable) bool) {
	for _, entry := range e {
		if !fn(entry.name, entry.vr) {
			return
		}
	}
}

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

func TestEnvMapExportsOnlyExportedShellVars(t *testing.T) {
	t.Parallel()

	got := envMap(testEnvMapEnv{
		{name: "plain", vr: expand.Variable{Set: true, Kind: expand.String, Str: "value"}},
		{name: "exported", vr: expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: "value"}},
		{name: "ref", vr: expand.Variable{Set: true, Exported: true, Kind: expand.NameRef, Str: "target"}},
		{name: "shadow", vr: expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: "parent"}},
		{name: "shadow", vr: expand.Variable{Set: true, Kind: expand.String, Str: "local"}},
	})

	if got["exported"] != "value" {
		t.Fatalf("exported = %q, want %q", got["exported"], "value")
	}
	if got["ref"] != "target" {
		t.Fatalf("ref = %q, want %q", got["ref"], "target")
	}
	if _, ok := got["plain"]; ok {
		t.Fatalf("plain present in env map: %#v", got)
	}
	if _, ok := got["shadow"]; ok {
		t.Fatalf("shadow present in env map: %#v", got)
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

func TestCoreRunUsesShellPrintfBuiltinForFormatting(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "printf")
	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"single=\"'A\"",
			"text='a b'",
			`printf '%d|%X|%.1f|%q\n' "$single" 31 1.25 "$text"`,
			`printf -v whole '%d|%X|%.1f|%q' "$single" 31 1.25 "$text"`,
			`printf '<%s>\n' "$whole"`,
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
	const want = "65|1F|1.2|a\\ b\n<65|1F|1.2|a\\ b>\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestCoreRunUsesShellPrintfBuiltinForVarRefs(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "echo", "printf")
	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"typeset -A assoc=([k]=v)",
			"key=k",
			"single=\"'A\"",
			"printf -v 'assoc[$key]' '%d' \"$single\"",
			`echo "assoc=${assoc[k]} status=$?"`,
			"array=(a b '')",
			"printf -v 'array[1+1]' %X 31",
			`echo "array=${array[2]} status=$?"`,
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
	if got, want := stdout.String(), "assoc=65 status=0\narray=1F status=0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestCoreRunUsesBuiltinCompgenWithoutRegistryWrapper(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"compgen -A builtin g",
			"echo ---",
			"builtin compgen -A builtin g",
			"echo ---",
			"command compgen -A builtin g",
			"echo ---",
			"/bin/compgen -A builtin g",
			"echo wrapped=$?",
			"",
		}, "\n"),
		Env:      map[string]string{"PATH": "/bin"},
		Registry: commands.NewRegistry(),
		FS:       newShellTestFS(t),
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}
	const want = "" +
		"getopts\n" +
		"---\n" +
		"getopts\n" +
		"---\n" +
		"getopts\n" +
		"---\n" +
		"wrapped=127\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCoreRunUsesShellTestBuiltinForVarRefs(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "echo", "printf", "test", "[")
	var stdout strings.Builder
	var stderr strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"typeset -A assoc=([empty]='' [k]=v)",
			"test -v assoc[empty]",
			`echo "assoc-empty=$?"`,
			"test -v assoc[k]",
			`echo "assoc-k=$?"`,
			"test -v assoc[missing]",
			`echo "assoc-missing=$?"`,
			"key=k",
			"test -v 'assoc[$key]'",
			`echo "assoc-dynamic=$?"`,
			"array=(1 2 3 '')",
			"test -v 'array[3]'",
			`echo "array-empty=$?"`,
			"test -v 'array[1+1]'",
			`echo "array-arith=$?"`,
			"[ -v assoc[k] ]",
			`echo "bracket=$?"`,
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
	const want = "" +
		"assoc-empty=0\n" +
		"assoc-k=0\n" +
		"assoc-missing=1\n" +
		"assoc-dynamic=0\n" +
		"array-empty=0\n" +
		"array-arith=0\n" +
		"bracket=0\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestCoreRunPreservesArithmeticDiagnosticsInsideFunctions(t *testing.T) {
	t.Parallel()

	var stderr strings.Builder
	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"_f() {",
			"  COMPREPLY+=( $(( 1 / 0 )) )",
			"}",
			"_f",
			"",
		}, "\n"),
		Stdout: io.Discard,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stderr.String(), "1 / 0 : division by 0 (error token is \"0 \")\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCoreRunCompgenFunctionErrorsSuppressPartialReplies(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"_f() {",
			"  COMPREPLY=( foo bar )",
			"  COMPREPLY+=( $(( 1 / 0 )) )",
			"}",
			"compgen -F _f foo",
			"echo status=$?",
			"",
		}, "\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "status=1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "" +
		"compgen: warning: -F option may not work as you expect\n" +
		"1 / 0 : division by 0 (error token is \"0 \")\n"
	if got := stderr.String(); got != wantStderr {
		t.Fatalf("stderr = %q, want %q", got, wantStderr)
	}
}

func TestCoreRunCompgenCommandHookPreservesStdoutOrder(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"f() { echo foo; echo bar; }",
			"compgen -C f b",
			"echo status=$?",
			"",
		}, "\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "foo\nbar\nstatus=0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "compgen: warning: -C option may not work as you expect\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCoreRunCompgenWordlistHonorsEscapedIFSDelimiters(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"IFS=':%'",
			`compgen -W 'spam:eggs%ham cheese\:colon'`,
			"",
		}, "\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "spam\neggs\nham cheese:colon\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
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

func TestCoreRunOmitsNonStringVarsFromCommandEnv(t *testing.T) {
	t.Parallel()

	envProbe := commands.DefineCommand("envprobe", func(ctx context.Context, inv *commands.Invocation) error {
		presence := func(name string) string {
			if _, ok := inv.Env[name]; ok {
				return "set"
			}
			return "unset"
		}
		_, _ = io.WriteString(inv.Stdout, "scalar="+inv.Env["scalar"]+" array="+presence("array")+" assoc="+presence("assoc")+"\n")
		return nil
	})

	registry := newShellTestRegistry(t, envProbe)
	fsys := newShellTestFS(t, "envprobe")
	var stdout strings.Builder

	_, err := Run(context.Background(), &Execution{
		Script: strings.Join([]string{
			"scalar=value",
			"typeset -a array=(1 2 3)",
			"typeset -A assoc=([foo]=bar)",
			"export scalar array assoc",
			"envprobe",
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
	if got, want := stdout.String(), "scalar=value array=unset assoc=unset\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestLookupCommandPrefersRealExecutableOverRegistryStub(t *testing.T) {
	t.Parallel()

	registry := newShellTestRegistry(t)
	fsys := newShellTestFS(t, "tr")
	if err := fsys.MkdirAll(context.Background(), "/tmp/bin", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp/bin) error = %v", err)
	}
	file, err := fsys.OpenFile(context.Background(), "/tmp/bin/tr", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("OpenFile(/tmp/bin/tr) error = %v", err)
	}
	if _, err := io.WriteString(file, "echo wrong\n"); err != nil {
		t.Fatalf("WriteString(/tmp/bin/tr) error = %v", err)
	}
	_ = file.Close()

	resolved, ok, err := lookupCommand(context.Background(), &Execution{
		FS:       fsys,
		Registry: registry,
	}, "/tmp", expand.ListEnviron("PATH=/tmp/bin:/bin"), "tr")
	if err != nil {
		t.Fatalf("lookupCommand() error = %v", err)
	}
	if !ok {
		t.Fatalf("lookupCommand() did not resolve command")
	}
	if got, want := resolved.name, "bash"; got != want {
		t.Fatalf("resolved.name = %q, want %q", got, want)
	}
	if got, want := resolved.path, "/tmp/bin/tr"; got != want {
		t.Fatalf("resolved.path = %q, want %q", got, want)
	}
	if got, want := resolved.source, "shell-script"; got != want {
		t.Fatalf("resolved.source = %q, want %q", got, want)
	}
	if got, want := strings.Join(resolved.args, ","), "/tmp/bin/tr"; got != want {
		t.Fatalf("resolved.args = %q, want %q", got, want)
	}
}

func TestLookupCommandUsesStubBackedPathEntry(t *testing.T) {
	t.Parallel()

	resolved, ok, err := lookupCommand(context.Background(), &Execution{
		FS:       newShellTestFS(t, "tr"),
		Registry: newShellTestRegistry(t),
	}, "/tmp", expand.ListEnviron("PATH=/bin"), "tr")
	if err != nil {
		t.Fatalf("lookupCommand() error = %v", err)
	}
	if !ok {
		t.Fatalf("lookupCommand() did not resolve command")
	}
	if got, want := resolved.name, "tr"; got != want {
		t.Fatalf("resolved.name = %q, want %q", got, want)
	}
	if got, want := resolved.path, "/bin/tr"; got != want {
		t.Fatalf("resolved.path = %q, want %q", got, want)
	}
	if got, want := resolved.source, "path-search"; got != want {
		t.Fatalf("resolved.source = %q, want %q", got, want)
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
		if _, err := io.WriteString(file, virtualCommandStubPrefix+name+"\n"); err != nil {
			t.Fatalf("WriteString(%s) error = %v", name, err)
		}
		_ = file.Close()
	}
	return fsys
}
