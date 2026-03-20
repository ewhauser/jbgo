package builtins_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	gbash "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/internal/shellstate"
)

func TestCompoptPersistsAcrossCommandsInScript(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "complete -W 'foo bar' mycommand\ncompopt -o nospace mycommand\ncomplete -p mycommand\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "complete -o nospace -W 'foo bar' mycommand\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestCompoptErrorsWithoutActiveCompletionContext(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "compopt -o filenames +o nospace\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stderr, "compopt: not currently executing completion function\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestCompoptInvalidOptionReturnsExitCodeTwo(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "compopt -o invalid cmd\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if got, want := result.Stderr, "compopt: invalid: invalid option name\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestCompoptModifiesDefaultAndEmptyCompletionScopes(t *testing.T) {
	t.Parallel()
	state := shellstate.NewCompletionState()
	ctx := shellstate.WithCompletionState(context.Background(), state)

	if exitCode := runBuiltin(t, ctx, builtins.NewComplete(), "-F", "myfunc", "-D"); exitCode != 0 {
		t.Fatalf("complete -F myfunc -D exit code = %d, want 0", exitCode)
	}
	if exitCode := runBuiltin(t, ctx, builtins.NewCompopt(), "-D", "-o", "nospace", "-o", "filenames"); exitCode != 0 {
		t.Fatalf("compopt -D exit code = %d, want 0", exitCode)
	}
	defaultSpec, ok := state.Get(shellstate.CompletionSpecDefaultKey)
	if !ok {
		t.Fatalf("default completion spec missing")
	}
	if !defaultSpec.IsDefault {
		t.Fatalf("default completion spec IsDefault = false, want true")
	}
	if got, want := defaultSpec.Function, "myfunc"; got != want || !defaultSpec.HasFunction {
		t.Fatalf("default completion function = %q (has=%v), want %q", got, defaultSpec.HasFunction, want)
	}
	if got, want := defaultSpec.Options, []string{"nospace", "filenames"}; !slices.Equal(got, want) {
		t.Fatalf("default completion options = %v, want %v", got, want)
	}

	if exitCode := runBuiltin(t, ctx, builtins.NewCompopt(), "-E", "-o", "default"); exitCode != 0 {
		t.Fatalf("compopt -E exit code = %d, want 0", exitCode)
	}
	emptySpec, ok := state.Get(shellstate.CompletionSpecEmptyKey)
	if !ok {
		t.Fatalf("empty-line completion spec missing")
	}
	if got, want := emptySpec.Options, []string{"default"}; !slices.Equal(got, want) {
		t.Fatalf("empty-line completion options = %v, want %v", got, want)
	}
}

func TestCompoptPreservesExistingSpecWhileDisablingOptions(t *testing.T) {
	t.Parallel()
	state := shellstate.NewCompletionState()
	ctx := shellstate.WithCompletionState(context.Background(), state)

	if exitCode := runBuiltin(t, ctx, builtins.NewComplete(), "-o", "nospace", "-o", "filenames", "-F", "myfunc", "cmd"); exitCode != 0 {
		t.Fatalf("complete exit code = %d, want 0", exitCode)
	}
	if exitCode := runBuiltin(t, ctx, builtins.NewCompopt(), "+o", "nospace", "cmd"); exitCode != 0 {
		t.Fatalf("compopt exit code = %d, want 0", exitCode)
	}

	spec, ok := state.Get("cmd")
	if !ok {
		t.Fatalf("command completion spec missing")
	}
	if got, want := spec.Function, "myfunc"; got != want || !spec.HasFunction {
		t.Fatalf("completion function = %q (has=%v), want %q", got, spec.HasFunction, want)
	}
	if got, want := spec.Options, []string{"filenames"}; !slices.Equal(got, want) {
		t.Fatalf("completion options = %v, want %v", got, want)
	}
}

func TestCompoptPersistsAcrossInteractiveEntries(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder
	result, err := session.Interact(context.Background(), &gbash.InteractiveRequest{
		Stdin:  strings.NewReader("complete -F myfunc cmd\ncompopt -o nospace cmd\ncomplete -p cmd\nexit\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Interact() error = %v", err)
	}
	if result == nil {
		t.Fatalf("Interact() result = nil")
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if !strings.Contains(stdout.String(), "complete -o nospace -F myfunc cmd\n") {
		t.Fatalf("stdout = %q, want completion output", stdout.String())
	}
}

func TestCompletionWrappersShareStateWithShellBuiltins(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"complete -W 'foo bar' cmd\n"+
		"/bin/complete -p cmd\n"+
		"compopt -o nospace cmd\n"+
		"/bin/complete -p cmd\n"+
		"/bin/compopt +o nospace cmd\n"+
		"complete -p cmd\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, result.Stderr)
	}
	const want = "" +
		"complete -W 'foo bar' cmd\n" +
		"complete -o nospace -W 'foo bar' cmd\n" +
		"complete -W 'foo bar' cmd\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestCompgenMatchesAcrossBuiltinAndWrapperEntryPoints(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"compgen -A builtin g\n"+
		"echo ---\n"+
		"builtin compgen -A builtin g\n"+
		"echo ---\n"+
		"command compgen -A builtin g\n"+
		"echo ---\n"+
		"/bin/compgen -A builtin g\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, result.Stderr)
	}
	const want = "" +
		"getopts\n" +
		"---\n" +
		"getopts\n" +
		"---\n" +
		"getopts\n" +
		"---\n" +
		"getopts\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCompgenAcceptsKeywordAndExportActions(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"export MATCH_ME=1\n"+
		"compgen -A export MATCH_\n"+
		"echo ---\n"+
		"compgen -A keyword wh\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, result.Stderr)
	}
	const want = "" +
		"MATCH_ME\n" +
		"---\n" +
		"while\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func runBuiltin(tb testing.TB, ctx context.Context, cmd commands.Command, args ...string) (exitCode int) {
	tb.Helper()

	var outBuf strings.Builder
	var errBuf strings.Builder
	inv := commands.NewInvocation(&commands.InvocationOptions{
		Args:   append([]string(nil), args...),
		Env:    defaultBaseEnv(),
		Cwd:    defaultHomeDir,
		Stdin:  strings.NewReader(""),
		Stdout: &outBuf,
		Stderr: &errBuf,
	})

	err := commands.RunCommand(ctx, cmd, inv)
	if err != nil {
		code, ok := commands.ExitCode(err)
		if !ok {
			tb.Fatalf("RunCommand(%T, %v) error = %v", cmd, args, err)
		}
		exitCode = code
	}

	return exitCode
}
