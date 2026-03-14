package runtime

import (
	"context"
	"testing"
)

func TestRuntimeRunUsesFreshSessionEachTime(t *testing.T) {
	rt := newRuntime(t, &Config{})

	first, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo session-data > /shared.txt\n",
	})
	if err != nil {
		t.Fatalf("Run(first) error = %v", err)
	}
	if first.ExitCode != 0 {
		t.Fatalf("first ExitCode = %d, want 0; stderr=%q", first.ExitCode, first.Stderr)
	}

	second, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat /shared.txt\n",
	})
	if err != nil {
		t.Fatalf("Run(second) error = %v", err)
	}
	if second.ExitCode == 0 {
		t.Fatalf("second ExitCode = %d, want non-zero", second.ExitCode)
	}
}

func TestSessionWorkDirAppliesPerExecWithoutLeaking(t *testing.T) {
	session := newSession(t, &Config{})

	first, err := session.Exec(context.Background(), &ExecutionRequest{
		WorkDir: "/tmp",
		Script:  "pwd\n",
	})
	if err != nil {
		t.Fatalf("Exec(first) error = %v", err)
	}
	if got, want := first.Stdout, "/tmp\n"; got != want {
		t.Fatalf("first Stdout = %q, want %q", got, want)
	}

	second, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "pwd\n",
	})
	if err != nil {
		t.Fatalf("Exec(second) error = %v", err)
	}
	if got, want := second.Stdout, "/home/agent\n"; got != want {
		t.Fatalf("second Stdout = %q, want %q", got, want)
	}
}
