package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunCLIPrintsVersion(t *testing.T) {
	prevVersion, prevCommit, prevDate, prevBuiltBy := version, commit, date, builtBy
	version, commit, date, builtBy = "v1.2.3", "abc123", "2026-03-10T20:00:00Z", "test"
	t.Cleanup(func() {
		version, commit, date, builtBy = prevVersion, prevCommit, prevDate, prevBuiltBy
	})

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--version"}, strings.NewReader("echo ignored"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	want := "gbash v1.2.3\ncommit: abc123\nbuilt: 2026-03-10T20:00:00Z\nbuilt-by: test\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunCLIHelpRendersBashInvocationFlags(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--help"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	help := stdout.String()
	for _, want := range []string{"-c command_string", "-s", "-o option", "-i", "--interactive", "--version"} {
		if !strings.Contains(help, want) {
			t.Fatalf("stdout = %q, want help to contain %q", help, want)
		}
	}
}

func TestRunCLICommandStringSupportsGroupedShortFlags(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-ceu", `echo "$MISSING"`}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "unbound variable") {
		t.Fatalf("stderr = %q, want nounset diagnostic", stderr.String())
	}
}

func TestRunCLICommandStringUsesBashArg0Semantics(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantOut  string
		wantCode int
	}{
		{
			name:     "default argv0 is invocation name",
			args:     []string{"-c", `printf '%s|%s\n' "$0" "$1"`},
			wantOut:  "gbash|\n",
			wantCode: 0,
		},
		{
			name:     "explicit argv0 shifts remaining args",
			args:     []string{"-c", `printf '%s|%s\n' "$0" "$1"`, "name", "value"},
			wantOut:  "name|value\n",
			wantCode: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout strings.Builder
			var stderr strings.Builder

			exitCode, err := runCLI(context.Background(), "gbash", tc.args, strings.NewReader(""), &stdout, &stderr, false)
			if err != nil {
				t.Fatalf("runCLI() error = %v", err)
			}
			if exitCode != tc.wantCode {
				t.Fatalf("exitCode = %d, want %d; stderr=%q", exitCode, tc.wantCode, stderr.String())
			}
			if got := stdout.String(); got != tc.wantOut {
				t.Fatalf("stdout = %q, want %q", got, tc.wantOut)
			}
			if got := stderr.String(); got != "" {
				t.Fatalf("stderr = %q, want empty", got)
			}
		})
	}
}

func TestRunCLISupportsScriptFileArgs(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "script.sh")
	if err := os.WriteFile(scriptPath, []byte("printf '%s|%s|%s\\n' \"$0\" \"$1\" \"$2\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{scriptPath, "left", "right"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), scriptPath+"|left|right\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIDashSReadsScriptFromStdinAndUsesArgs(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-s", "value"}, strings.NewReader("printf '%s|%s\\n' \"$0\" \"$1\"\n"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "gbash|value\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIImplicitStdinUsesInvocationNameAsArg0(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", nil, strings.NewReader("printf '%s\\n' \"$0\"\n"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "gbash\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLISupportsDashOPipefail(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-e", "-o", "pipefail", "-c", "false | true\necho after"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
}

func TestRunCLIStartupOptionsAffectExecution(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	tests := []struct {
		name      string
		args      []string
		wantOut   string
		stderrSub string
	}{
		{
			name:    "noexec",
			args:    []string{"-n", "-c", "echo should-not-run"},
			wantOut: "",
		},
		{
			name:    "allexport",
			args:    []string{"-a", "-c", "FOO=bar env | grep '^FOO=bar$'"},
			wantOut: "FOO=bar\n",
		},
		{
			name:    "noglob",
			args:    []string{"-f", "-c", "printf '%s\\n' /tmp/*"},
			wantOut: "/tmp/*\n",
		},
		{
			name:      "xtrace",
			args:      []string{"-x", "-c", "echo traced"},
			wantOut:   "traced\n",
			stderrSub: "+ echo traced",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout strings.Builder
			var stderr strings.Builder

			exitCode, err := runCLI(context.Background(), "gbash", tc.args, strings.NewReader(""), &stdout, &stderr, false)
			if err != nil {
				t.Fatalf("runCLI() error = %v", err)
			}
			if exitCode != 0 {
				t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != tc.wantOut {
				t.Fatalf("stdout = %q, want %q", got, tc.wantOut)
			}
			if tc.stderrSub != "" && !strings.Contains(stderr.String(), tc.stderrSub) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), tc.stderrSub)
			}
		})
	}
}

