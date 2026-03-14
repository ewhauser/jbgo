package yq

import (
	"context"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestYQReadsYAMLFromStdin(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf 'name: alice\\nteam: core\\n' | yq '.name'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "alice\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYQAutoDetectsJSONFiles(t *testing.T) {
	t.Parallel()

	session := newYQSession(t)
	writeSessionFile(t, session, "/input.json", []byte(`{"name":"alice","team":"core"}`+"\n"))

	result := mustExecSession(t, session, "yq '.name' /input.json\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\"alice\"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYQSupportsNullInputCreation(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `yq -n '.a.b = "cat"'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a:\n  b: cat\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYQSupportsExpressionFromFile(t *testing.T) {
	t.Parallel()

	session := newYQSession(t)
	writeSessionFile(t, session, "/filter.yq", []byte(".team\n"))

	result := mustExecSession(t, session, "printf 'name: alice\\nteam: core\\n' | yq --from-file /filter.yq\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "core\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYQSupportsInPlaceEdit(t *testing.T) {
	t.Parallel()

	session := newYQSession(t)
	writeSessionFile(t, session, "/doc.yml", []byte("name: alice\n"))

	result := mustExecSession(t, session, `yq -i '.name = "bob"' /doc.yml`+"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if got, want := string(readSessionFile(t, session, "/doc.yml")), "name: bob\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestYQSupportsEvalAllAcrossFiles(t *testing.T) {
	t.Parallel()

	session := newYQSession(t)
	writeSessionFile(t, session, "/a.yml", []byte("name: alice\n"))
	writeSessionFile(t, session, "/b.yml", []byte("team: core\n"))

	result := mustExecSession(t, session, `yq ea 'select(fileIndex == 0) * select(fileIndex == 1)' /a.yml /b.yml`+"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "name: alice\nteam: core\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYQSupportsOutputFormattingFlags(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf '{"a":1,"b":2}\n' | yq -p json -o json -I 0 '.'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "{\"a\":1,\"b\":2}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYQSupportsExplicitWrappedScalars(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf 'name: alice\\n' | yq -o json --unwrapScalar=false '.name'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\"alice\"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYQSupportsNulSeparatedOutput(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf '- a\\n- b\\n' | yq -0 '.[]'\n",
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

func TestYQExitStatusWhenNoMatchesFound(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf 'name: alice\\n' | yq -e '.missing'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "no matches found") {
		t.Fatalf("Stderr = %q, want no matches message", result.Stderr)
	}
}

func TestYQDisablesLoadOperators(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `printf 'name: alice\n' | yq 'load("/etc/passwd")'` + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "file operations have been disabled") {
		t.Fatalf("Stderr = %q, want file-ops denial", result.Stderr)
	}
}

func TestYQDisablesEnvOperators(t *testing.T) {
	t.Parallel()

	rt := newYQRuntime(t)
	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: `yq -n 'env(MY_VAR)'` + "\n",
		Env: map[string]string{
			"MY_VAR": "secret",
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "env operations have been disabled") {
		t.Fatalf("Stderr = %q, want env-ops denial", result.Stderr)
	}
}
