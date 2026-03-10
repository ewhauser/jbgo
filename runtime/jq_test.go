package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestJQReadsFromStdinWithRawOutput(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `echo '{"name":"test"}' | jq -r '.name'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "test\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQCompactOutputAcrossMultipleFiles(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	setup := mustExecSession(t, session, "echo '{\"id\":1}' > /a.json\n echo '{\"id\":2}' > /b.json\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "jq -c '.' /a.json /b.json\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "{\"id\":1}\n{\"id\":2}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSlurpsMultipleValuesFromStdin(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo '1\n2\n3' | jq -s '.'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "[\n  1,\n  2,\n  3\n]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsNullInput(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "jq -n 'empty'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("want empty output, got stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestJQSupportsStdinMarkerWithFiles(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	setup := mustExecSession(t, session, "echo '{\"from\":\"file\"}' > /file.json\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, `echo '{"from":"stdin"}' | jq -r '.from' - /file.json`+"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "stdin\nfile\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsRawInput(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	setup := mustExecSession(t, session, "echo alpha > /in.txt\n echo beta >> /in.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "jq -R '.' /in.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\"alpha\"\n\"beta\"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsFilterFromFile(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	setup := mustExecSession(t, session, "echo '.name' > /filter.jq\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, `echo '{"name":"alice"}' | jq -r -f /filter.jq`+"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "alice\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsArgAndArgJSON(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `jq -n -c --arg name alice --argjson meta '{"team":"core"}' '{name: $name, team: $meta.team}'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "{\"name\":\"alice\",\"team\":\"core\"}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsSlurpfileAndRawfile(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/nums.json", []byte("1\n2\n3\n"))
	writeSessionFile(t, session, "/message.txt", []byte("hello\n"))

	result := mustExecSession(t, session, `jq -n -c --slurpfile nums /nums.json --rawfile msg /message.txt '{count: ($nums | length), msg: $msg}'`+"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "{\"count\":3,\"msg\":\"hello\\n\"}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsArgsAndJSONArgs(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `jq -n '$ARGS.positional[1]' --args one two` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\"two\"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	result, err = rt.Run(context.Background(), &ExecutionRequest{
		Script: `jq -n '$ARGS.positional[1].x' --jsonargs '1' '{"x":2}'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsRawOutputZeroDelimiter(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `echo '["a","b"]' | jq -r --raw-output0 '.[]'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\x00b\x00"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsIndentAndTabFormatting(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `echo '{"a":1}' | jq --indent 4 '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "{\n    \"a\": 1\n}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	result, err = rt.Run(context.Background(), &ExecutionRequest{
		Script: `echo '{"a":1}' | jq --tab '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "{\n\t\"a\": 1\n}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQExitStatusTracksFalsyOutput(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 'false' | jq -e '.'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "false\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQHandlesMissingFile(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "jq '.x' /missing.json\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if got := result.Stderr; !strings.Contains(got, "/missing.json") {
		t.Fatalf("Stderr = %q, want missing file error", got)
	}
}

func TestJQHandlesInvalidJSON(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 'not json' | jq '.'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 5 {
		t.Fatalf("ExitCode = %d, want 5; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stderr; !strings.Contains(got, "parse error") {
		t.Fatalf("Stderr = %q, want parse error", got)
	}
}

func TestJQHandlesInvalidQuery(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "jq 'if . then'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stderr; !strings.Contains(got, "invalid query") {
		t.Fatalf("Stderr = %q, want invalid query error", got)
	}
}
