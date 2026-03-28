package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/shellvariant"
)

func TestSessionPersistsFilesystemAcrossExecs(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestSessionExecShellVariantShDisablesBraceExpansion(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ShellVariant: shellvariant.SH,
		Script:       "printf '%s\\n' {a,b}\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "{a,b}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSessionInteractShellVariantShUsesPosixDefaults(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	var stdout strings.Builder
	result, err := session.Interact(context.Background(), &InteractiveRequest{
		ShellVariant: shellvariant.SH,
		Stdin:        strings.NewReader("printf '%s\\n' {a,b}\nexit\n"),
		Stdout:       &stdout,
	})
	if err != nil {
		t.Fatalf("Interact() error = %v", err)
	}
	if result == nil {
		t.Fatalf("Interact() result = nil")
	}
	if !strings.Contains(stdout.String(), "{a,b}\n") {
		t.Fatalf("Stdout = %q, want literal brace expansion output", stdout.String())
	}
}

func TestSessionExecShVariantUsesNonBashParseDiagnostics(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ShellVariant: shellvariant.SH,
		Script:       "foo=(bar)\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.ExitCode, 2; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if strings.Contains(result.Stderr, "line 1:") {
		t.Fatalf("Stderr = %q, want non-bash diagnostic format", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "posix") {
		t.Fatalf("Stderr = %q, want posix variant hint", result.Stderr)
	}
}

func TestSessionExecBatsVariantRejectsTestDecl(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ShellVariant: shellvariant.Bats,
		Script:       "@test \"demo\" { echo hi; }\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.ExitCode, 2; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if !strings.Contains(result.Stderr, "bats runner not implemented") {
		t.Fatalf("Stderr = %q, want bats runner error", result.Stderr)
	}
}

func TestSessionExecShebangShUsesPosixShellVariant(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: strings.Join([]string{
			"cat > /tmp/shebang-sh <<'EOF'",
			"#!/bin/sh",
			"printf '%s\\n' {a,b}",
			"EOF",
			"chmod +x /tmp/shebang-sh",
			"/tmp/shebang-sh",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "{a,b}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSessionExecTopLevelShebangInfersShellVariant(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "#!/bin/sh\nprintf '%s\\n' {a,b}\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "{a,b}\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSessionExecNestedShellCommandsSupportMkshAndZsh(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"mksh", "zsh"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})

			result, err := session.Exec(context.Background(), &ExecutionRequest{
				Script: name + " -c \"printf '%s\\\\n' {a,b}\"\n",
			})
			if err != nil {
				t.Fatalf("Exec() error = %v", err)
			}
			if got, want := result.Stdout, "a\nb\n"; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestSessionExecNestedBatsCommandReturnsRunnerError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "bats -c '@test \"demo\" { echo hi; }'\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.ExitCode, 2; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if !strings.Contains(result.Stderr, "bats runner not implemented") {
		t.Fatalf("Stderr = %q, want bats runner error", result.Stderr)
	}
	if strings.Contains(result.Stderr, "command not found") {
		t.Fatalf("Stderr = %q, want registered bats shell command", result.Stderr)
	}
}

func TestSessionExecBatsVariantDispatchesScriptsToBatsRunner(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ShellVariant: shellvariant.Bats,
		Script: strings.Join([]string{
			"cat > /tmp/plain-bats <<'EOF'",
			"@test \"demo\" {",
			"  echo hi",
			"}",
			"EOF",
			"chmod +x /tmp/plain-bats",
			"/tmp/plain-bats",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.ExitCode, 2; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if !strings.Contains(result.Stderr, "bats runner not implemented") {
		t.Fatalf("Stderr = %q, want bats runner error", result.Stderr)
	}
	if strings.Contains(result.Stderr, "@test: command not found") {
		t.Fatalf("Stderr = %q, want bats dispatch instead of bash fallback", result.Stderr)
	}
}

func TestSessionExecShebangBatsReturnsRunnerError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: strings.Join([]string{
			"cat > /tmp/shebang-bats <<'EOF'",
			"#!/bin/bats",
			"@test \"demo\" {",
			"  echo hi",
			"}",
			"EOF",
			"chmod +x /tmp/shebang-bats",
			"/tmp/shebang-bats",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.ExitCode, 126; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if !strings.Contains(result.Stderr, "bats runner not implemented") {
		t.Fatalf("Stderr = %q, want bats runner error", result.Stderr)
	}
}

func TestSessionDoesNotPersistCDAcrossExecs(t *testing.T) {
	t.Parallel()
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

func TestSessionClockAnchorKeepsMonotonicReading(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	if !strings.Contains(session.clockRealAt.String(), "m=+") {
		t.Fatalf("initial clockRealAt = %v, want monotonic reading", session.clockRealAt)
	}

	when := time.Date(2024, time.May, 6, 7, 8, 9, 0, time.UTC)
	if err := session.setTime(when); err != nil {
		t.Fatalf("setTime() error = %v", err)
	}
	if !strings.Contains(session.clockRealAt.String(), "m=+") {
		t.Fatalf("setTime clockRealAt = %v, want monotonic reading", session.clockRealAt)
	}
}

func TestExecPreservesInheritedStdoutFileMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir(sub) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "out"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(out) error = %v", err)
	}

	session := newSession(t, &Config{
		FileSystem: ReadWriteDirectoryFileSystem(root, ReadWriteDirectoryOptions{}),
	})

	stdoutFile, err := os.OpenFile(filepath.Join(root, "sub", "out"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("OpenFile(stdout) error = %v", err)
	}
	defer func() { _ = stdoutFile.Close() }()

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		WorkDir: "/sub",
		Script:  "cat out\n",
		Stdout:  stdoutFile,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "cat: out: input file is output file\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if got := string(readSessionFile(t, session, "/sub/out")); got != "x\n" {
		t.Fatalf("file contents = %q, want %q", got, "x\n")
	}
}

func TestSessionsAreFilesystemIsolated(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestExecPreservesExplicitSessionBootTimestamp(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Env: map[string]string{
			"GBASH_SESSION_BOOT_AT": "2001-02-03T04:05:06Z",
		},
		Script: "printf '%s\\n' \"$GBASH_SESSION_BOOT_AT\"\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "2001-02-03T04:05:06Z\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestReplaceEnvLetsShellInitializeShellOwnedStartupVars(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		ReplaceEnv: true,
		Env: map[string]string{
			"PWD": "/home/agent",
		},
		Script: "printf 'PATH=%s\\nSHELL=%s\\nHOME=%q\\n' \"$PATH\" \"$SHELL\" \"$HOME\"\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "PATH=/usr/bin:/bin\nSHELL=/bin/sh\nHOME=''\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSessionInteractPersistsStateAcrossEntries(t *testing.T) {
	t.Parallel()
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

func TestSessionInteractNounsetSkipsCurrentLineButContinues(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder

	result, err := session.Interact(context.Background(), &InteractiveRequest{
		Stdin: strings.NewReader("" +
			"set -u\n" +
			"echo before; echo $missing; echo after\n" +
			"echo line2\n" +
			"exit 7\n"),
		Stdout: &stdout,
		Stderr: &stderr,
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
	for _, want := range []string{"before\n", "line2\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "after\n") {
		t.Fatalf("stdout = %q, want current-line remainder to be skipped", stdout.String())
	}
	if got, want := stderr.String(), "missing: unbound variable\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestSessionInteractTracksHistoryCommand(t *testing.T) {
	t.Parallel()
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

func TestSessionInteractUsesPipelineSubshellSemantics(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder

	result, err := session.Interact(context.Background(), &InteractiveRequest{
		Stdin: strings.NewReader("" +
			"unset value\n" +
			"printf 'hello\\n' | read -r value\n" +
			"printf 'value:<%s>\\n' \"${value-}\"\n" +
			"exit\n"),
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
	if !strings.Contains(stdout.String(), "value:<>\n") {
		t.Fatalf("stdout = %q, want pipeline mutation to stay isolated", stdout.String())
	}
}

func TestSessionInteractSupportsLetAndKeepsRawHistory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder

	result, err := session.Interact(context.Background(), &InteractiveRequest{
		Stdin: strings.NewReader("" +
			"b=3\n" +
			"let b+=1\n" +
			"echo $b\n" +
			"history 3\n" +
			"exit\n"),
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
		"4\n",
		"    2  let b+=1\n",
		"    3  echo $b\n",
		"    4  history 3\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestSessionInteractSupportsProcessSubstitution(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	var stdout strings.Builder
	var stderr strings.Builder

	result, err := session.Interact(context.Background(), &InteractiveRequest{
		Stdin: strings.NewReader("" +
			"p=<(echo hello)\n" +
			"cat \"$p\"\n" +
			"while IFS= read -r line; do echo \"loop:$line\"; done < <(printf 'a\\nb\\n')\n" +
			"q=>(cat > /tmp/out)\n" +
			"printf 'hello-out\\n' > \"$q\"\n" +
			"while [ ! -s /tmp/out ]; do sleep 0.01; done\n" +
			"cat /tmp/out\n" +
			"exit\n"),
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
		"hello\n",
		"loop:a\n",
		"loop:b\n",
		"hello-out\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}
