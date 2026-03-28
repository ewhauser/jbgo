package interp

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/completionutil"
	"github.com/ewhauser/gbash/shellvariant"
)

func TestCompletionBackendRunCommandHookMatchesDirectFunctionOrder(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.runShellReader(context.Background(), strings.NewReader("f() { echo foo; echo bar; }\n"), "completion-backend-test.sh", nil)
	if err != nil {
		t.Fatalf("runShellReader error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	runner.call(context.Background(), todoPos, []string{"f"})
	if got, want := stdout.String(), "foo\nbar\n"; got != want {
		t.Fatalf("direct call stdout = %q, want %q", got, want)
	}
	if got := runner.exit.code; got != 0 {
		t.Fatalf("direct call exit code = %d, want 0", got)
	}

	stdout.Reset()
	stderr.Reset()
	backend := newRunnerCompletionBackend(context.Background(), runner, nil)
	got := backend.RunCommandHook("f", completionutil.HookRequest{Word: "b"})
	want := completionutil.HookResult{
		Candidates: []string{"foo", "bar"},
		Status:     0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RunCommandHook() = %#v, want %#v", got, want)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCompletionBackendRunFunctionPropagatesExpansionErrors(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	src := strings.Join([]string{
		"_f() {",
		"  COMPREPLY=( foo bar )",
		"  COMPREPLY+=( $(( 1 / 0 )) )",
		"}",
		"",
	}, "\n")
	err = runner.runShellReader(context.Background(), strings.NewReader(src), "completion-backend-test.sh", nil)
	if err != nil {
		t.Fatalf("runShellReader error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	runner.call(context.Background(), todoPos, []string{"_f", "compgen", "foo", ""})
	if got, want := stderr.String(), "1 / 0 : division by 0 (error token is \"0 \")\n"; got != want {
		t.Fatalf("direct call stderr = %q, want %q", got, want)
	}

	stdout.Reset()
	stderr.Reset()
	backend := newRunnerCompletionBackend(context.Background(), runner, nil)
	got := backend.RunFunction("_f", completionutil.HookRequest{Word: "foo"})
	want := completionutil.HookResult{Status: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RunFunction() = %#v, want %#v (stderr=%q)", got, want, stderr.String())
	}
}

func TestCompletionBackendValidateWordlistSyntaxUsesShellVariant(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(&RunnerConfig{
		Dir:          "/tmp",
		ShellVariant: shellvariant.SH,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	backend := newRunnerCompletionBackend(context.Background(), runner, nil)
	err = backend.ValidateWordlistSyntax("${foo[1]}")
	if err == nil {
		t.Fatal("ValidateWordlistSyntax() error = nil, want posix parse error")
	}
	if !strings.Contains(err.Error(), "posix") {
		t.Fatalf("ValidateWordlistSyntax() error = %v, want posix diagnostic", err)
	}
}
