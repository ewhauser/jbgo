package extras

import (
	"context"
	"slices"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestRegisterNilRegistry(t *testing.T) {
	t.Parallel()
	if err := Register(nil); err != nil {
		t.Fatalf("Register(nil) error = %v", err)
	}
}

func TestRegisterAddsContribCommands(t *testing.T) {
	t.Parallel()
	registry := FullRegistry()

	for _, name := range []string{"awk", "html-to-markdown", "jq", "python", "python3", "sqlite3", "yq"} {
		if !slices.Contains(registry.Names(), name) {
			t.Fatalf("Names() missing %q: %v", name, registry.Names())
		}
	}
	if slices.Contains(registry.Names(), "nodejs") {
		t.Fatalf("Names() unexpectedly contains %q: %v", "nodejs", registry.Names())
	}
}

func TestRegisterSupportsBundledCommands(t *testing.T) {
	t.Parallel()
	rt, err := gbruntime.New(gbruntime.WithRegistry(FullRegistry()))
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf 'a,b\\n' | awk -F, '{print $2}'\n" +
			"printf '<h1>docs</h1>' | html-to-markdown\n" +
			"printf '{\"name\":\"alice\"}\\n' | jq -r '.name'\n" +
			"printf 'name: alice\\n' | yq '.name'\n" +
			`sqlite3 :memory: "select 1;"`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stdout, "b\n# docs\nalice\nalice\n1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRegisterSupportsBundledPythonWhenAvailable(t *testing.T) {
	t.Parallel()

	rt, err := gbruntime.New(gbruntime.WithRegistry(FullRegistry()))
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python -c 'print(\"py\")'\npython3 -c 'print(\"py3\")'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(result.Stderr, "monty native bindings are unavailable") {
		t.Skip("gomonty unavailable on this build")
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stdout, "py\npy3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
