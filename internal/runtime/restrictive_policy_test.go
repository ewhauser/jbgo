package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/policy"
)

func restrictivePolicy(allowedCommands, allowedBuiltins []string) policy.Policy {
	return policy.NewStatic(&policy.Config{
		AllowedCommands: allowedCommands,
		AllowedBuiltins: allowedBuiltins,
		ReadRoots:       []string{defaultHomeDir, "/usr/bin", "/bin"},
		WriteRoots:      []string{defaultHomeDir},
		Limits: policy.Limits{
			MaxStdoutBytes: 1 << 20,
			MaxStderrBytes: 1 << 20,
			MaxFileBytes:   8 << 20,
		},
	})
}

func TestRestrictivePolicyDeniesEvalBuiltin(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"echo"}, []string{"cd"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "eval 'echo hi'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `builtin "eval" denied`) {
		t.Fatalf("Stderr = %q, want builtin eval denial", result.Stderr)
	}
}

func TestRestrictivePolicyDeniesSourceBuiltin(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: restrictivePolicy([]string{"echo"}, []string{"cd"}),
	})
	writeSessionFile(t, session, "/home/agent/lib.sh", []byte("echo sourced\n"))

	result := mustExecSession(t, session, "source /home/agent/lib.sh\n")
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `builtin "source" denied`) {
		t.Fatalf("Stderr = %q, want builtin source denial", result.Stderr)
	}
}

func TestRestrictivePolicyNestedBashRespectsCommandAllowlist(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"bash", "echo"}, []string{"cd"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo note > note.txt\nbash -c 'cat note.txt'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `command "cat" denied`) {
		t.Fatalf("Stderr = %q, want nested cat denial", result.Stderr)
	}
}

func TestRestrictivePolicyNestedShRespectsCommandAllowlist(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"sh", "echo"}, []string{"cd"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo note > note.txt\nsh -c 'cat note.txt'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `command "cat" denied`) {
		t.Fatalf("Stderr = %q, want nested cat denial", result.Stderr)
	}
}

func TestRestrictivePolicyCommandBuiltinCannotBypassCommandAllowlist(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"echo"}, []string{"command"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo note > note.txt\ncommand cat note.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `command "cat" denied`) {
		t.Fatalf("Stderr = %q, want command builtin denial", result.Stderr)
	}
}

func TestRestrictivePolicyBuiltinBuiltinCannotBypassBuiltinAllowlist(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"echo"}, []string{"builtin"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "builtin eval 'echo hi'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `builtin "eval" denied`) {
		t.Fatalf("Stderr = %q, want builtin eval denial", result.Stderr)
	}
}

func TestRestrictivePolicyCommandBuiltinCannotBypassBuiltinAllowlist(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"echo"}, []string{"command"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "command eval 'echo hi'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `builtin "eval" denied`) {
		t.Fatalf("Stderr = %q, want command-wrapped eval denial", result.Stderr)
	}
}

func TestRestrictivePolicyTimeoutRespectsCommandAllowlist(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"timeout", "echo"}, []string{"cd"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo note > note.txt\ntimeout 1 cat note.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `command "cat" denied`) {
		t.Fatalf("Stderr = %q, want timeout child denial", result.Stderr)
	}
}

func TestRestrictivePolicyXArgsRespectsCommandAllowlist(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: restrictivePolicy([]string{"xargs", "printf"}, []string{"cd"}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'note.txt\\n' | xargs cat\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 && result.ExitCode != 123 {
		t.Fatalf("ExitCode = %d, want 126 or 123; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `command "cat" denied`) {
		t.Fatalf("Stderr = %q, want xargs child denial", result.Stderr)
	}
}
