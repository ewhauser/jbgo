package builtins_test

import (
	"context"
	"testing"
)

func TestEnvSupportsLongIgnoreEnvironmentIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env --ignore-environment ONLY=present printenv ONLY\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "present\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsBareDoubleDashCommandSeparator(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -- printenv HOME\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsAssignmentsAfterBareDoubleDash(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -- ONLY=present printenv ONLY\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "present\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvPreservesInvalidBytesForNestedCommands(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "value=$(printf '\\355\\272\\255')\n" +
			"env printf '%s' \"$value\" | od -An -tx1 -v\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "           ed  ba  ad                                                    \n\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsUnsetShortAndLongAttachedForms(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -uHOME --unset=USER printenv HOME USER\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
}

func TestEnvReportsMissingUnsetArgument(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -u\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: option requires an argument -- 'u'\nTry 'env --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestPrintEnvReturnsOneWhenAnyNameIsMissing(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printenv HOME MISSING\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsChdirWithCommand(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir /tmp/work\nenv --chdir=/tmp/work pwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/work\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvReportsMissingCommandForChdir(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir /tmp/work\nenv -C /tmp/work\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 125 {
		t.Fatalf("ExitCode = %d, want 125; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: must specify command with --chdir (-C)\nTry 'env --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvDebugReportsArgv0Override(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -v -a short true\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "argv0:     'short'\nexecuting: true\n   arg[0]= 'short'\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvNestedShellResolvesEqualsCommandThroughEmptyPathEntry(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=$PATH:\n" +
			"export PATH\n" +
			"cat <<'EOF' > ./c=d\n" +
			"#!/bin/sh\n" +
			"echo pass\n" +
			"EOF\n" +
			"chmod 755 ./c=d\n" +
			"env sh -c 'exec \"$@\"' sh c=d echo fail\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "pass\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
