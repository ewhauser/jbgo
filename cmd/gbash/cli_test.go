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
	if !stdout.WaitForSubstring("==> a <==\nx\n", 500*time.Millisecond) {
		t.Fatalf("stdout did not emit followed content for a; got %q", stdout.String())
	}

	if err := os.Rename(filepath.Join(tmp, "a"), filepath.Join(tmp, "b")); err != nil {
		t.Fatalf("Rename(a,b) error = %v", err)
	}
	if !stderr.WaitForSubstring("has become inaccessible", 500*time.Millisecond) {
		t.Fatalf("stderr did not report inaccessible file; got %q", stderr.String())
	}
	if !stderr.WaitForSubstring("has been replaced", 500*time.Millisecond) {
		t.Fatalf("stderr did not report replaced file; got %q", stderr.String())
	}

	if err := os.WriteFile(filepath.Join(tmp, "a"), []byte("x2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(a) second generation error = %v", err)
	}
	if !stderr.WaitForSubstring("has appeared", 500*time.Millisecond) {
		t.Fatalf("stderr did not report file appearance; got %q", stderr.String())
	}
	if !stdout.WaitForSubstring("==> a <==\nx2\n", 500*time.Millisecond) {
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
	if !stdout.WaitForSubstring("==> b <==\ny\n", 500*time.Millisecond) {
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
	if !stdout.WaitForSubstring("==> a <==\nz\n", 500*time.Millisecond) {
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

	if err := os.WriteFile(filepath.Join(tmp, "file"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(file) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var stdout strings.Builder
	var stderr strings.Builder
	done := make(chan struct {
		exitCode int
		err      error
	}, 1)

	go func() {
		exitCode, err := runCLI(ctx, "gbash", []string{
			"compat", "exec", "tail", "--follow=name", "-s0.05", "--max-unchanged-stats=1", "file",
		}, strings.NewReader(""), &stdout, &stderr, false)
		done <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()

	time.Sleep(150 * time.Millisecond)
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
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("tail --follow=name did not exit after the file disappeared; stderr=%q", stderr.String())
	}

	if !strings.Contains(stderr.String(), "has become inaccessible") {
		t.Fatalf("stderr = %q, want inaccessible diagnostic", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no files remaining") {
		t.Fatalf("stderr = %q, want no-files-remaining diagnostic", stderr.String())
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
