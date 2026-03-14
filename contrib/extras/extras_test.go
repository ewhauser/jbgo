package extras

import (
	"context"
	"slices"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestRegisterNilRegistry(t *testing.T) {
	if err := Register(nil); err != nil {
		t.Fatalf("Register(nil) error = %v", err)
	}
}

func TestRegisterAddsContribCommands(t *testing.T) {
	registry := FullRegistry()

	for _, name := range []string{"awk", "jq", "sqlite3", "yq"} {
		if !slices.Contains(registry.Names(), name) {
			t.Fatalf("Names() missing %q: %v", name, registry.Names())
		}
	}
}

func TestRegisterSupportsBundledCommands(t *testing.T) {
	rt, err := gbruntime.New(gbruntime.WithRegistry(FullRegistry()))
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf 'a,b\\n' | awk -F, '{print $2}'\n" +
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
	if got, want := result.Stdout, "b\nalice\nalice\n1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
