package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestIDReportsDeterministicSandboxIdentity(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "id\nid -u\nid -g\nid -Gn\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "uid=1000(agent) gid=1000(agent) groups=1000(agent)\n1000\n1000\nagent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestIDSupportsCompatibilityFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "id -a\nid -A\nid -u -n\nid -u -r\nid -G -z\nid -p\nid -P\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if !strings.HasPrefix(result.Stdout, "uid=1000(agent) gid=1000(agent) groups=1000(agent)\n") {
		t.Fatalf("Stdout = %q, want default-format prefix", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "agent\n1000\n1000\x00") {
		t.Fatalf("Stdout = %q, want name, real-id, and NUL-delimited group output", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "uid\tagent\ngroups\tagent\n") {
		t.Fatalf("Stdout = %q, want -p human-readable block", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "agent:x:1000:1000::/home/agent:/bin/sh\n") {
		t.Fatalf("Stdout = %q, want password-record output", result.Stdout)
	}
}

func TestIDRejectsUnsupportedContextAndContinuesPastMissingUsers(t *testing.T) {
	session := newSession(t, &Config{})

	contextResult := mustExecSession(t, session, "id -Z\n")
	if contextResult.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want non-zero for -Z without sandbox context")
	}
	if !strings.Contains(contextResult.Stderr, "works only on an SELinux/SMACK-enabled kernel") {
		t.Fatalf("Stderr = %q, want unsupported-context error", contextResult.Stderr)
	}

	result := mustExecSession(t, session, "id -u missing agent\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want non-zero when one requested user is missing")
	}
	if got, want := result.Stdout, "1000\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "missing: no such user") {
		t.Fatalf("Stderr = %q, want missing-user error", result.Stderr)
	}
}

func TestYesRepeatsDefaultAndCustomOperandsUntilTimeout(t *testing.T) {
	session := newSession(t, &Config{})

	defaultResult := mustExecSession(t, session, "timeout 0.02 yes > /tmp/yes-default.out || true\nhead -n 3 /tmp/yes-default.out\n")
	if defaultResult.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", defaultResult.ExitCode, defaultResult.Stderr)
	}
	if got, want := defaultResult.Stdout, "y\ny\ny\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	customResult := mustExecSession(t, session, "timeout 0.02 yes foo bar > /tmp/yes-custom.out || true\nhead -n 3 /tmp/yes-custom.out\n")
	if customResult.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", customResult.ExitCode, customResult.Stderr)
	}
	if got, want := customResult.Stdout, "foo bar\nfoo bar\nfoo bar\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestYesRejectsUnknownOptionsAndSupportsHelpVersion(t *testing.T) {
	rt := newRuntime(t, &Config{})

	helpResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "yes --help\n"})
	if err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if helpResult.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", helpResult.ExitCode, helpResult.Stderr)
	}
	if !strings.Contains(helpResult.Stdout, "usage: yes [STRING]...") {
		t.Fatalf("Stdout = %q, want help text", helpResult.Stdout)
	}

	versionResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "yes --version\n"})
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if versionResult.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", versionResult.ExitCode, versionResult.Stderr)
	}
	if !strings.Contains(versionResult.Stdout, "yes (jbgo)") {
		t.Fatalf("Stdout = %q, want version text", versionResult.Stdout)
	}

	invalidResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "yes --bogus\n"})
	if err != nil {
		t.Fatalf("Run(invalid) error = %v", err)
	}
	if invalidResult.ExitCode == 0 {
		t.Fatalf("invalid ExitCode = 0, want non-zero")
	}
	if !strings.Contains(invalidResult.Stderr, "unrecognized option") {
		t.Fatalf("Stderr = %q, want invalid-option error", invalidResult.Stderr)
	}
}
