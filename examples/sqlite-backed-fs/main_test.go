package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresDB(t *testing.T) {
	t.Parallel()
	_, err := run(context.Background(), strings.NewReader(""), io.Discard, io.Discard, nil)
	if err == nil {
		t.Fatal("run() error = nil, want required db error")
	}
	if !strings.Contains(err.Error(), "--db is required") {
		t.Fatalf("run() error = %v, want db requirement", err)
	}
}

func TestRunUsesScriptFlagAndStdin(t *testing.T) {
	t.Parallel()
	t.Run("script flag", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "flag.db")
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode, err := run(context.Background(), strings.NewReader("ignored\n"), &stdout, &stderr, []string{
			"--db", dbPath,
			"--script", "printf 'flag\\n'\n",
		})
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
		}
		if got, want := stdout.String(), "flag\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("stdin", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "stdin.db")
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode, err := run(context.Background(), strings.NewReader("printf 'stdin\\n'\n"), &stdout, &stderr, []string{
			"--db", dbPath,
		})
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
		}
		if got, want := stdout.String(), "stdin\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
}

func TestRunREPLPersistsCWDAndEnvAcrossEntries(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "repl.db")
	input := strings.NewReader("pwd\ncd /tmp\npwd\nexport FOO=bar\necho $FOO\nexit\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode, err := run(context.Background(), input, &stdout, &stderr, []string{
		"--db", dbPath,
		"--repl",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
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

func TestRunREPLSupportsMultilineStatements(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "multiline.db")
	input := &chunkReader{
		chunks: []string{
			"if true; then\n",
			" echo hi\n",
			"fi\n",
			"exit\n",
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode, err := run(context.Background(), input, &stdout, &stderr, []string{
		"--db", dbPath,
		"--repl",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	out := stdout.String()
	if strings.Count(out, sqliteContinuationPrompt) < 2 {
		t.Fatalf("stdout = %q, want at least two continuation prompts", out)
	}
	if !strings.Contains(out, "hi\n~$ ") {
		t.Fatalf("stdout = %q, want multiline command output", out)
	}
}

func TestRunREPLHonorsExitStatus(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "exit.db")
	input := strings.NewReader("echo hi\nexit 7\necho later\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode, err := run(context.Background(), input, &stdout, &stderr, []string{
		"--db", dbPath,
		"--repl",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
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

func TestRunRejectsREPLAndScriptTogether(t *testing.T) {
	t.Parallel()
	_, err := run(context.Background(), strings.NewReader(""), io.Discard, io.Discard, []string{
		"--db", filepath.Join(t.TempDir(), "conflict.db"),
		"--repl",
		"--script", "pwd\n",
	})
	if err == nil {
		t.Fatal("run() error = nil, want repl/script conflict")
	}
	if !strings.Contains(err.Error(), "--repl and --script cannot be used together") {
		t.Fatalf("run() error = %v, want repl/script conflict", err)
	}
}

func TestRunPropagatesExitCodeAndStderr(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "stderr.db")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode, err := run(context.Background(), strings.NewReader(""), &stdout, &stderr, []string{
		"--db", dbPath,
		"--script", "echo nope >&2\nexit 7\n",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("exitCode = %d, want 7", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); !strings.Contains(got, "nope\n") {
		t.Fatalf("stderr = %q, want message", got)
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
