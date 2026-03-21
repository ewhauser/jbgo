package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/trace"
)

func TestExecScriptPathSetsBashIntrospection(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ScriptPath: "main.sh",
		Script: strings.Join([]string{
			"set -u",
			`printf 'ZERO:%s\n' "$0"`,
			`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
			`printf 'LINE:%s\n' "${BASH_LINENO[0]}"`,
			`printf 'FUNC:%s\n' "${FUNCNAME-unset}"`,
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.Stdout, strings.Join([]string{
		"ZERO:main.sh",
		"SRC:main.sh",
		"LINE:0",
		"FUNC:unset",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExecNameOnlyLeavesBashSourceUnset(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Name: "inline.sh",
		Script: strings.Join([]string{
			"set -u",
			`printf 'ZERO:%s\n' "$0"`,
			`printf 'SRC:%s\n' "${BASH_SOURCE[0]-unset}"`,
			`printf 'LINE:%s\n' "${BASH_LINENO[0]-unset}"`,
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.Stdout, strings.Join([]string{
		"ZERO:inline.sh",
		"SRC:unset",
		"LINE:unset",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExecInlineCommandStringSetsBashExecutionString(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	script := strings.Join([]string{
		"declare -p BASH_EXECUTION_STRING",
		"BASH_EXECUTION_STRING=override",
		"echo status=$?",
		"declare -p BASH_EXECUTION_STRING",
		"",
	}, "\n")
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Name:   "inline.sh",
		Script: script,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.Stdout, strings.Join([]string{
		"declare -- BASH_EXECUTION_STRING=$'declare -p BASH_EXECUTION_STRING\\nBASH_EXECUTION_STRING=override\\necho status=$?\\ndeclare -p BASH_EXECUTION_STRING\\n'",
		"status=0",
		"declare -- BASH_EXECUTION_STRING=\"override\"",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExecInlineFunctionsTrackCallLinesWithExecutionNameSource(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Name: "inline.sh",
		Script: strings.Join([]string{
			"f() {",
			`  printf 'FUNC:%s\n' "${FUNCNAME[*]-}"`,
			`  printf 'SRC:%s\n' "${BASH_SOURCE[*]-}"`,
			`  printf 'LINE:%s\n' "${BASH_LINENO[*]-}"`,
			"}",
			"f",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.Stdout, strings.Join([]string{
		"FUNC:f",
		"SRC:inline.sh",
		"LINE:6",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExecInlineFunctionsUseExecutionNameForExtdebugSource(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Name: "inline.sh",
		Script: strings.Join([]string{
			"shopt -s extdebug",
			"f() { :; }",
			"declare -F f",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.Stdout, "f 2 inline.sh\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExecInlineSourceTracksCallLinesWithoutPseudoFrames(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/lib.sh", []byte(strings.Join([]string{
		`printf 'SRC:%s\n' "${BASH_SOURCE[*]-}"`,
		`printf 'LINE:%s\n' "${BASH_LINENO[*]-}"`,
		"",
	}, "\n")))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Name:   "inline.sh",
		Script: "source /lib.sh\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.Stdout, strings.Join([]string{
		"SRC:/lib.sh",
		"LINE:1",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExecRejectsScriptPathWithCommand(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	_, err := session.Exec(context.Background(), &ExecutionRequest{
		ScriptPath: "main.sh",
		Command:    []string{"echo", "hi"},
	})
	if err == nil {
		t.Fatal("Exec() error = nil, want validation error")
	}
	if got, want := err.Error(), "execution request cannot set both ScriptPath and Command"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestNestedBashFileExecPropagatesScriptPath(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/child.sh", []byte(strings.Join([]string{
		"set -u",
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]}"`,
		`printf 'LINE:%s\n' "${BASH_LINENO[0]}"`,
		"",
	}, "\n")))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "bash /child.sh\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.Stdout, strings.Join([]string{
		"ZERO:/child.sh",
		"SRC:/child.sh",
		"LINE:0",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTraceUsesRealUserLineNumbers(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{
		Tracing: TraceConfig{Mode: TraceRaw},
	})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo one\necho two\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	var positions []string
	for i := range result.Events {
		event := result.Events[i]
		if event.Kind != trace.EventCallExpanded || event.Command == nil || event.Command.Name != "echo" {
			continue
		}
		positions = append(positions, event.Command.Position)
	}

	if got, want := positions, []string{"1:1", "2:1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("echo positions = %#v, want %#v", got, want)
	}
}

func TestTraceTreatsLiteralBootstrapNameAsUserScript(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{
		Tracing: TraceConfig{Mode: TraceRaw},
	})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ScriptPath: "<gbash-prelude>",
		Script:     "pwd\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	var names []string
	var positions []string
	for i := range result.Events {
		event := result.Events[i]
		if event.Kind != trace.EventCallExpanded || event.Command == nil {
			continue
		}
		names = append(names, event.Command.Name)
		positions = append(positions, event.Command.Position)
	}

	if got, want := names, []string{"pwd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("call names = %#v, want %#v", got, want)
	}
	if got, want := positions, []string{"1:1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("echo positions = %#v, want %#v", got, want)
	}
}

func TestSyntaxErrorsIncludeSourceSnippet(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: strings.Join([]string{
			"if [[ a =~ c a ]]; then",
			"  echo ok",
			"fi",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.ExitCode, 2; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if got, want := result.Stderr, strings.Join([]string{
		"stdin: line 1: syntax error in conditional expression: unexpected token `a'",
		"stdin: line 1: syntax error near `a'",
		"stdin: line 1: `if [[ a =~ c a ]]; then'",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestSyntaxErrorsRejectTypedArgsLikeBash(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo (42)\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if got, want := result.ExitCode, 2; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if got, want := result.Stderr, strings.Join([]string{
		"stdin: line 1: syntax error near unexpected token `42'",
		"stdin: line 1: `echo (42)'",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestSyntaxErrorsUseRealUserLineNumbers(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ScriptPath: "syntax.sh",
		Script:     "echo one\n(\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "syntax.sh: line 2:") {
		t.Fatalf("Stderr = %q, want syntax.sh: line 2", result.Stderr)
	}
}