func TestRunCLIInteractiveCommandStringUsesInteractiveShellSemantics(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-ic", "alias hi='echo alias-ok'\nhi"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "alias-ok\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIInteractiveScriptUsesInteractiveShellSemantics(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "script.sh")
	if err := os.WriteFile(scriptPath, []byte("alias hi='echo alias-ok'\nhi\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-i", scriptPath}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "alias-ok\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIMulticallBashSupportsVersion(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), filepath.Join(tmp, "bash"), []string{"--version"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "bash") {
		t.Fatalf("stdout = %q, want bash version text", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIMulticallBashUsesHostBashForTrapCompatibility(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), filepath.Join(tmp, "bash"), []string{"-c", `trap "" PIPE; trap - PIPE; echo ok`}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "ok\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLICompatExecPassesStdin(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"compat", "exec", "cat"}, strings.NewReader("stdin-data"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "stdin-data" {
		t.Fatalf("stdout = %q, want %q", got, "stdin-data")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLICompatExecCatRejectsAppendToSelf(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	target := filepath.Join(tmp, "out")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdoutFile, err := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer func() { _ = stdoutFile.Close() }()

	var stderr strings.Builder
	exitCode, err := runCLI(context.Background(), "gbash", []string{"compat", "exec", "cat", "out"}, strings.NewReader(""), stdoutFile, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if got, want := stderr.String(), "cat: out: input file is output file\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(data), "x\n"; got != want {
		t.Fatalf("file contents = %q, want %q", got, want)
	}
}

func TestRunCLICompatExecUnknownCommandReturns127(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"compat", "exec", "missing-command"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 127 {
		t.Fatalf("exitCode = %d, want 127", exitCode)
	}
	if !strings.Contains(stderr.String(), "missing-command: command not found") {
		t.Fatalf("stderr = %q, want command-not-found message", stderr.String())
	}
}

func TestRunCLICompatExecYesReportsSingleWriteErrorOnDevFull(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	info, err := os.Stat("/dev/full")
	if err != nil || info.Mode()&os.ModeDevice == 0 {
		t.Skip("/dev/full unavailable")
	}

	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("OpenFile(/dev/full) error = %v", err)
	}
	defer func() { _ = full.Close() }()

	var stderr strings.Builder
	exitCode, err := runCLI(context.Background(), "gbash", []string{"compat", "exec", "yes", "x"}, strings.NewReader(""), full, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got, want := strings.Count(stderr.String(), "yes: standard output"), 1; got != want {
		t.Fatalf("stderr = %q, want %d write diagnostic", stderr.String(), want)
	}
}

func TestRunCLICompatExecEnvSupportsDoubleDashCommandSeparator(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"compat", "exec", "env", "--", "pwd", "-P"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	physicalTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", tmp, err)
	}
	if got, want := stdout.String(), filepath.ToSlash(physicalTmp)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIMulticallEnvSupportsAssignmentsAfterDoubleDash(t *testing.T) {
	tmp := t.TempDir()

	commandDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	envPath := filepath.Join(commandDir, "env")
	if err := os.WriteFile(envPath, []byte("# compat shim\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", envPath, err)
	}
	pwdPath := filepath.Join(commandDir, "pwd")
	if err := os.WriteFile(pwdPath, []byte("# compat shim\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", pwdPath, err)
	}

	physicalDir := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(physicalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", physicalDir, err)
	}
	logicalDir := filepath.Join(tmp, "c")
	if err := os.Symlink(physicalDir, logicalDir); err != nil {
		t.Skipf("Symlink() error = %v", err)
	}

	t.Chdir(logicalDir)
	t.Setenv("PWD", filepath.ToSlash(logicalDir))

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode, err := runCLI(context.Background(), envPath, []string{"--", "POSIXLY_CORRECT=1", "pwd"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), filepath.ToSlash(logicalDir)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLICompatExecStreamsOutputBeforeExit(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	var stderr strings.Builder
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{"compat", "exec", "seq", "999999", "inf"}, strings.NewReader(""), stdout, &stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stdout.WaitForSubstring("999999\n1000000\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not stream expected prefix before compat exec exited; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowMissingFileByName(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "-F", "-s0.05", "--max-unchanged-stats=1", "missing/file",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stderr.WaitForSubstring("cannot open", 500*time.Millisecond) {
		t.Fatalf("stderr did not report missing file; got %q", stderr.String())
	}
	if err := os.MkdirAll(filepath.Join(tmp, "missing"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "missing", "file"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if !stderr.WaitForSubstring("has appeared", 500*time.Millisecond) {
		t.Fatalf("stderr did not report file appearance; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("x\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit followed content; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowMissingFlatFileByName(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "--follow=name", "--retry", "-s0.05", "--max-unchanged-stats=1", "missing",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stderr.WaitForSubstring("cannot open 'missing'", 500*time.Millisecond) {
		t.Fatalf("stderr did not report missing file; got %q", stderr.String())
	}
	if err := os.WriteFile(filepath.Join(tmp, "missing"), []byte("X\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(missing) error = %v", err)
	}
	if !stderr.WaitForSubstring("has appeared", 500*time.Millisecond) {
		t.Fatalf("stderr did not report file appearance; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("X\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit followed content; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowUntailableByNameUntilFileAppears(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.Mkdir(filepath.Join(tmp, "untailable"), 0o755); err != nil {
		t.Fatalf("Mkdir(untailable) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "-F", "-s0.05", "--max-unchanged-stats=1", "untailable",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stderr.WaitForSubstring("error reading 'untailable': Is a directory", 500*time.Millisecond) {
		t.Fatalf("stderr did not report untailable directory read error; got %q", stderr.String())
	}
	if !stderr.WaitForSubstring("untailable: cannot follow end of this type of file", 500*time.Millisecond) {
		t.Fatalf("stderr did not report untailable file; got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "has become accessible") || strings.Contains(stderr.String(), "has appeared") {
		t.Fatalf("stderr reported file accessibility before replacement; got %q", stderr.String())
	}

	if err := os.Remove(filepath.Join(tmp, "untailable")); err != nil {
		t.Fatalf("Remove(untailable) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "untailable"), []byte("foo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(untailable) error = %v", err)
	}
	if !stderr.WaitForSubstring("has become accessible", 500*time.Millisecond) {
		t.Fatalf("stderr did not report file accessibility after replacement; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("foo\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit followed content; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowDescriptorSurvivesRename(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "a"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(a) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "-f", "-s0.05", "a",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if err := os.WriteFile(filepath.Join(tmp, "a"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(a) error = %v", err)
	}
	if !stdout.WaitForSubstring("x\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit initial content; got %q", stdout.String())
	}
	if err := os.Rename(filepath.Join(tmp, "a"), filepath.Join(tmp, "b")); err != nil {
		t.Fatalf("Rename(a,b) error = %v", err)
	}
	file, err := os.OpenFile(filepath.Join(tmp, "b"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(b) error = %v", err)
	}
	if _, err := file.WriteString("y\n"); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString(b) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(b) error = %v", err)
	}
	if !stdout.WaitForSubstring("x\ny\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not continue following renamed file; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowByNameHandlesRenameAndReplacement(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(tmp, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "-F", "-s0.05", "--max-unchanged-stats=1", "a", "b",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if err := os.WriteFile(filepath.Join(tmp, "a"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(a) error = %v", err)
	}
	if !stdout.WaitForSubstring("==> a <==\nx\n", 1500*time.Millisecond) {
		t.Fatalf("stdout did not emit followed content for a; got %q", stdout.String())
	}
	if err := os.WriteFile(filepath.Join(tmp, "b"), []byte("b0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(b) error = %v", err)
	}
	if !stdout.WaitForSubstring("==> b <==\nb0\n", 1500*time.Millisecond) {
		t.Fatalf("stdout did not emit followed content for b; got %q", stdout.String())
	}

	if err := os.Rename(filepath.Join(tmp, "a"), filepath.Join(tmp, "b")); err != nil {
		t.Fatalf("Rename(a,b) error = %v", err)
	}
	if !stderr.WaitForSubstring("'a' has become inaccessible", 1500*time.Millisecond) {
		t.Fatalf("stderr did not report inaccessible file; got %q", stderr.String())
	}
	if !stderr.WaitForSubstring("'b' has been replaced", 1500*time.Millisecond) {
		t.Fatalf("stderr did not report replaced file; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("==> b <==\nb0\nx\n", 1500*time.Millisecond) {
		t.Fatalf("stdout did not emit replacement content for b; got %q", stdout.String())
	}

	if err := os.WriteFile(filepath.Join(tmp, "a"), []byte("x2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(a) second generation error = %v", err)
	}
	if !stderr.WaitForSubstring("'a' has appeared", 1500*time.Millisecond) {
		t.Fatalf("stderr did not report file appearance; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("==> a <==\nx2\n", 1500*time.Millisecond) {
		t.Fatalf("stdout did not emit replacement content for a; got %q", stdout.String())
	}

	bFile, err := os.OpenFile(filepath.Join(tmp, "b"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(b) error = %v", err)
	}
	if _, err := bFile.WriteString("y\n"); err != nil {
		_ = bFile.Close()
		t.Fatalf("WriteString(b) error = %v", err)
	}
	if err := bFile.Close(); err != nil {
		t.Fatalf("Close(b) error = %v", err)
	}
	if !stdout.WaitForSubstring("==> b <==\ny\n", 1500*time.Millisecond) {
		t.Fatalf("stdout did not continue following renamed b; got %q", stdout.String())
	}

	aFile, err := os.OpenFile(filepath.Join(tmp, "a"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(a) error = %v", err)
	}
	if _, err := aFile.WriteString("z\n"); err != nil {
		_ = aFile.Close()
		t.Fatalf("WriteString(a) error = %v", err)
	}
	if err := aFile.Close(); err != nil {
		t.Fatalf("Close(a) error = %v", err)
	}
	if !stdout.WaitForSubstring("==> a <==\nz\n", 1500*time.Millisecond) {
		t.Fatalf("stdout did not continue following recreated a; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailGroupedQuietFollowFlags(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	for _, name := range []string{"1", "2"} {
		if err := os.WriteFile(filepath.Join(tmp, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "-qF", "-s0.05", "--max-unchanged-stats=1", "1", "2",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if err := os.WriteFile(filepath.Join(tmp, "2"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(2) error = %v", err)
	}
	if !stdout.WaitForSubstring("x\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit followed content; got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "==>") {
		t.Fatalf("stdout = %q, did not expect headers with -qF", stdout.String())
	}
	if strings.Contains(stderr.String(), "unsupported flag -qF") {
		t.Fatalf("stderr = %q, grouped short flags were not parsed", stderr.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowByNameWithoutRetryFailsWhenMissingInitially(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{
		"compat", "exec", "tail", "--follow=name", "no-such",
	}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "cannot open 'no-such'") {
		t.Fatalf("stderr = %q, want missing-file diagnostic", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no files remaining") {
		t.Fatalf("stderr = %q, want no-files-remaining diagnostic", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowByNameWithoutRetryStopsWhenFileDisappears(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "file"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(file) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "--follow=name", "-s0.05", "--max-unchanged-stats=1", "file",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stdout.WaitForSubstring("seed\n", time.Second) {
		t.Fatalf("stdout did not emit initial content; got %q", stdout.String())
	}
	if err := os.Rename(filepath.Join(tmp, "file"), filepath.Join(tmp, "file.unfollow")); err != nil {
		t.Fatalf("Rename(file, file.unfollow) error = %v", err)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("runCLI() error = %v", result.err)
		}
		if result.exitCode != 1 {
			t.Fatalf("exitCode = %d, want 1; stderr=%q", result.exitCode, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatalf("tail --follow=name did not exit after the file disappeared; stderr=%q", stderr.String())
	}

	if !strings.Contains(stderr.String(), "'file' has become inaccessible") {
		t.Fatalf("stderr = %q, want inaccessible diagnostic", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no files remaining") {
		t.Fatalf("stderr = %q, want no-files-remaining diagnostic", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowByNameWithoutRetryTracksReappearingFileWhileOthersRemain(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	for _, name := range []string{"a", "foo"} {
		if err := os.WriteFile(filepath.Join(tmp, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "--follow=name", "-s0.05", "--max-unchanged-stats=1", "a", "foo",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if err := os.WriteFile(filepath.Join(tmp, "a"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(a) error = %v", err)
	}
	if !stdout.WaitForSubstring("==> a <==\nx\n", time.Second) {
		t.Fatalf("stdout did not emit initial content; got %q", stdout.String())
	}
	if err := os.WriteFile(filepath.Join(tmp, "foo"), []byte("foo0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(foo) error = %v", err)
	}
	if !stdout.WaitForSubstring("==> foo <==\nfoo0\n", time.Second) {
		t.Fatalf("stdout did not emit initial content for foo; got %q", stdout.String())
	}
	if err := os.Remove(filepath.Join(tmp, "foo")); err != nil {
		t.Fatalf("Remove(foo) error = %v", err)
	}
	if !stderr.WaitForSubstring("'foo' has become inaccessible", time.Second) {
		t.Fatalf("stderr did not report inaccessible file; got %q", stderr.String())
	}
	if err := os.WriteFile(filepath.Join(tmp, "foo"), []byte("ok ok ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(foo) error = %v", err)
	}
	if !stderr.WaitForSubstring("'foo' has appeared", time.Second) {
		t.Fatalf("stderr did not report reappearing file; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("==> foo <==\nfoo0\nok ok ok\n", time.Second) {
		t.Fatalf("stdout did not resume the reappearing file; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowPidKeepsRunningWhileLastPidIsAlive(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "here"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(here) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(ctx, "gbash", []string{
		"compat", "exec", "tail", "-f", "-s0.05", "--pid=2147483647", "--pid=" + strconv.Itoa(os.Getpid()), "here",
	}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowPidExitsBeforeLongSleepWhenPidIsDead(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "empty"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(empty) error = %v", err)
	}

	start := time.Now()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{
		"compat", "exec", "tail", "-f", "-s10", "--pid=2147483647", "empty",
	}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("tail waited too long for a dead pid: %v", elapsed)
	}
}

func TestRunCLICompatExecTailRetryWarnsWithoutFollow(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "file"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(file) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{
		"compat", "exec", "tail", "--retry", "file",
	}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "--retry ignored") {
		t.Fatalf("stderr = %q, want retry warning", stderr.String())
	}
}

func TestRunCLICompatExecTailRetryDescriptorReportsAppearanceAndTruncation(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "--follow=descriptor", "--retry", "-s0.05", "missing",
		}, strings.NewReader(""), stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stderr.WaitForSubstring("--retry only effective for the initial open", 500*time.Millisecond) {
		t.Fatalf("stderr did not report descriptor retry warning; got %q", stderr.String())
	}
	if !stderr.WaitForSubstring("cannot open 'missing'", 500*time.Millisecond) {
		t.Fatalf("stderr did not report missing file; got %q", stderr.String())
	}
	if err := os.WriteFile(filepath.Join(tmp, "missing"), []byte("X1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(missing) error = %v", err)
	}
	if !stderr.WaitForSubstring("has appeared", 500*time.Millisecond) {
		t.Fatalf("stderr did not report appearing file; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("X1\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit initial followed content; got %q", stdout.String())
	}
	if err := os.WriteFile(filepath.Join(tmp, "missing"), []byte("X\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(missing truncate) error = %v", err)
	}
	if !stderr.WaitForSubstring("file truncated", 500*time.Millisecond) {
		t.Fatalf("stderr did not report truncation; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("X1\nX\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit truncated content; got %q", stdout.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLICompatExecTailRetryDescriptorGivesUpOnUntailableReplacement(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(context.Background(), "gbash", []string{
			"compat", "exec", "tail", "--follow=descriptor", "--retry", "-s0.05", "missing",
		}, strings.NewReader(""), &stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stderr.WaitForSubstring("cannot open 'missing'", 500*time.Millisecond) {
		t.Fatalf("stderr did not report missing file; got %q", stderr.String())
	}
	if err := os.Mkdir(filepath.Join(tmp, "missing"), 0o755); err != nil {
		t.Fatalf("Mkdir(missing) error = %v", err)
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", result.exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "untailable file") {
		t.Fatalf("stderr = %q, want untailable-file diagnostic", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no files remaining") {
		t.Fatalf("stderr = %q, want no-files-remaining diagnostic", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowDashReadsStandardInput(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{
		"compat", "exec", "tail", "-f", "-",
	}, strings.NewReader("line\n"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "line\n" {
		t.Fatalf("stdout = %q, want %q", got, "line\n")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLICompatExecTailFollowDashReportsClosedStdin(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	stdinPath := filepath.Join(tmp, "closed-stdin")
	if err := os.WriteFile(stdinPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(closed-stdin) error = %v", err)
	}
	stdin, err := os.Open(stdinPath)
	if err != nil {
		t.Fatalf("Open(closed-stdin) error = %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("Close(closed-stdin) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{
		"compat", "exec", "tail", "-f", "-",
	}, stdin, &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "cannot fstat 'standard input'") {
		t.Fatalf("stderr = %q, want cannot-fstat diagnostic", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no files remaining") {
		t.Fatalf("stderr = %q, want no-files-remaining diagnostic", stderr.String())
	}
}

func TestRunCLICompatExecTailFollowNameRejectsDash(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{
		"compat", "exec", "tail", "--follow=name", "-",
	}, strings.NewReader("line\n"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "cannot follow '-' by name") {
		t.Fatalf("stderr = %q, want follow-name stdin rejection", stderr.String())
	}
}

func TestRunCLICompatExecTailDebugReportsPollingMode(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "a"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(a) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	var stdout strings.Builder
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "--debug", "-n0", "-F", "-s0.05", "a",
		}, strings.NewReader(""), &stdout, stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stderr.WaitForSubstring("using polling mode", 200*time.Millisecond) {
		t.Fatalf("stderr did not report polling mode; got %q", stderr.String())
	}

	result := <-done
	if result.err != nil {
		t.Fatalf("runCLI() error = %v", result.err)
	}
	if result.exitCode != 124 {
		t.Fatalf("exitCode = %d, want 124; stderr=%q", result.exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution timed out") {
		t.Fatalf("stderr = %q, want timeout marker", stderr.String())
	}
}

func TestRunCLIMulticallUsesArgv0CommandAndBypassesTTYRepl(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	commandDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	commandPath := filepath.Join(commandDir, "pwd")
	if err := os.WriteFile(commandPath, []byte("# compat shim\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", commandPath, err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), commandPath, nil, strings.NewReader("ignored"), &stdout, &stderr, true)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	physicalTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", tmp, err)
	}
	want := filepath.ToSlash(physicalTmp) + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if strings.Contains(stdout.String(), "~$") {
		t.Fatalf("stdout = %q, did not expect interactive prompt", stdout.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIMulticallBareArgv0ResolvesCommandDirFromPATH(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	commandDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, name := range []string{"sh", "echo", "printf"} {
		commandPath := filepath.Join(commandDir, name)
		if err := os.WriteFile(commandPath, []byte("# compat shim\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", commandPath, err)
		}
	}
	t.Setenv("PATH", commandDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "sh", []string{"-c", "printf 'ok\\n'; echo $#", "ignored", "1", "2", "3"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "ok\n3\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIMulticallPwdHonorsLogicalAndPhysicalModes(t *testing.T) {
	tmp := t.TempDir()

	commandDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	commandPath := filepath.Join(commandDir, "pwd")
	if err := os.WriteFile(commandPath, []byte("# compat shim\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", commandPath, err)
	}

	physicalDir := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(physicalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", physicalDir, err)
	}
	logicalDir := filepath.Join(tmp, "c")
	if err := os.Symlink(physicalDir, logicalDir); err != nil {
		t.Skipf("Symlink() error = %v", err)
	}

	t.Chdir(logicalDir)
	t.Setenv("PWD", filepath.ToSlash(logicalDir))
	physicalLogicalDir, err := filepath.EvalSymlinks(logicalDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", logicalDir, err)
	}
	physicalLogicalDir = filepath.ToSlash(physicalLogicalDir)

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), commandPath, []string{"-L"}, strings.NewReader("ignored"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI(-L) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), filepath.ToSlash(logicalDir)+"\n"; got != want {
		t.Fatalf("stdout(-L) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(-L) = %q, want empty", got)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode, err = runCLI(context.Background(), commandPath, []string{"--physical"}, strings.NewReader("ignored"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI(--physical) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), physicalLogicalDir+"\n"; got != want {
		t.Fatalf("stdout(--physical) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(--physical) = %q, want empty", got)
	}

	t.Setenv("POSIXLY_CORRECT", "1")
	stdout.Reset()
	stderr.Reset()
	exitCode, err = runCLI(context.Background(), commandPath, nil, strings.NewReader("ignored"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI(POSIXLY_CORRECT) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), filepath.ToSlash(logicalDir)+"\n"; got != want {
		t.Fatalf("stdout(POSIXLY_CORRECT) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(POSIXLY_CORRECT) = %q, want empty", got)
	}
}

type streamingWriter struct {
	mu  sync.Mutex
	buf strings.Builder
	sig chan struct{}
}

func newStreamingWriter() *streamingWriter {
	return &streamingWriter{sig: make(chan struct{}, 1)}
}

func (w *streamingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if err == nil {
		select {
		case w.sig <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (w *streamingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *streamingWriter) WaitForSubstring(substr string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if strings.Contains(w.String(), substr) {
			return true
		}
		select {
		case <-w.sig:
		case <-deadline.C:
			return strings.Contains(w.String(), substr)
		}
	}
}

var _ io.Writer = (*streamingWriter)(nil)
