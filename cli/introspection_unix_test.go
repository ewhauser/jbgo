//go:build !windows && !js

package cli

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRunFileScriptCopyScriptRejectsNamedPipeSource(t *testing.T) {
	t.Parallel()

	fifoPath := filepath.Join(t.TempDir(), "script.fifo")
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("Mkfifo(%q) error = %v", fifoPath, err)
	}

	exitCode, stdout, stderr, err := runCLI(t, []string{"--copy-script", fifoPath}, "")
	if err == nil {
		t.Fatal("run() error = nil, want non-regular source failure")
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if got, want := stdout, ""; got != want {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr, ""; got != want {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if got, want := err.Error(), fifoPath+": copy-script source must be a regular file"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
