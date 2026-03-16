package builtins_test

import "testing"

func TestPrintfSupportsBashNumericCharConstants(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"single=\"'A\"\n"+
			"double='\"B'\n"+
			"printf '%d|%i|%o|%u|%x|%X|%.1f|%g\\n' \"$single\" \"$single\" \"$single\" \"$single\" \"$single\" \"$single\" \"$single\" \"$double\"\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "65|65|101|65|41|41|65.0|66\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPrintfCharacterFormatUsesFirstCharacter(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"quoted=\"'B\"\n"+
			"printf '%c%c%c%c' A 65 \"$quoted\" '' | od -An -tx1 -v\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 41 36 27 00\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
