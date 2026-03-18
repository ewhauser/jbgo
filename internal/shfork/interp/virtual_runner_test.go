package interp_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shfork/interp"
)

func TestNewUsesDeterministicVirtualDefaults(t *testing.T) {
	t.Parallel()

	file := parse(t, nil, `printf '%s\n' "$HOME" "$TMPDIR" "$UID" "$EUID" "$GID" "$EGID" "$$" "$PPID" "$PWD"`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	runner, err := interp.New(interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}

	if err := runner.Run(context.Background(), file); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	const want = "/home/agent\n/tmp\n1000\n1000\n1000\n1000\n1\n0\n/home/agent\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestNewVirtualUsesDeterministicDefaults(t *testing.T) {
	t.Parallel()

	file := parse(t, nil, `printf '%s\n' "$HOME" "$TMPDIR" "$UID" "$EUID" "$GID" "$EGID" "$$" "$PPID" "$PWD"`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	runner, err := interp.NewVirtual(&interp.VirtualConfig{}, interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}

	if err := runner.Run(context.Background(), file); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	const want = "/home/agent\n/tmp\n1000\n1000\n1000\n1000\n1\n0\n/home/agent\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestNewWithoutHandlersDoesNotReachHost(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	helperPath := filepath.Join(dir, "helper.sh")
	if err := os.WriteFile(helperPath, []byte("echo leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	file := parse(t, nil, "PATH=/bin:/usr/bin\nenv || echo env-blocked\n. ./helper.sh || echo source-blocked\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	runner, err := interp.New(interp.Dir(filepath.ToSlash(dir)), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}

	if err := runner.Run(context.Background(), file); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := stdout.String(), "env-blocked\nsource-blocked\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if strings.Contains(stdout.String(), "leaked") {
		t.Fatalf("stdout unexpectedly exposed host file contents: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "executable file not found") {
		t.Fatalf("stderr = %q, want command lookup failure", stderr.String())
	}
	if !strings.Contains(stderr.String(), "file does not exist") {
		t.Fatalf("stderr = %q, want closed open-handler failure", stderr.String())
	}
}

func TestProcessSubstitutionRequiresHandler(t *testing.T) {
	t.Parallel()

	file := parse(t, nil, `read value < <(printf 'hi')`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	runner, err := interp.New(interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}

	err = runner.Run(context.Background(), file)
	if err == nil {
		t.Fatal("Run() error = nil, want failure")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "process substitution unavailable") {
		t.Fatalf("stderr = %q, want process substitution failure", stderr.String())
	}
}
