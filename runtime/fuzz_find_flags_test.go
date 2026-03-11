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
		writeSessionFile(t, session, "/tmp/perm/run.sh", []byte("#!/bin/sh\n"))
		writeSessionFile(t, session, "/tmp/perm/plain.txt", text)
		writeSessionFile(t, session, "/tmp/prune/keep/seen.txt", text)
		writeSessionFile(t, session, "/tmp/prune/skip/hidden.txt", text)
		writeSessionFile(t, session, "/tmp/nul/one", []byte("1"))
		writeSessionFile(t, session, "/tmp/nul/two", []byte("2"))
		writeSessionFile(t, session, "/tmp/printf/file.txt", []byte("data"))
		writeSessionFile(t, session, "/tmp/delete/keep.txt", text)
		writeSessionFile(t, session, "/tmp/delete/remove.tmp", text)
		if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/empty/emptydir", 0o755); err != nil {
			t.Fatalf("MkdirAll(emptydir) error = %v", err)
		}
		if err := session.FileSystem().Chmod(context.Background(), "/tmp/perm/run.sh", 0o755); err != nil {
			t.Fatalf("Chmod(run.sh) error = %v", err)
		}
		if err := session.FileSystem().Chmod(context.Background(), "/tmp/printf/file.txt", 0o755); err != nil {
			t.Fatalf("Chmod(printf/file.txt) error = %v", err)
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
				"find /tmp/size -size 5c >/tmp/find-size-exact.out || true\n" +
				"find /tmp/perm -perm 755 >/tmp/find-perm.out || true\n" +
				"find /tmp/prune -path '/tmp/prune/skip*' -prune -o -print >/tmp/find-prune.out || true\n" +
				"find /tmp/prune -mindepth 1 -maxdepth 1 >/tmp/find-mindepth.out || true\n" +
				"find /tmp/prune -depth -maxdepth 1 >/tmp/find-depth.out || true\n" +
				"find /tmp/find -name '*.txt' -exec echo FILE {} \\; >/tmp/find-exec-each.out || true\n" +
				"find /tmp/find -name '*.txt' -exec echo BATCH {} + >/tmp/find-exec-batch.out || true\n" +
				"find /tmp/nul -mindepth 1 -maxdepth 1 -print0 >/tmp/find-print0.out || true\n" +
				"find /tmp/printf/file.txt -printf '%f|%p|%P|%m\\n' >/tmp/find-printf.out || true\n" +
				"find /tmp/delete -name '*.tmp' -delete || true\n" +
				"find /tmp/delete -print >/tmp/find-delete.out || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
