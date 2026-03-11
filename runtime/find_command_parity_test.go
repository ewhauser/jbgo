package runtime

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestFindSupportsCaseInsensitivePathAndRegexFlagsIsolated(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/dir/README.md", []byte("readme"))
	writeSessionFile(t, session, "/dir/readme.txt", []byte("lower"))
	writeSessionFile(t, session, "/dir/sub/other.txt", []byte("nested"))
	writeSessionFile(t, session, "/Project/SRC/file.ts", []byte("upper"))
	writeSessionFile(t, session, "/Project/src/other.ts", []byte("lower"))

	result := mustExecSession(t, session,
		"find /dir -iname \"readme*\"\n"+
			"find /Project -ipath \"*src*\"\n"+
			"find /dir -regex \".*\\\\.txt\"\n"+
			"find /dir -iregex \".*\\\\.TXT\"\n"+
			"find /Project -path \"*/SRC/*\"\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/dir/README.md\n/dir/readme.txt\n/Project/SRC\n/Project/SRC/file.ts\n/Project/src\n/Project/src/other.ts\n/dir/readme.txt\n/dir/sub/other.txt\n/dir/readme.txt\n/dir/sub/other.txt\n/Project/SRC/file.ts\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsEmptyMTimeAndNewerFlagsIsolated(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/empty/empty.txt", nil)
	writeSessionFile(t, session, "/empty/notempty/file.txt", []byte("content"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/empty/emptydir", 0o755); err != nil {
		t.Fatalf("MkdirAll(emptydir) error = %v", err)
	}

	writeSessionFile(t, session, "/mtime/recent.txt", []byte("recent"))
	writeSessionFile(t, session, "/mtime/old.txt", []byte("old"))

	writeSessionFile(t, session, "/newer/ref.txt", []byte("ref"))
	writeSessionFile(t, session, "/newer/newer.txt", []byte("newer"))
	writeSessionFile(t, session, "/newer/older.txt", []byte("older"))

	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)
	ref := now.Add(-time.Minute)
	older := ref.Add(-time.Minute)
	for _, item := range []struct {
		path string
		when time.Time
	}{
		{"/mtime/recent.txt", now},
		{"/mtime/old.txt", old},
		{"/newer/ref.txt", ref},
		{"/newer/newer.txt", now},
		{"/newer/older.txt", older},
	} {
		if err := session.FileSystem().Chtimes(context.Background(), item.path, item.when, item.when); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", item.path, err)
		}
	}

	result := mustExecSession(t, session,
		"find /empty -empty -type f\n"+
			"find /empty -empty -type d\n"+
			"find /mtime -type f -mtime +7\n"+
			"find /mtime -type f -mtime -7\n"+
			"find /mtime -type f -mtime 0\n"+
			"find /newer -type f -newer /newer/ref.txt\n"+
			"find /newer -type f -newer /newer/missing.txt\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/empty/empty.txt\n/empty/emptydir\n/mtime/old.txt\n/mtime/recent.txt\n/mtime/recent.txt\n/newer/newer.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsSizeFlagsIsolated(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/size/large.txt", bytes.Repeat([]byte("x"), 2048))
	writeSessionFile(t, session, "/size/exact.txt", []byte("12345"))
	writeSessionFile(t, session, "/size/small.txt", []byte("tiny"))

	result := mustExecSession(t, session,
		"find /size -type f -size +1k\n"+
			"find /size -type f -size -100c\n"+
			"find /size -type f -size 5c\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/size/large.txt\n/size/exact.txt\n/size/small.txt\n/size/exact.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
