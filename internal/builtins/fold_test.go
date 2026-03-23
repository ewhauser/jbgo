package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestFoldHelpAndVersion(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	helpResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "fold --help\n"})
	if err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if helpResult.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", helpResult.ExitCode, helpResult.Stderr)
	}
	for _, want := range []string{
		"Usage: fold [OPTION]... [FILE]...\n",
		"  -b, --bytes",
		"  -s, --spaces",
		"  -w, --width=WIDTH",
	} {
		if !strings.Contains(helpResult.Stdout, want) {
			t.Fatalf("help stdout = %q, want substring %q", helpResult.Stdout, want)
		}
	}

	versionResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "fold --version\n"})
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if versionResult.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", versionResult.ExitCode, versionResult.Stderr)
	}
	if got, want := versionResult.Stdout, "fold (gbash)\n"; got != want {
		t.Fatalf("version stdout = %q, want %q", got, want)
	}
}

func TestFoldDefaultWrap(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})

	// Line shorter than 80 — no wrap.
	r := mustExecSession(t, session, "printf '1234' | fold\n")
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got, want := r.Stdout, "1234"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFoldHardCutoff(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, input, want string
		width             string
	}{
		{"basic", "1234", "12\n34", "2"},
		{"with-newline", "1234\n", "12\n34\n", "2"},
		{"exact-width", "1\n", "1\n", "2"},
		{"less-than-width", " ", " ", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, "printf '"+tc.input+"' | fold -w"+tc.width+"\n")
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldEmptyLines(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		{"single-newline", "printf '\\n' | fold\n", "\n"},
		{"empty-between", "printf '12\\n\\n34\\n' | fold -w2\n", "12\n\n34\n"},
		{"multiple-empty", "printf '0\\n1\\n\\n2\\n\\n\\n' | fold -w1\n", "0\n1\n\n2\n\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldWordBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, input, want string
		width             string
	}{
		{"basic", "one two", "one \ntwo", "4"},
		{"leading-space", " aaa", " \naaa", "3"},
		{"with-newline", "one two\n", "one \ntwo\n", "4"},
		{"only-spaces", "    ", "  \n  ", "2"},
		{"only-spaces-nl", "    \n", "  \n  \n", "2"},
		{"empty-line", "\n", "\n", "80"},
		{"preserve-empty", "0\n1\n\n2\n\n\n", "0\n1\n\n2\n\n\n", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, "printf '"+tc.input+"' | fold -s -w"+tc.width+"\n")
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldTabBehavior(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		// Tab at start counts as 8 columns.
		{"tab-plus-char", "printf '\\t1' | fold -w8\n", "\t\n1"},
		// Single tab fits in width 8, no extra newline.
		{"single-tab", "printf '\\t' | fold -w1\n", "\t"},
		// Tab after char: "a" is col 1, tab goes to col 8, "bbb" starts at col 8.
		{"tab-at-8", "printf 'a\\tbbb\\n' | fold -w8\n", "a\t\nbbb\n"},
		// Tab after char with wider width: "a\t" goes to col 8, "bb" at 9,10, fold at 10.
		{"tab-after-10", "printf 'a\\tbbb\\n' | fold -w10\n", "a\tbb\nb\n"},
		// Narrow width forces fold before tab.
		{"narrow-tab", "printf 'a\\t1' | fold -w7\n", "a\n\t\n1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldTabWordBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		{"tab-break-8", "printf 'a\\tbbb\\n' | fold -s -w8\n", "a\t\nbbb\n"},
		{"tab-break-10", "printf 'a\\tbbb\\n' | fold -s -w10\n", "a\t\nbbb\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldBackspace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		// BS decreases column count: "1\b" => col 0, "345" => cols 0,1,2 => fold at 2.
		{"decrease-col", "printf '1\\x08345' | fold -w2\n", "1\x08" + "34\n5"},
		// BS doesn't go below 0.
		{"floor-zero", "printf '1\\x08\\x083456' | fold -w2\n", "1\x08\x08" + "34\n56"},
		// BS is not a word boundary.
		{"not-word-boundary", "printf 'foobar\\x086789abcdef' | fold -s -w10\n", "foobar\x086789a\nbcdef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldCarriageReturn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		// CR resets column to 0.
		{"reset-col", "printf '12345\\r123456789abcdef' | fold -w6\n", "12345\r123456\n789abc\ndef"},
		// CR is not a word boundary.
		{"not-word-boundary", "printf 'fizz\\rbuzz\\rfizzbuzz' | fold -s -w6\n", "fizz\rbuzz\rfizzbu\nzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldBytewise(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		{"basic", "printf '1234' | fold -b -w2\n", "12\n34"},
		{"with-newline", "printf '1234\\n' | fold -b -w2\n", "12\n34\n"},
		{"empty-lines", "printf '123\\n\\n45' | fold -b -w2\n", "12\n3\n\n45"},
		{"tab-counts-as-1", "printf '1\\t2\\n' | fold -b -w2\n", "1\t\n2\n"},
		// BS counts as 1 byte, no column decrease.
		{"bs-no-decrease", "printf '1\\x08345' | fold -b -w2\n", "1\x08\n34\n5"},
		// CR counts as 1 byte, no column reset.
		{"cr-no-reset", "printf '12345\\r123456789abcdef' | fold -b -w6\n", "12345\r\n123456\n789abc\ndef"},
		// CR is not word boundary in -b mode either.
		{"cr-not-boundary", "printf 'fizz\\rbuzz\\rfizzbuzz' | fold -b -s -w6\n", "fizz\rb\nuzz\rfi\nzzbuzz"},
		{"less-than-width", "printf '1234' | fold -b\n", "1234"},
		{"equal-width", "printf ' ' | fold -b -w1\n", " "},
		{"spaces-only", "printf '    ' | fold -b -s -w2\n", "  \n  "},
		{"spaces-nl", "printf '    \\n' | fold -b -s -w2\n", "  \n  \n"},
		{"empty-line", "printf '\\n' | fold -b\n", "\n"},
		{"preserve-empty", "printf '0\\n1\\n\\n2\\n\\n\\n' | fold -b -w1\n", "0\n1\n\n2\n\n\n"},
		{"preserve-empty-s", "printf '0\\n1\\n\\n2\\n\\n\\n' | fold -b -s -w1\n", "0\n1\n\n2\n\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldCharacterMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		// Basic character folding.
		{"basic", "printf 'abcdef\\n' | fold -c -w3\n", "abc\ndef\n"},
		// Preserves empty lines.
		{"empty-lines", "printf 'abc\\n\\ndef\\n' | fold -c -w5\n", "abc\n\ndef\n"},
		// Wide chars count as 1 in character mode.
		{"wide-chars", "printf '\\uff1a\\uff1a\\uff1a\\uff1a\\n' | fold -c -w3\n", "\uff1a\uff1a\uff1a\n\uff1a\n"},
		// Word boundary.
		{"word-boundary", "printf 'ab cd ef\\n' | fold -c -s -w5\n", "ab \ncd ef\n"},
		// Tab in character mode.
		{"tab", "printf 'ab\\tcd\\n' | fold -c -w4\n", "ab\n\t\ncd\n"},
		// BS decreases in character mode.
		{"backspace", "printf 'abcde\\x08fg\\n' | fold -c -w5\n", "abcde\x08f\ng\n"},
		// CR resets in character mode.
		{"cr-reset", "printf 'abcd\\refgh\\n' | fold -c -w5\n", "abcd\refgh\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldWideCharacters(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		// Wide chars take 2 columns in default mode.
		{"wide-2col", "printf '\\ub250\\ub250\\ub250\\n' | fold -w5\n", "\ub250\ub250\n\ub250\n"},
		// In character mode, wide chars count as 1.
		{"wide-char-mode", "printf '\\ub250\\ub250\\ub250\\n' | fold -c -w5\n", "\ub250\ub250\ub250\n"},
		// Fullwidth colon (2 cols each): 5 at w10 = fold at 5.
		{"fullwidth-col", "printf '\\uff1a\\uff1a\\uff1a\\uff1a\\uff1a\\n' | fold -w10\n", "\uff1a\uff1a\uff1a\uff1a\uff1a\n"},
		// Fullwidth at w2: each takes 2 cols, fold after each.
		{"fullwidth-narrow", "printf '\\uff45\\uff45' | fold -w2\n", "\uff45\n\uff45"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldCombiningCharacters(t *testing.T) {
	t.Parallel()

	// NFC: é as single character — 1 column each.
	session := newSession(t, &Config{})
	r := mustExecSession(t, session, "printf '\\u00e9\\u00e9\\u00e9' | fold -w2\n")
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got, want := r.Stdout, "\u00e9\u00e9\n\u00e9"; got != want {
		t.Fatalf("NFC Stdout = %q, want %q", got, want)
	}

	// NFD: e + combining accent — base+combining coalesce to 1 column in column mode.
	session2 := newSession(t, &Config{})
	r2 := mustExecSession(t, session2, "printf 'e\\u0301e\\u0301e\\u0301' | fold -w2\n")
	if r2.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r2.ExitCode, r2.Stderr)
	}
	if got, want := r2.Stdout, "e\u0301e\u0301\ne\u0301"; got != want {
		t.Fatalf("NFD Stdout = %q, want %q", got, want)
	}
}

func TestFoldObsoleteSyntax(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	r := mustExecSession(t, session, "printf 'one two' | fold -4 -s\n")
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got, want := r.Stdout, "one \ntwo"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFoldFromFile(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/input.txt", []byte("abcdef\n"))

	r := mustExecSession(t, session, "fold -w3 /tmp/input.txt\n")
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got, want := r.Stdout, "abc\ndef\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFoldMultipleFiles(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/a.txt", []byte("1234\n"))
	writeSessionFile(t, session, "/tmp/b.txt", []byte("5678\n"))

	r := mustExecSession(t, session, "fold -w2 /tmp/a.txt /tmp/b.txt\n")
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got, want := r.Stdout, "12\n34\n56\n78\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFoldStdinDash(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	r := mustExecSession(t, session, "printf 'abcd' | fold -w2 -\n")
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got, want := r.Stdout, "ab\ncd"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFoldMissingFile(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	r := mustExecSession(t, session, "fold /nonexistent\n")
	if r.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want nonzero for missing file")
	}
	if !strings.Contains(r.Stderr, "No such file") && !strings.Contains(r.Stderr, "no such file") {
		t.Fatalf("Stderr = %q, want file-not-found message", r.Stderr)
	}
}

func TestFoldInvalidWidth(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script string
	}{
		{"non-numeric", "printf 'x' | fold -w abc\n"},
		{"zero", "printf 'x' | fold -w 0\n"},
		{"negative", "printf 'x' | fold -w -1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode == 0 {
				t.Fatalf("ExitCode = 0, want nonzero for invalid width")
			}
			if !strings.Contains(r.Stderr, "invalid") {
				t.Fatalf("Stderr = %q, want error about invalid width", r.Stderr)
			}
		})
	}
}

func TestFoldGroupedObsoleteSyntax(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		{"s-and-width", "printf 'one two' | fold -s4\n", "one \ntwo"},
		{"bs-and-width", "printf '12345678' | fold -bs4\n", "1234\n5678"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFoldNoNewlineAtEnd(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	r := mustExecSession(t, session, "printf '12\\n\\n34' | fold -w2\n")
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got, want := r.Stdout, "12\n\n34"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFoldCharacterModeTab(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, script, want string
	}{
		// Tab in -c mode at col 2 advances to 8, exceeds width 4, so fold.
		{"tab-exceed", "printf 'ab\\tcd\\n' | fold -c -w4\n", "ab\n\t\ncd\n"},
		// Non-ASCII char before tab.
		{"non-ascii-tab", "printf '\\u00e9\\tb\\n' | fold -c -w2\n", "\u00e9\n\t\nb\n"},
		// Multiple tabs.
		{"multi-tab", "printf 'a\\tb\\tc\\n' | fold -c -w10\n", "a\tb\n\tc\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			r := mustExecSession(t, session, tc.script)
			if r.ExitCode != 0 {
				t.Fatalf("ExitCode = %d; stderr=%q", r.ExitCode, r.Stderr)
			}
			if got := r.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}
