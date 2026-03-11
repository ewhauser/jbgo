package runtime

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func FuzzFindFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("readme\n"),
		[]byte("alpha\nbeta\n"),
		[]byte("12345"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		text := normalizeFuzzText(rawData)

		writeSessionFile(t, session, "/tmp/find/README.md", text)
		writeSessionFile(t, session, "/tmp/find/readme.txt", text)
		writeSessionFile(t, session, "/tmp/find/sub/other.txt", text)
		writeSessionFile(t, session, "/tmp/Project/SRC/file.ts", text)
		writeSessionFile(t, session, "/tmp/Project/src/other.ts", text)
		writeSessionFile(t, session, "/tmp/empty/empty.txt", nil)
		writeSessionFile(t, session, "/tmp/empty/notempty/file.txt", text)
		writeSessionFile(t, session, "/tmp/mtime/recent.txt", text)
		writeSessionFile(t, session, "/tmp/mtime/old.txt", text)
		writeSessionFile(t, session, "/tmp/newer/ref.txt", text)
		writeSessionFile(t, session, "/tmp/newer/newer.txt", text)
		writeSessionFile(t, session, "/tmp/newer/older.txt", text)
		writeSessionFile(t, session, "/tmp/size/large.txt", bytes.Repeat([]byte("x"), 2048))
		writeSessionFile(t, session, "/tmp/size/exact.txt", []byte("12345"))
		writeSessionFile(t, session, "/tmp/size/small.txt", []byte("tiny"))
		if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/empty/emptydir", 0o755); err != nil {
			t.Fatalf("MkdirAll(emptydir) error = %v", err)
		}

		now := time.Now().UTC()
		old := now.Add(-10 * 24 * time.Hour)
		ref := now.Add(-time.Minute)
		older := ref.Add(-time.Minute)
		for _, item := range []struct {
			path string
			when time.Time
		}{
			{"/tmp/mtime/recent.txt", now},
			{"/tmp/mtime/old.txt", old},
			{"/tmp/newer/ref.txt", ref},
			{"/tmp/newer/newer.txt", now},
			{"/tmp/newer/older.txt", older},
		} {
			if err := session.FileSystem().Chtimes(context.Background(), item.path, item.when, item.when); err != nil {
				t.Fatalf("Chtimes(%q) error = %v", item.path, err)
			}
		}

		script := []byte(
			"find /tmp/find -iname 'readme*' >/tmp/find-iname.out || true\n" +
				"find /tmp/Project -ipath '*src*' >/tmp/find-ipath.out || true\n" +
				"find /tmp/find -regex '.*\\.txt' >/tmp/find-regex.out || true\n" +
				"find /tmp/find -iregex '.*\\.TXT' >/tmp/find-iregex.out || true\n" +
				"find /tmp/Project -path '*/SRC/*' >/tmp/find-path.out || true\n" +
				"find /tmp/empty -empty >/tmp/find-empty.out || true\n" +
				"find /tmp/mtime -mtime +7 >/tmp/find-mtime-old.out || true\n" +
				"find /tmp/mtime -mtime -7 >/tmp/find-mtime-new.out || true\n" +
				"find /tmp/newer -newer /tmp/newer/ref.txt >/tmp/find-newer.out || true\n" +
				"find /tmp/size -size +1k >/tmp/find-size-large.out || true\n" +
				"find /tmp/size -size 5c >/tmp/find-size-exact.out || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
