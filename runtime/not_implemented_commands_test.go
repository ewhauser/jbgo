package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestUUtilsCompatibilityPlaceholdersReturnNotImplemented(t *testing.T) {
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name   string
		script string
	}{
		{name: "arch", script: "arch\n"},
		{name: "install", script: "install /tmp/src /tmp/dst\n"},
		{name: "realpath", script: "realpath /tmp/example\n"},
		{name: "who", script: "who\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tt.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
			}
			if result.Stdout != "" {
				t.Fatalf("Stdout = %q, want empty output", result.Stdout)
			}
			if !strings.Contains(result.Stderr, tt.name+": not implemented") {
				t.Fatalf("Stderr = %q, want not-implemented error", result.Stderr)
			}
		})
	}
}
