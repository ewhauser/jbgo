package builtins_test

import (
	"strings"
	"testing"
)

func TestODHexByteDumpMatchesEchoHelperShape(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.bin", []byte{0x07, 0x08, 0x1b, 0x0c, 0x0a, 0x0d, 0x09, 0x0b})

	result := mustExecSession(t, session, "od -tx1 /tmp/in.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0000000 07 08 1b 0c 0a 0d 09 0b\n0000010\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODSupportsSkipReadAndNoAddress(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.bin", []byte{0x01, 0x02, 0x03, 0x04})

	result := mustExecSession(t, session, "od -An -j1 -N2 -tx1 /tmp/in.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 02 03\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODAlignsMultipleFormatsWithoutAddressPadding(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.bin", []byte{'a', 0x03, 'b', 0x04, 'c', '\n'})

	result := mustExecSession(t, session, "od -A n -t c -t x1 /tmp/in.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "   a 003   b 004   c  \\n\n  61  03  62  04  63  0a\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODSuppressesDuplicateLinesUnlessVIsSet(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.bin", []byte("ABCDEFGHABCDEFGHijklmnop"))

	result := mustExecSession(t, session, "od -An -w8 -tx1 /tmp/in.bin\nprintf '%s\\n' ---\nod -An -v -w8 -tx1 /tmp/in.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 41 42 43 44 45 46 47 48\n*\n 69 6a 6b 6c 6d 6e 6f 70\n---\n 41 42 43 44 45 46 47 48\n 41 42 43 44 45 46 47 48\n 69 6a 6b 6c 6d 6e 6f 70\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODSupportsEndianWordFormatting(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.bin", []byte{0x01, 0x02, 0x03, 0x04})

	result := mustExecSession(t, session, "od -An --endian=big -tx2 /tmp/in.bin\nprintf '%s\\n' ---\nod -An --endian=little -tx2 /tmp/in.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 0102 0304\n---\n 0201 0403\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODRespectsReadLimitOnSharedStdin(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("abcdefg\n"))

	result := mustExecSession(t, session, "(od -An -N3 -c; od -An -N3 -c) < /tmp/in.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "   a   b   c\n   d   e   f\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODAcceptsInferredEndianLongOption(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.bin", []byte{0x01, 0x02, 0x03, 0x04})

	result := mustExecSession(t, session, "od -An -tx2 --end=big /tmp/in.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 0102 0304\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODPreservesEchoConformanceFormattingOnPartialLines(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, strings.Join([]string{
		"echo -en '\\03777' | od -A n -t x1 | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"echo -en '\\04000' | od -A n -t x1 | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"flags='-en'",
		"echo $flags '\\0777' | od -A n -t x1 | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"echo -en 'abcd\\x6' | od -A n -c | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"echo -e '\\x' '\\xg' | od -A n -c | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"flags='-en'",
		"echo $flags 'abcd\\04' | od -A n -c | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"echo -en 'abcd\\u006' | od -A n -c | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"flags='-en'",
		"echo $flags '\\u6' | od -A n -c | sed 's/ \\+/ /g'",
		"printf '%s\\n' '---'",
		"flags='-en'",
		"echo $flags '\\0' '\\1' '\\8' | od -A n -c | sed 's/ \\+/ /g'",
		"",
	}, "\n"))
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, strings.Join([]string{
		" ff 37",
		"---",
		" 00 30",
		"---",
		" ff",
		"---",
		" a b c d 006",
		"---",
		" \\ x \\ x g \\n",
		"---",
		" a b c d 004",
		"---",
		" a b c d 006",
		"---",
		" 006",
		"---",
		" \\0 \\ 1 \\ 8",
		"",
	}, "\n"); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestODRejectsUnsupportedIntegerTypeSizes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf '' | od -An -tx16\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if got, want := result.Stderr, "od: invalid type size 16 in \"x16\"\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestODReportsMissingOffsetLikeFilename(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "od ++0\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if got, want := result.Stderr, "od: ++0: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestODWithNoAddressProducesNoOutputForEmptyInput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf '' | od -An -tx1\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
}
