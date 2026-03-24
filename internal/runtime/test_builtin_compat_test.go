package runtime

import "testing"

func TestShellClassicTestMatchesBashClassicAmbiguities(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"test 0 -eq 0 -a '(' -f ')'\n"+
			"printf 'test-f=%s\\n' \"$?\"\n"+
			"test 0 -eq 0 -a '(' -t ')'\n"+
			"printf 'test-t=%s\\n' \"$?\"\n"+
			"test 0 -eq 0 -a '(' ! ')'\n"+
			"printf 'test-bang=%s\\n' \"$?\"\n"+
			"[ 0 -eq 0 -a '(' -f ')' ]\n"+
			"printf 'bracket-f=%s\\n' \"$?\"\n"+
			"[ 0 -eq 0 -a '(' -t ')' ]\n"+
			"printf 'bracket-t=%s\\n' \"$?\"\n"+
			"[ 0 -eq 0 -a '(' ! ')' ]\n"+
			"printf 'bracket-bang=%s\\n' \"$?\"\n"+
			"test ! x -a ! x\n"+
			"printf 'test-leading-bang=%s\\n' \"$?\"\n"+
			"[ ! x -a ! x ]\n"+
			"printf 'bracket-leading-bang=%s\\n' \"$?\"\n"+
			"test \\( x = \\) \\)\n"+
			"printf 'test-rparen=%s\\n' \"$?\"\n"+
			"[ \\( x = \\) \\) ]\n"+
			"printf 'bracket-rparen=%s\\n' \"$?\"\n"+
			"test \\( x = \\( \\)\n"+
			"printf 'test-lparen=%s\\n' \"$?\"\n"+
			"[ \\( x = \\( \\) ]\n"+
			"printf 'bracket-lparen=%s\\n' \"$?\"\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, ""+
		"test-f=0\n"+
		"test-t=0\n"+
		"test-bang=0\n"+
		"bracket-f=0\n"+
		"bracket-t=0\n"+
		"bracket-bang=0\n"+
		"test-leading-bang=1\n"+
		"bracket-leading-bang=1\n"+
		"test-rparen=2\n"+
		"bracket-rparen=2\n"+
		"test-lparen=1\n"+
		"bracket-lparen=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != ""+
		"test: x: unary operator expected\n"+
		"[: x: unary operator expected\n" && got != ""+
		"test: = must be followed by a word\n"+
		"[: = must be followed by a word\n" {
		t.Fatalf("Stderr = %q, want one of the expected parse diagnostics", got)
	}
}
