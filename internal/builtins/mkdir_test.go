package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestMkdirSupportsModeFlags(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -m 0700 /home/agent/secure\nmkdir --mode=u=rwx,go= /home/agent/symbolic\nstat -c '%a' /home/agent/secure /home/agent/symbolic\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "0700\n0700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMkdirWithoutParentsRequiresExistingParent(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir /home/agent/missing/child\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-parent error", result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/missing/child"); err == nil {
		t.Fatalf("Stat(child) unexpectedly succeeded")
	}
}
