package runtime

import (
	"context"
	"testing"
)

func TestSessionPersistsFilesystemAcrossExecs(t *testing.T) {
	session := newSession(t, &Config{})

	writeResult, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo hi > /shared.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec(write) error = %v", err)
	}
	if writeResult.ExitCode != 0 {
		t.Fatalf("write ExitCode = %d, want 0", writeResult.ExitCode)
	}

	readResult, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "cat /shared.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec(read) error = %v", err)
	}
	if got, want := readResult.Stdout, "hi\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSessionDoesNotPersistShellVarsAcrossExecs(t *testing.T) {
	session := newSession(t, &Config{})

	if _, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "export FOO=bar\n",
	}); err != nil {
		t.Fatalf("Exec(export) error = %v", err)
	}

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "echo \"$FOO\"\n",
	})
	if err != nil {
		t.Fatalf("Exec(read) error = %v", err)
	}
	if got, want := result.Stdout, "\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSessionDoesNotPersistCDAcrossExecs(t *testing.T) {
	session := newSession(t, &Config{})

	first, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "cd /tmp\npwd\n",
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

func TestSessionsAreFilesystemIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	session1, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession(session1) error = %v", err)
	}
	session2, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession(session2) error = %v", err)
	}

	if _, err := session1.Exec(context.Background(), &ExecutionRequest{
		Script: "echo hi > /only-in-session-one.txt\n",
	}); err != nil {
		t.Fatalf("Exec(session1) error = %v", err)
	}

	result, err := session2.Exec(context.Background(), &ExecutionRequest{
		Script: "cat /only-in-session-one.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec(session2) error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
}

func TestExecReturnsFinalEnv(t *testing.T) {
	session := newSession(t, &Config{BaseEnv: map[string]string{"INITIAL": "value"}})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "export NEW_VAR=hello\nunset INITIAL\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.FinalEnv["NEW_VAR"], "hello"; got != want {
		t.Fatalf("FinalEnv[NEW_VAR] = %q, want %q", got, want)
	}
	if _, ok := result.FinalEnv["INITIAL"]; ok {
		t.Fatalf("FinalEnv should not contain INITIAL after unset: %#v", result.FinalEnv)
	}
}

func TestReplaceEnvDoesNotUseSessionBaseEnv(t *testing.T) {
	session := newSession(t, &Config{BaseEnv: map[string]string{"FOO": "base"}})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ReplaceEnv: true,
		Env: map[string]string{
			"PATH": defaultPath,
			"HOME": "",
		},
		Script: "echo \"${FOO:-missing}\"\n/bin/pwd\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "missing\n/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if _, ok := result.FinalEnv["FOO"]; ok {
		t.Fatalf("FinalEnv should not contain FOO when ReplaceEnv is true: %#v", result.FinalEnv)
	}
}
