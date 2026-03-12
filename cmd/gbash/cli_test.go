package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
	want := filepath.ToSlash(tmp) + "\n"
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
