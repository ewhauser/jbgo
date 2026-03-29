package jq

import (
	"context"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestJQReadsFromStdinWithRawOutput(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	session := newJQSession(t)
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

func TestJQNullInputDoesNotConsumeShellLoopInput(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `while IFS= read -r line; do
  x=$(jq -nc --arg c "$line" '{role: $c}')
  y=$(printf '[]' | jq -c --argjson m "$x" '. + [$m]')
  printf 'line=%s y=%s\n' "$line" "$y"
done < <(printf '%s\n' alpha beta)` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "line=alpha y=[{\"role\":\"alpha\"}]\nline=beta y=[{\"role\":\"beta\"}]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQNullInputStillSupportsDeferredInputBuiltin(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf '%s\n' 1 2 | jq -nc '[input, input]'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "[1,2]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQNullInputIgnoresUnusedMissingFiles(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `jq -n '.' missing.json` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "null\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestJQNullInputTryCatchKeepsMissingFilesFatal(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `jq -n 'try input catch "ok"' missing.json` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "\"ok\"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "jq: missing.json: No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-file diagnostic", result.Stderr)
	}
}

func TestJQSupportsStdinMarkerWithFiles(t *testing.T) {
	t.Parallel()

	session := newJQSession(t)
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

	session := newJQSession(t)
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

	session := newJQSession(t)
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	session := newJQSession(t)
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

	rt := newJQRuntime(t)

	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	result, err = rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	result, err = rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

func TestJQSupportsVersionAliases(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "jq -V\njq -v\njq --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, jqVersionText+jqVersionText+jqVersionText; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsASCIIOutput(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf '%s\n' '{"Ω":"λ"}' | jq --ascii-output -c '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "{\"\\u03a9\":\"\\u03bb\"}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsInputsAndInputFilename(t *testing.T) {
	t.Parallel()

	session := newJQSession(t)
	result := mustExecSession(t, session, strings.Join([]string{
		"printf '%s\\n' '1' '2' | jq -n -c '[inputs]'",
		"printf '%s\\n' '1' > a.json",
		"jq 'input_filename' a.json",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "[1,2]\n\"a.json\"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsColorAndMonochromeOutput(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)

	colorResult, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf '%s\n' '{"x":1}' | jq -C -c '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if colorResult.ExitCode != 0 {
		t.Fatalf("color ExitCode = %d, want 0; stderr=%q", colorResult.ExitCode, colorResult.Stderr)
	}
	if !strings.Contains(colorResult.Stdout, "\x1b[") {
		t.Fatalf("Stdout = %q, want ANSI color output", colorResult.Stdout)
	}

	monoResult, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf '%s\n' '{"x":1}' | jq -CM -c '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if monoResult.ExitCode != 0 {
		t.Fatalf("mono ExitCode = %d, want 0; stderr=%q", monoResult.ExitCode, monoResult.Stderr)
	}
	if strings.Contains(monoResult.Stdout, "\x1b[") {
		t.Fatalf("Stdout = %q, want monochrome output", monoResult.Stdout)
	}
	if got, want := monoResult.Stdout, "{\"x\":1}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsUnbufferedOutput(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "jq --unbuffered -n '1'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsStreamMode(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf '%s\n' '[1,[2]]' | jq --stream -c '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "[[0],1]\n[[1,0],2]\n[[1,0]]\n[[1]]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsStreamErrorsMode(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: strings.Join([]string{
			`printf '%s' '{' | jq --stream-errors -c '.'`,
			`printf '%s' '{"a":[1,' | jq --stream-errors -c '.'`,
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
	if got, want := result.Stdout, "[\"Unfinished JSON term at EOF at line 1, column 1\",[null]]\n[[\"a\",0],1]\n[\"Unfinished JSON term at EOF at line 1, column 8\",[\"a\",1]]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsSeqMode(t *testing.T) {
	t.Parallel()

	session := newJQSession(t)
	result := mustExecSession(t, session, strings.Join([]string{
		`printf '\0361\n' | jq --seq '.' | od -An -t x1 | tr -d ' \n'`,
		"printf '\\n'",
		`jq -n --seq '1,2' | od -An -t x1 | tr -d ' \n'`,
		"printf '\\n'",
		`jq -n --seq -r '"a","b"' | od -An -t x1 | tr -d ' \n'`,
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1e310a\n1e310a1e320a\n610a620a"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQWarnsOnNonSequenceInput(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf '%s\n' '1' | jq --seq '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty stdout", result.Stdout)
	}
	if got, want := result.Stderr, "jq: ignoring parse error: Unfinished abandoned text at EOF at line 2, column 0\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestJQSupportsModuleLoading(t *testing.T) {
	t.Parallel()

	session := newJQSession(t)
	writeSessionFile(t, session, "/lib/mod.jq", []byte("def f: 42;\n"))
	writeSessionFile(t, session, "/lib/data.json", []byte("{\"v\":7}\n"))

	result := mustExecSession(t, session, strings.Join([]string{
		`jq -L /lib -n 'include "mod"; f'`,
		`jq -L /lib -n 'import "data" as $d; $d[0].v'`,
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "42\n7\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsBuildConfiguration(t *testing.T) {
	t.Parallel()

	rt := newJQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "jq --build-configuration\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, jqBuildConfigurationText; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestJQSupportsCompatibilityAliasesIsolated(t *testing.T) {
	t.Parallel()
	rt := newJQRuntime(t)

	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf '[1,{\"x\":\"Ω\"}]\\n' > /tmp/in.json\n" +
			"jq --compact --ascii --color --monochrome '.' /tmp/in.json\n" +
			"jq -aCMc '.' /tmp/in.json\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "[1,{\"x\":\"\\u03a9\"}]\n[1,{\"x\":\"\\u03a9\"}]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
