package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunCLIInteractivePersistsCWDAndEnvAcrossEntries(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("pwd\ncd /tmp\npwd\nexport FOO=bar\necho $FOO\nexit\n")
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-i"}, input, &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	out := stdout.String()
	for _, want := range []string{
		"~$ /home/agent\n",
		"/tmp$ /tmp\n",
		"bar\n/tmp$ ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want substring %q", out, want)
		}
	}
}

func TestRunCLIInteractiveSupportsMultilineStatements(t *testing.T) {
	t.Parallel()

	input := &chunkReader{
		chunks: []string{
			"if true; then\n",
			" echo hi\n",
			"fi\n",
			"exit\n",
		},
	}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-i"}, input, &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	out := stdout.String()
	if strings.Count(out, continuationPrompt) < 2 {
		t.Fatalf("stdout = %q, want at least two continuation prompts", out)
	}
	if !strings.Contains(out, "hi\n~$ ") {
		t.Fatalf("stdout = %q, want multiline command output", out)
	}
}

func TestRunCLIInteractiveHonorsExitStatus(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("echo hi\nexit 7\necho later\n")
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-i"}, input, &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("exitCode = %d, want 7", exitCode)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	out := stdout.String()
	if !strings.Contains(out, "hi\n~$ ") {
		t.Fatalf("stdout = %q, want first command output", out)
	}
	if strings.Contains(out, "later") {
		t.Fatalf("stdout = %q, did not expect commands after exit", out)
	}
}

func TestRunCLIInteractiveProvidesVirtualTTY(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("tty\nexit\n")
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-i"}, input, &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got := stdout.String(); !strings.Contains(got, "/dev/tty\n~$ ") {
		t.Fatalf("stdout = %q, want tty output with prompt", got)
	}
}

func TestRunCLIInteractiveStartupOptionsPersistAcrossEntries(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("set +o nounset\necho X${MISSING}Y\nexit\n")
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash", []string{"-iu"}, input, &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got := stdout.String(); !strings.Contains(got, "XY\n~$ ") {
		t.Fatalf("stdout = %q, want nounset change to persist", got)
	}
}

type chunkReader struct {
	chunks []string
	index  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.index])
	r.chunks[r.index] = r.chunks[r.index][n:]
	if r.chunks[r.index] == "" {
		r.index++
	}
	return n, nil
}
