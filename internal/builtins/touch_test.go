package builtins_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTouchSupportsAdditionalDateFormats(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, `touch -d 'Thu Jan 01 12:34:00 2015' /home/agent/posix.txt
touch -d @1623786360 /home/agent/epoch.txt
touch -d '1970-01-01 18:43:33.023456789' /home/agent/fraction.txt
`)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	posixInfo, err := session.FileSystem().Stat(context.Background(), "/home/agent/posix.txt")
	if err != nil {
		t.Fatalf("Stat(posix.txt) error = %v", err)
	}
	if got, want := posixInfo.ModTime().UTC(), time.Date(2015, time.January, 1, 12, 34, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("posix.txt ModTime = %v, want %v", got, want)
	}

	epochInfo, err := session.FileSystem().Stat(context.Background(), "/home/agent/epoch.txt")
	if err != nil {
		t.Fatalf("Stat(epoch.txt) error = %v", err)
	}
	if got, want := epochInfo.ModTime().UTC().Unix(), int64(1623786360); got != want {
		t.Fatalf("epoch.txt ModTime unix = %d, want %d", got, want)
	}

	fractionInfo, err := session.FileSystem().Stat(context.Background(), "/home/agent/fraction.txt")
	if err != nil {
		t.Fatalf("Stat(fraction.txt) error = %v", err)
	}
	if got, want := fractionInfo.ModTime().UTC(), time.Unix(18*3600+43*60+33, 23_456_789).UTC(); !got.Equal(want) {
		t.Fatalf("fraction.txt ModTime = %v, want %v", got, want)
	}
}

func TestTouchSupportsAbbreviatedTimeWordsOnCreate(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, `TZ=UTC date --set '2024-05-06 07:08:09' >/dev/null
touch -t 201501011234 --time=atim /home/agent/atim.txt
touch -t 201501011234 --time=a /home/agent/a.txt
touch -t 201501011234 --time=m /home/agent/m.txt
stat -c '%X %Y' /home/agent/atim.txt /home/agent/a.txt /home/agent/m.txt
`)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	got := parseTouchUnixTimePairs(t, result.Stdout)
	if len(got) != 3 {
		t.Fatalf("time pair count = %d, want 3; stdout=%q", len(got), result.Stdout)
	}

	wantExplicit := time.Date(2015, time.January, 1, 12, 34, 0, 0, time.UTC).Unix()

	if got[0][0] != wantExplicit || got[0][1] == wantExplicit {
		t.Fatalf("atim.txt times = %v, want atime %d and a preserved mtime", got[0], wantExplicit)
	}
	if got[1][0] != wantExplicit || got[1][1] == wantExplicit {
		t.Fatalf("a.txt times = %v, want atime %d and a preserved mtime", got[1], wantExplicit)
	}
	if got[2][0] == wantExplicit || got[2][1] != wantExplicit {
		t.Fatalf("m.txt times = %v, want a preserved atime and mtime %d", got[2], wantExplicit)
	}
}

func TestTouchSupportsLegacyPosixTimestampAndLeapSecond(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, `_POSIX2_VERSION=199209 touch 0101000090 /home/agent/posix.txt
touch -t 197001010000.60 /home/agent/leap.txt
`)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	posixInfo, err := session.FileSystem().Stat(context.Background(), "/home/agent/posix.txt")
	if err != nil {
		t.Fatalf("Stat(posix.txt) error = %v", err)
	}
	if got, want := posixInfo.ModTime().UTC().Unix(), time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC).Unix(); got != want {
		t.Fatalf("posix.txt ModTime unix = %d, want %d", got, want)
	}

	leapInfo, err := session.FileSystem().Stat(context.Background(), "/home/agent/leap.txt")
	if err != nil {
		t.Fatalf("Stat(leap.txt) error = %v", err)
	}
	if got, want := leapInfo.ModTime().UTC().Unix(), int64(60); got != want {
		t.Fatalf("leap.txt ModTime unix = %d, want %d", got, want)
	}
}

func TestTouchDashTouchesRedirectedStdout(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/home/agent/out.txt", []byte("keep\n"))
	old := time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
	if err := session.FileSystem().Chtimes(context.Background(), "/home/agent/out.txt", old, old); err != nil {
		t.Fatalf("Chtimes(out.txt) error = %v", err)
	}

	result := mustExecSession(t, session, `TZ=UTC date --set '2024-05-06 07:08:09' >/dev/null
touch - >> /home/agent/out.txt
`)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	info, err := session.FileSystem().Stat(context.Background(), "/home/agent/out.txt")
	if err != nil {
		t.Fatalf("Stat(out.txt) error = %v", err)
	}
	if got, want := info.ModTime().UTC().Unix(), time.Date(2024, time.May, 6, 7, 8, 9, 0, time.UTC).Unix(); got != want {
		t.Fatalf("out.txt ModTime unix = %d, want %d", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/home/agent/out.txt")), "keep\n"; got != want {
		t.Fatalf("out.txt contents = %q, want %q", got, want)
	}
}

func TestTouchDashWithoutRedirectDoesNotCreateStdoutPath(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, `touch -
touch - | cat >/dev/null
`)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if _, err := session.FileSystem().Stat(context.Background(), "/dev/stdout"); !os.IsNotExist(err) {
		t.Fatalf("Stat(/dev/stdout) error = %v, want not exist", err)
	}
}

func parseTouchUnixTimePairs(tb testing.TB, output string) [][2]int64 {
	tb.Helper()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	pairs := make([][2]int64, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			tb.Fatalf("unexpected stat output line %q", line)
		}
		atime, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			tb.Fatalf("ParseInt(%q) error = %v", fields[0], err)
		}
		mtime, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			tb.Fatalf("ParseInt(%q) error = %v", fields[1], err)
		}
		pairs = append(pairs, [2]int64{atime, mtime})
	}
	return pairs
}
