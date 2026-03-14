package runtime

import (
	"context"
	"strings"
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

func TestSessionInteractPersistsStateAcrossEntries(t *testing.T) {
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder

	result, err := session.Interact(context.Background(), &InteractiveRequest{
		StartupOptions: []string{"nounset"},
		Stdin:          strings.NewReader("set +o nounset\ncd /tmp\npwd\necho X${MISSING}Y\nexit 7\n"),
		Stdout:         &stdout,
		Stderr:         &stderr,
	})
	if err != nil {
		t.Fatalf("Interact() error = %v", err)
	}
	if result == nil {
		t.Fatalf("Interact() result = nil")
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7; stdout=%q stderr=%q", result.ExitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	for _, want := range []string{"/tmp\n", "XY\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestSessionInteractTracksHistoryCommand(t *testing.T) {
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder

	result, err := session.Interact(context.Background(), &InteractiveRequest{
		Stdin:  strings.NewReader("pwd\nhistory\nhistory 1\nhistory -c\nhistory\nexit\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Interact() error = %v", err)
	}
	if result == nil {
		t.Fatalf("Interact() result = nil")
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	for _, want := range []string{
		"/home/agent\n",
		"    1  pwd\n",
		"    2  history\n",
		"    3  history 1\n",
		"    1  history\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}
