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

func TestExecInlineFunctionsTrackCallLinesWithoutBashSource(t *testing.T) {
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
		"SRC:",
		"LINE:6",
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

func TestSyntaxErrorsUseRealUserLineNumbers(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	_, err := session.Exec(context.Background(), &ExecutionRequest{
		ScriptPath: "syntax.sh",
		Script:     "echo one\n(\n",
	})
	if err == nil {
		t.Fatal("Exec() error = nil, want parse failure")
	}
	if !strings.Contains(err.Error(), "syntax.sh:2:1") {
		t.Fatalf("error = %q, want syntax.sh:2:1", err.Error())
	}
}
