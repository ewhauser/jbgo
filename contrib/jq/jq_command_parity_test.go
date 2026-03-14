package jq

import (
	"context"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestJQSupportsCompatibilityAliasesIsolated(t *testing.T) {
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
	if got, want := result.Stdout, "[1,{\"x\":\"Ω\"}]\n[1,{\"x\":\"Ω\"}]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
