package runtime

import "testing"

func TestRedirectRegressionSupportsOverwriteAppendAndInputRedirection(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo first > log.txt\n echo second >> log.txt\n cat < log.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "first\nsecond\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionChainsShellAndRegistryCommands(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'alpha\nbeta\nbeta\n' > words.txt\n cat words.txt | grep beta | head -n 1\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCommandSubstitutionRegressionFeedsExpandedArgs(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/note.txt", []byte("sandbox\n"))

	result := mustExecSession(t, session, "name=$(cat note.txt)\n echo \"hello $name\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello sandbox\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestConditionalRegressionSupportsBuiltinStringPredicates(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "status=ready\n if test \"$status\" = ready; then echo exists; else echo missing; fi\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "exists\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLoopRegressionSupportsForControlFlow(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "for name in alpha beta; do echo \"item:$name\"; done\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "item:alpha\nitem:beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
