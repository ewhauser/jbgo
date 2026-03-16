package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/cli"
)

type cliJSONTiming struct {
	StartedAt  string  `json:"startedAt"`
	FinishedAt string  `json:"finishedAt"`
	DurationMs float64 `json:"durationMs"`
}

type cliJSONTrace struct {
	Schema      string `json:"schema"`
	SessionID   string `json:"sessionId"`
	ExecutionID string `json:"executionId"`
	EventCount  int    `json:"eventCount"`
}

type cliJSONResult struct {
	Stdout          string         `json:"stdout"`
	Stderr          string         `json:"stderr"`
	ExitCode        int            `json:"exitCode"`
	StdoutTruncated bool           `json:"stdoutTruncated"`
	StderrTruncated bool           `json:"stderrTruncated"`
	Timing          *cliJSONTiming `json:"timing"`
	Trace           *cliJSONTrace  `json:"trace"`
}

func TestRunCLIPrintsVersion(t *testing.T) {
	t.Parallel()

	cfg := cli.Config{
		Name: "gbash",
		Build: &cli.BuildInfo{
			Version: "v1.2.3",
			Commit:  "abc123",
			Date:    "2026-03-10T20:00:00Z",
			BuiltBy: "test",
		},
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIWithConfig(context.Background(), cfg, "gbash", []string{"--version"}, strings.NewReader("echo ignored"), &stdout, &stderr, false)
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
	t.Parallel()
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

func TestRunCLIHelpRendersFilesystemFlags(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--help"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	for _, want := range []string{"CLI filesystem options:", "--root DIR", "--cwd DIR", "--readwrite-root DIR", "CLI output options:", "--json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want help to contain %q", stdout.String(), want)
		}
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIHelpRendersServerFlags(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--help"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	for _, want := range []string{"CLI server options:", "--server", "--socket PATH", "--listen HOST:PORT", "--session-ttl DURATION"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want help to contain %q", stdout.String(), want)
		}
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIJSONOutputEncodesExecutionResult(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-c", "printf 'hello\\n'; printf 'warn\\n' >&2", "--json"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	payload := mustParseCLIJSONResult(t, stdout.String())
	if got, want := payload.Stdout, "hello\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := payload.Stderr, "warn\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if got := payload.ExitCode; got != 0 {
		t.Fatalf("payload exitCode = %d, want 0", got)
	}
	if payload.StdoutTruncated || payload.StderrTruncated {
		t.Fatalf("truncated flags = stdout %t stderr %t, want both false", payload.StdoutTruncated, payload.StderrTruncated)
	}
	if payload.Timing == nil || payload.Timing.StartedAt == "" || payload.Timing.FinishedAt == "" {
		t.Fatalf("timing = %#v, want populated timing fields", payload.Timing)
	}
	if payload.Trace != nil {
		t.Fatalf("trace = %#v, want nil when tracing is disabled", payload.Trace)
	}
}

func TestRunCLIJSONOutputEncodesCLIError(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-c", "pwd", "--json", "--root", "/", "--readwrite-root", "/"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	payload := mustParseCLIJSONResult(t, stdout.String())
	if got := payload.Stdout; got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(payload.Stderr, "gbash: init runtime: --root and --readwrite-root are mutually exclusive") {
		t.Fatalf("stderr = %q, want init-runtime JSON diagnostic", payload.Stderr)
	}
	if payload.Timing != nil {
		t.Fatalf("timing = %#v, want nil when execution never started", payload.Timing)
	}
}

func TestRunCLIJSONOutputRejectsInteractiveShell(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--json"}, strings.NewReader(""), &stdout, &stderr, true)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	payload := mustParseCLIJSONResult(t, stdout.String())
	if !strings.Contains(payload.Stderr, "only supported for non-interactive executions") {
		t.Fatalf("stderr = %q, want non-interactive rejection", payload.Stderr)
	}
}

func TestRunCLIServerRequiresTransport(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--server"}, strings.NewReader(""), &stdout, &stderr, false)
	if err == nil {
		t.Fatal("runCLI() error = nil, want transport requirement")
	}
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(err.Error(), "--socket or --listen") {
		t.Fatalf("error = %v, want transport requirement", err)
	}
}

func TestRunCLIServerRejectsMultipleTransportFlags(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{
		"--server",
		"--socket", filepath.Join(t.TempDir(), "gbash.sock"),
		"--listen", "127.0.0.1:9000",
	}, strings.NewReader(""), &stdout, &stderr, false)
	if err == nil {
		t.Fatal("runCLI() error = nil, want transport conflict")
	}
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want transport conflict", err)
	}
}

