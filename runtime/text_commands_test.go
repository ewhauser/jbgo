package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestGrepWorksInPipelineFromStdin(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo alpha > /tmp/in.txt\n echo beta >> /tmp/in.txt\n echo alpha-two >> /tmp/in.txt\n cat /tmp/in.txt | grep alpha\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "alpha\nalpha-two\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepRecursiveSearchPrefixesFilenames(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p dir/sub\n echo needle > dir/root.txt\n echo another needle > dir/sub/file.txt\n grep -r needle dir\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/home/agent/dir/root.txt:needle", "/home/agent/dir/sub/file.txt:another needle"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
}

func TestGrepReturnsExitCodeOneWhenNoMatch(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hello > /tmp/in.txt\n grep missing /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("want empty output on no-match, got stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestGrepReturnsExitCodeTwoOnMissingFile(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "grep pattern /missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/missing.txt") {
		t.Fatalf("Stderr = %q, want missing-file error", result.Stderr)
	}
}

func TestHeadReadsFirstNLines(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/in.txt\n echo two >> /tmp/in.txt\n echo three >> /tmp/in.txt\n head -n 2 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "one\ntwo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHeadShowsHeadersForMultipleFiles(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo aaa > /tmp/a.txt\n echo bbb > /tmp/b.txt\n head /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "==> /tmp/a.txt <==\naaa\n\n==> /tmp/b.txt <==\nbbb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTailSupportsFromLineSyntax(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/in.txt\n echo two >> /tmp/in.txt\n echo three >> /tmp/in.txt\n echo four >> /tmp/in.txt\n tail -n +3 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "three\nfour\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTailWorksInPipeline(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/in.txt\n echo two >> /tmp/in.txt\n echo three >> /tmp/in.txt\n cat /tmp/in.txt | tail -n 2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "two\nthree\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCReportsTotalsForMultipleFiles(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/a.txt\n echo two words > /tmp/b.txt\n wc /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/tmp/a.txt", "/tmp/b.txt", "total"} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
		}
	}
}

func TestWCCountsWordsFromStdin(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one two three | wc -w\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := strings.TrimSpace(result.Stdout), "3"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCCountsBinaryBytes(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/binary.bin", []byte{0x41, 0x00, 0x42, 0x00, 0x43})

	result := mustExecSession(t, session, "wc -c /tmp/binary.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "5 /tmp/binary.bin") {
		t.Fatalf("Stdout = %q, want byte count for binary file", result.Stdout)
	}
}
