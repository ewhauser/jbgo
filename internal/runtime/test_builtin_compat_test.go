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
			"printf 'bracket-lparen=%s\\n' \"$?\"\n"+
			"test \\( x -a \\( y \\) \\)\n"+
			"printf 'test-nested-and=%s\\n' \"$?\"\n"+
			"[ \\( x -a \\( y \\) \\) ]\n"+
			"printf 'bracket-nested-and=%s\\n' \"$?\"\n"+
			"test \\( x -o \\( y \\) \\)\n"+
			"printf 'test-nested-or=%s\\n' \"$?\"\n"+
			"[ \\( x -o \\( y \\) \\) ]\n"+
			"printf 'bracket-nested-or=%s\\n' \"$?\"\n"+
			"test \\( x -a \\( y -a \\( z \\) \\) \\)\n"+
			"printf 'test-deep-nested-and=%s\\n' \"$?\"\n"+
			"[ \\( x -a \\( y -a \\( z \\) \\) \\) ]\n"+
			"printf 'bracket-deep-nested-and=%s\\n' \"$?\"\n",
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
		"bracket-lparen=1\n"+
		"test-nested-and=0\n"+
		"bracket-nested-and=0\n"+
		"test-nested-or=0\n"+
		"bracket-nested-or=0\n"+
		"test-deep-nested-and=0\n"+
		"bracket-deep-nested-and=0\n"; got != want {
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

func TestShellClassicTestRejectsEmptyGroupedExpression(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"test x -a \\( \\)\n"+
			"printf 'test-empty=%s\\n' \"$?\"\n"+
			"[ x -a \\( \\) ]\n"+
			"printf 'bracket-empty=%s\\n' \"$?\"\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, ""+
		"test-empty=2\n"+
		"bracket-empty=2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, ""+
		"test: `)' expected\n"+
		"[: `)' expected\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}
