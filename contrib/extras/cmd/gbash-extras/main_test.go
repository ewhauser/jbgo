package main

import (
	"context"
	"io"
	"strings"
	"testing"

	rootcli "github.com/ewhauser/gbash/cli"
)

func runCLI(ctx context.Context, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	cfg := newCLIConfig()
	cfg.TTYDetector = func(io.Reader) bool { return stdinTTY }
	return rootcli.Run(ctx, cfg, argv0, args, stdin, stdout, stderr)
}

func TestCLIHelpAndVersionIdentifyBinary(t *testing.T) {
	prevVersion, prevCommit, prevDate, prevBuiltBy := version, commit, date, builtBy
	version, commit, date, builtBy = "v1.2.3", "abc123", "2026-03-10T20:00:00Z", "test"
	t.Cleanup(func() {
		version, commit, date, builtBy = prevVersion, prevCommit, prevDate, prevBuiltBy
	})

	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash-extras", []string{"--help"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI(--help) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stdout.String(); !strings.Contains(got, "Usage: gbash-extras") {
		t.Fatalf("stdout = %q, want gbash-extras usage", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode, err = runCLI(context.Background(), "gbash-extras", []string{"--version"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI(--version) error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	want := "gbash-extras v1.2.3\ncommit: abc123\nbuilt: 2026-03-10T20:00:00Z\nbuilt-by: test\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestCLIRegistersStableExtras(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash-extras", []string{"-c", "printf 'a,b\\n' | awk -F, '{print $2}'\n" +
		"printf '{\"name\":\"alice\"}\\n' | jq -r '.name'\n" +
		"printf 'name: alice\\n' | yq '.name'\n" +
		`sqlite3 :memory: "select 1;"`}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "b\nalice\nalice\n1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestCLIDoesNotRegisterNodeJSByDefault(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode, err := runCLI(context.Background(), "gbash-extras", []string{"-c", "nodejs -e 'console.log(1)'"}, strings.NewReader(""), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("runCLI() error = %v", err)
	}
	if exitCode != 127 {
		t.Fatalf("exitCode = %d, want 127", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); !strings.Contains(got, "nodejs: command not found") {
		t.Fatalf("stderr = %q, want nodejs command-not-found", got)
	}
}
