package builtins_test

import (
	"strings"
	"testing"
)

func TestFmtHelpAndVersion(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, "fmt --help\nprintf '%s\\n' '---'\nfmt --version\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Usage: fmt [-WIDTH] [OPTION]... [FILE]...\n") {
		t.Fatalf("help stdout missing usage: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "fmt (gbash)\n") {
		t.Fatalf("version stdout missing version line: %q", result.Stdout)
	}
}

func TestFmtHelpAndVersionShortCircuitParsing(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, ""+
		"fmt --help --bad\n"+
		"printf '%s\\n' '---'\n"+
		"fmt -72x --help\n"+
		"printf '%s\\n' '---'\n"+
		"fmt --version --bad\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
	if got := strings.Count(result.Stdout, "Usage: fmt [-WIDTH] [OPTION]... [FILE]...\n"); got != 2 {
		t.Fatalf("help count = %d; stdout=%q", got, result.Stdout)
	}
	if got := strings.Count(result.Stdout, "fmt (gbash)\n"); got != 1 {
		t.Fatalf("version count = %d; stdout=%q", got, result.Stdout)
	}
}

func TestFmtGNUWidthCases(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, "printf 'aa bb cc dd ee' | fmt -w 8\nprintf '%s\\n' '---'\nprintf 'aa bb cc dd ee' | fmt -w 7\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "aa bb cc\ndd ee\n---\naa\nbb cc\ndd ee\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestFmtGNUGoalOptionCase(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	script := "" +
		"cat <<'EOF' > /tmp/base\n" +
		"\n" +
		"@command{fmt} prefers breaking lines at the end of a sentence, and tries to\n" +
		"avoid line breaks after the first word of a sentence or before the last word\n" +
		"of a sentence.  A @dfn{sentence break} is defined as either the end of a\n" +
		"paragraph or a word ending in any of @samp{.?!}, followed by two spaces or end\n" +
		"of line, ignoring any intervening parentheses or quotes.  Like @TeX{},\n" +
		"@command{fmt} reads entire ''paragraphs'' before choosing line breaks; the\n" +
		"algorithm is a variant of that given by\n" +
		"Donald E. Knuth and Michael F. Plass\n" +
		"in ''Breaking Paragraphs Into Lines'',\n" +
		"@cite{Software---Practice & Experience}\n" +
		"@b{11}, 11 (November 1981), 1119--1184.\n" +
		"EOF\n" +
		"fmt -g 60 -w 72 /tmp/base\n"
	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "\n" +
		"@command{fmt} prefers breaking lines at the end of a sentence,\n" +
		"and tries to avoid line breaks after the first word of a sentence\n" +
		"or before the last word of a sentence.  A @dfn{sentence break}\n" +
		"is defined as either the end of a paragraph or a word ending\n" +
		"in any of @samp{.?!}, followed by two spaces or end of line,\n" +
		"ignoring any intervening parentheses or quotes.  Like @TeX{},\n" +
		"@command{fmt} reads entire ''paragraphs'' before choosing line\n" +
		"breaks; the algorithm is a variant of that given by Donald\n" +
		"E. Knuth and Michael F. Plass in ''Breaking Paragraphs Into\n" +
		"Lines'', @cite{Software---Practice & Experience} @b{11}, 11\n" +
		"(November 1981), 1119--1184.\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestFmtSplitOnlyLongLine(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, "printf '%2030s\\n' ' ' | sed 's/../ y/g' > /tmp/in\nfmt -s /tmp/in\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", result.ExitCode, result.Stderr)
	}

	wantLine := strings.Repeat(" y", 35) + "\n"
	want := strings.Repeat(wantLine, 29)
	if result.Stdout != want {
		t.Fatalf("Stdout length = %d, want %d", len(result.Stdout), len(want))
	}
}

func TestFmtPrefixHandling(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	script := "" +
		"printf ' 1\\n  2\\n\\t3\\n\\t\\t4\\n> quoted\\n> text\\n' | fmt -p '>'\n" +
		"printf '%s\\n' '---'\n" +
		"printf '>\\n' | fmt -p '>'\n" +
		"printf '%s\\n' '---'\n" +
		"printf 'fo\\n' | fmt -p 'foo'\n" +
		"printf '%s\\n' '---'\n" +
		"printf 'ça\\nçb\\n' | fmt -p 'ç'\n" +
		"printf '%s\\n' '---'\n" +
		"printf '> a\\n> b\\n>a\\n' | fmt -p '> '\n"
	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := " 1\n  2\n\t3\n\t\t4\n> quoted text\n---\n>\n---\nfo\n---\nça b\n---\n> a b\n>a\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestFmtGNUErrorCases(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, "fmt -w 32768\nfmt -w 2147483647\nfmt -72x\nfmt -c -72\nfmt no-such-file\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "" +
		"fmt: invalid width: '32768'\n" +
		"fmt: invalid width: '2147483647'\n" +
		"fmt: invalid width: '72x'\n" +
		"fmt: invalid option -- 7; -WIDTH is recognized only when it is the first\n" +
		"option; use -w N instead\n" +
		"Try 'fmt --help' for more information.\n" +
		"fmt: cannot open 'no-such-file' for reading: No such file or directory\n"
	if result.Stderr != want {
		t.Fatalf("Stderr = %q, want %q", result.Stderr, want)
	}
}

func TestFmtDoesNotTreatNonBreakingCharactersAsSpaces(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result := mustExecSession(t, session, ""+
		"printf '=\\u00A0=' | fmt -s -w1 | wc -l\n"+
		"printf '=\\u2007=' | fmt -s -w1 | wc -l\n"+
		"printf '=\\u202F=' | fmt -s -w1 | wc -l\n"+
		"printf '=\\u2060=' | fmt -s -w1 | wc -l\n"+
		"printf '=\\u0445=' | fmt -s -w1 | wc -l\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "1\n1\n1\n1\n1\n" {
		t.Fatalf("Stdout = %q", result.Stdout)
	}
}