func TestRunCLIServerRejectsNonLoopbackListen(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--server", "--listen", "0.0.0.0:9000"}, strings.NewReader(""), &stdout, &stderr, false)
	if err == nil {
		t.Fatal("runCLI() error = nil, want loopback requirement")
	}
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(err.Error(), "loopback host") {
		t.Fatalf("error = %v, want loopback requirement", err)
	}
}

func TestRunCLIServerRejectsScriptExecutionFlags(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--server", "--socket", filepath.Join(t.TempDir(), "gbash.sock"), "-c", "echo hi"}, strings.NewReader(""), &stdout, &stderr, false)
	if err == nil {
		t.Fatal("runCLI() error = nil, want server/script rejection")
	}
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error = %v, want server/script rejection", err)
	}
}

func TestRunCLIServerRejectsJSONMode(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--server", "--socket", filepath.Join(t.TempDir(), "gbash.sock"), "--json"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	payload := mustParseCLIJSONResult(t, stdout.String())
	if !strings.Contains(payload.Stderr, "--server and --json are mutually exclusive") {
		t.Fatalf("stderr = %q, want server/json rejection", payload.Stderr)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIServerListensOnTCP(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	addr := reserveLoopbackTCPAddress(t)
	var stdout strings.Builder
	var stderr strings.Builder
	errCh := make(chan error, 1)
	go func() {
		_, err := runCLI(ctx, "gbash", []string{"--server", "--listen", addr}, strings.NewReader(""), &stdout, &stderr, false)
		errCh <- err
	}()

	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("server exited before tcp listener became ready: %v", err)
		default:
		}
		dialed, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn = dialed
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("timed out waiting for gbash server tcp listener at %s", addr)
	}
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "system.hello",
		"params":  map[string]any{"client_name": "test"},
	}); err != nil {
		t.Fatalf("Encode(system.hello) error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var resp struct {
		Result struct {
			Capabilities struct {
				Transport string `json:"transport"`
			} `json:"capabilities"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("Decode(system.hello) error = %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("system.hello error = %#v, want success", resp.Error)
	}
	if got, want := resp.Result.Capabilities.Transport, "tcp"; got != want {
		t.Fatalf("transport = %q, want %q", got, want)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for gbash server shutdown")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("server stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("server stderr = %q, want empty", got)
	}
}

func TestRunCLISharedFlagsStopAfterFirstScriptPositional(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-c", `printf '%s\n' "$1"`, "_", "--json"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "--json\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIReadWriteRootPersistsHostWritesAcrossExecutions(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--readwrite-root", tmp, "--cwd", "/", "-c", "printf host-data > shared.txt"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI(write) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("write exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("write stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("write stderr = %q, want empty", got)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "shared.txt"))
	if err != nil {
		t.Fatalf("ReadFile(shared.txt) error = %v", err)
	}
	if got, want := string(data), "host-data"; got != want {
		t.Fatalf("host file contents = %q, want %q", got, want)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode, err = runCLI(context.Background(), "gbash", []string{"--readwrite-root=" + tmp, "--cwd=/", "-c", "pwd; cat shared.txt"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI(read) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("read exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "/\nhost-data"; got != want {
		t.Fatalf("read stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("read stderr = %q, want empty", got)
	}
}

func mustParseCLIJSONResult(t *testing.T, raw string) cliJSONResult {
	t.Helper()

	var out cliJSONResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("Unmarshal(JSON output) error = %v; raw=%q", err, raw)
	}
	return out
}

func reserveLoopbackTCPAddress(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(tcp) error = %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close(tcp listener) error = %v", err)
	}
	return addr
}

func TestRunCLIRootMountReadsHostFilesWithoutPersistingWrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(subdir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "host.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(host.txt) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--root", root, "--cwd", gbash.DefaultWorkspaceMountPoint + "/subdir", "-c", "pwd; cat host.txt; printf overlay > overlay.txt"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), gbash.DefaultWorkspaceMountPoint+"/subdir\nseed\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(subdir, "overlay.txt")); !os.IsNotExist(err) {
		t.Fatalf("overlay.txt exists on host, want overlay-only write; err=%v", err)
	}
}

func TestRunCLIFilesystemFlagsRejectConflictingModes(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"--root", "/", "--readwrite-root", "/", "-c", "pwd"}, strings.NewReader(""), &stdout, &stderr, false)
	if err == nil {
		t.Fatalf("runCLI() error = nil, want conflict error")
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want mutually exclusive diagnostic", err)
	}
}

func TestRunCLIReadWriteRootRejectsNonTempDirectories(t *testing.T) {
	t.Parallel()
	var stdout strings.Builder
	var stderr strings.Builder

	nonTempRoot := filepath.Dir(filepath.Clean(os.TempDir()))
	exitCode, err := runCLI(context.Background(), "gbash", []string{"--readwrite-root", nonTempRoot, "--cwd", "/", "-c", "pwd"}, strings.NewReader(""), &stdout, &stderr, false)
	if err == nil {
		t.Fatalf("runCLI() error = nil, want temp-directory restriction")
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(err.Error(), "system temp directory") {
		t.Fatalf("error = %v, want temp-directory diagnostic", err)
	}
}

func TestRunCLICommandStringSupportsGroupedShortFlags(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()

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
			t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestRunCLIHostUtilityPassesStdin(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "cat", nil, strings.NewReader("stdin-data"), &stdout, &stderr, false, nil)
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
func TestRunCLIHostUtilityCatRejectsAppendToSelf(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmp, "out"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(out) error = %v", err)
	}

	stdout, err := os.OpenFile(filepath.Join(tmp, "out"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("OpenFile(out append) error = %v", err)
	}
	defer func() { _ = stdout.Close() }()

	var stderr strings.Builder
	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "cat", []string{"out"}, strings.NewReader(""), stdout, &stderr, false, nil)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stderr.String(), "cat: out: input file is output file\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "out"))
	if err != nil {
		t.Fatalf("ReadFile(out) error = %v", err)
	}
	if got, want := string(data), "x\n"; got != want {
		t.Fatalf("out = %q, want %q", got, want)
	}
}

func TestRunCLIHostUtilityCatCanCopyThroughSharedReadWriteTarget(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmp, "fxy1"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(fxy1) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "fy"), []byte("y\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(fy) error = %v", err)
	}

	stdin, err := os.Open(filepath.Join(tmp, "fxy1"))
	if err != nil {
		t.Fatalf("Open(fxy1) error = %v", err)
	}
	defer func() { _ = stdin.Close() }()

	stdout, err := os.OpenFile(filepath.Join(tmp, "fxy1"), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(fxy1 readwrite) error = %v", err)
	}
	defer func() { _ = stdout.Close() }()

	var stderr strings.Builder
	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "cat", []string{"-", "fy"}, stdin, stdout, &stderr, false, nil)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "fxy1"))
	if err != nil {
		t.Fatalf("ReadFile(fxy1) error = %v", err)
	}
	if got, want := string(data), "x\ny\n"; got != want {
		t.Fatalf("fxy1 = %q, want %q", got, want)
	}
}

func TestRunCLIHostUtilityPassesGBASHUmaskToRuntime(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	target := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(file.txt) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "chmod", []string{"a=r,=x", "file.txt"}, strings.NewReader(""), &stdout, &stderr, false, &hostUtilityOpts{gbashUmask: "0005"})
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

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat(file.txt) error = %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o110); got != want {
		t.Fatalf("mode = %#o, want %#o", got, want)
	}
}

func TestRunCLIHostUtilityChownNoOpOnExistingOwnership(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	if err := os.Mkdir(filepath.Join(tmp, "keep"), 0o755); err != nil {
		t.Fatalf("Mkdir(keep) error = %v", err)
	}

	spec := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "chown", []string{"-R", spec, "keep"}, strings.NewReader(""), &stdout, &stderr, false, nil)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIHostUtilityChownAcceptsCurrentUsername(t *testing.T) {
	t.Parallel()
	current, err := user.Current()
	if err != nil || strings.TrimSpace(current.Username) == "" {
		t.Skipf("user.Current() unavailable: %v", err)
	}

	tmp := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmp, "owned.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(owned.txt) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "chown", []string{current.Username, "owned.txt"}, strings.NewReader(""), &stdout, &stderr, false, nil)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}
func TestRunCLIHostUtilityUnknownCommandReturns127(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "missing-command", nil, strings.NewReader(""), &stdout, &stderr, false, nil)
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

func TestRunCLIHostUtilityYesReportsSingleWriteErrorOnDevFull(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "yes", []string{"x"}, strings.NewReader(""), full, &stderr, false, nil)
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

func TestRunCLIHostUtilityEnvSupportsDoubleDashCommandSeparator(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "env", []string{"--", "pwd", "-P"}, strings.NewReader(""), &stdout, &stderr, false, nil)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "/\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIHostUtilityEnvSupportsAssignmentsAfterDoubleDash(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	physicalDir := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(physicalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", physicalDir, err)
	}
	logicalDir := filepath.Join(tmp, "c")
	if err := os.Symlink(physicalDir, logicalDir); err != nil {
		t.Skipf("Symlink() error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "env", []string{"--", "POSIXLY_CORRECT=1", "pwd"}, strings.NewReader(""), &stdout, &stderr, false, &hostUtilityOpts{cwd: logicalDir})
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "/a/b\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunCLIHostUtilityStreamsOutputBeforeExit(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	var stderr strings.Builder
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "seq", []string{"999999", "inf"}, strings.NewReader(""), stdout, &stderr, false, nil)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	if !stdout.WaitForSubstring("999999\n1000000\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not stream expected prefix before the host utility exited; got %q", stdout.String())
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

func TestRunCLIHostUtilityTailFollowMissingFileByName(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"-F", "-s0.05", "--max-unchanged-stats=1", "missing/file"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowMissingFlatFileByName(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"--follow=name", "--retry", "-s0.05", "--max-unchanged-stats=1", "missing"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowUntailableByNameUntilFileAppears(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"-F", "-s0.05", "--max-unchanged-stats=1", "untailable"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowDescriptorSurvivesRename(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"-f", "-s0.05", "a"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowByNameHandlesRenameAndReplacement(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"-F", "-s0.05", "--max-unchanged-stats=1", "a", "b"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailGroupedQuietFollowFlags(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"-qF", "-s0.05", "--max-unchanged-stats=1", "1", "2"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowByNameWithoutRetryFailsWhenMissingInitially(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"--follow=name", "no-such"}, strings.NewReader(""), &stdout, &stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowByNameWithoutRetryStopsWhenFileDisappears(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"--follow=name", "-s0.05", "--max-unchanged-stats=1", "file"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowByNameWithoutRetryTracksReappearingFileWhileOthersRemain(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"--follow=name", "-s0.05", "--max-unchanged-stats=1", "a", "foo"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowPidIsUnsupported(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmp, "here"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(here) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"-f", "-s0.05", "--pid=2147483647", "--pid=" + strconv.Itoa(os.Getpid()), "here"}, strings.NewReader(""), &stdout, &stderr, false, nil)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "tail: --pid is unsupported in this sandbox\n" {
		t.Fatalf("stderr = %q, want unsupported --pid error", got)
	}
}

func TestRunCLIHostUtilityTailFollowPidIsUnsupportedForDeadPidToo(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmp, "empty"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(empty) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"-f", "-s10", "--pid=2147483647", "empty"}, strings.NewReader(""), &stdout, &stderr, false, nil)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1; stderr=%q", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "tail: --pid is unsupported in this sandbox\n" {
		t.Fatalf("stderr = %q, want unsupported --pid error", got)
	}
}

func TestRunCLIHostUtilityTailRetryWarnsWithoutFollow(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmp, "file"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(file) error = %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"--retry", "file"}, strings.NewReader(""), &stdout, &stderr, false, nil)
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

func TestRunCLIHostUtilityTailRetryDescriptorReportsAppearanceAndTruncation(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	stdout := newStreamingWriter()
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"--follow=descriptor", "--retry", "-s0.05", "missing"}, strings.NewReader(""), stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailRetryDescriptorGivesUpOnUntailableReplacement(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	stderr := newStreamingWriter()
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"--follow=descriptor", "--retry", "-s0.05", "missing"}, strings.NewReader(""), &stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowDashReadsStandardInput(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"-f", "-"}, strings.NewReader("line\n"), &stdout, &stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowDashReportsClosedStdin(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"-f", "-"}, stdin, &stdout, &stderr, false, nil)
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

func TestRunCLIHostUtilityTailFollowNameRejectsDash(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "tail", []string{"--follow=name", "-"}, strings.NewReader("line\n"), &stdout, &stderr, false, nil)
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

func TestRunCLIHostUtilityTailDebugReportsPollingMode(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

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
		exitCode, err := runCLIHostUtility(t, ctx, tmp, "tail", []string{"--debug", "-n0", "-F", "-s0.05", "a"}, strings.NewReader(""), &stdout, stderr, false, nil)
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

func TestRunCLIHostUtilityPwdHonorsLogicalAndPhysicalModes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	physicalDir := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(physicalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", physicalDir, err)
	}
	logicalDir := filepath.Join(tmp, "c")
	if err := os.Symlink(physicalDir, logicalDir); err != nil {
		t.Skipf("Symlink() error = %v", err)
	}

	cwdOpts := &hostUtilityOpts{cwd: logicalDir}

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLIHostUtility(t, context.Background(), tmp, "pwd", []string{"-L"}, strings.NewReader("ignored"), &stdout, &stderr, false, cwdOpts)
	if err != nil {
		t.Fatalf("runCLI(-L) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "/a/b\n"; got != want {
		t.Fatalf("stdout(-L) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(-L) = %q, want empty", got)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode, err = runCLIHostUtility(t, context.Background(), tmp, "pwd", []string{"--physical"}, strings.NewReader("ignored"), &stdout, &stderr, false, cwdOpts)
	if err != nil {
		t.Fatalf("runCLI(--physical) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "/a/b\n"; got != want {
		t.Fatalf("stdout(--physical) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(--physical) = %q, want empty", got)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode, err = runCLIHostUtility(t, context.Background(), tmp, "pwd", nil, strings.NewReader("ignored"), &stdout, &stderr, false, &hostUtilityOpts{cwd: logicalDir, posixlyCorrect: "1"})
	if err != nil {
		t.Fatalf("runCLI(POSIXLY_CORRECT) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "/a/b\n"; got != want {
		t.Fatalf("stdout(POSIXLY_CORRECT) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(POSIXLY_CORRECT) = %q, want empty", got)
	}
}

type hostUtilityOpts struct {
	cwd            string // defaults to root when empty
	gbashUmask     string
	posixlyCorrect string
}

func runCLIHostUtility(t *testing.T, ctx context.Context, root, utility string, utilityArgs []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool, opts *hostUtilityOpts) (int, error) {
	t.Helper()

	cwd := root
	var gbashUmask, posixlyCorrect string
	if opts != nil {
		if opts.cwd != "" {
			cwd = opts.cwd
		}
		gbashUmask = opts.gbashUmask
		posixlyCorrect = opts.posixlyCorrect
	}

	sandboxCwd, err := currentSandboxCwd(root, cwd)
	if err != nil {
		t.Fatalf("currentSandboxCwd(%q, %q) error = %v", root, cwd, err)
	}

	args := []string{
		"--readwrite-root", root,
		"--cwd", sandboxCwd,
		"-c", `GBASH_UMASK=$1; export GBASH_UMASK; POSIXLY_CORRECT=$2; if [ -n "$POSIXLY_CORRECT" ]; then export POSIXLY_CORRECT; else unset POSIXLY_CORRECT; fi; shift 2; exec "$@"`,
		"_",
		gbashUmask,
		posixlyCorrect,
		utility,
	}
	args = append(args, utilityArgs...)
	return runCLI(ctx, "gbash", args, stdin, stdout, stderr, stdinTTY)
}

func currentSandboxCwd(root, cwd string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}

	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	sandboxCwd, ok, err := sandboxPathWithinRoot(root, cwd)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("cwd %q is outside %q", cwd, root)
	}
	return sandboxCwd, nil
}

func sandboxPathWithinRoot(root, cwd string) (sandboxPath string, withinRoot bool, err error) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(cwd))
	if err != nil {
		return "", false, err
	}
	if rel == "." {
		return "/", true, nil
	}
	parent := ".." + string(os.PathSeparator)
	if rel == ".." || strings.HasPrefix(rel, parent) {
		return "", false, nil
	}
	return "/" + filepath.ToSlash(rel), true, nil
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
