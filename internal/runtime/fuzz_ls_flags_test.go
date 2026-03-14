package runtime

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func FuzzLSFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("alpha\nbeta\n"),
		[]byte("hidden\nvisible\n"),
		[]byte("12345"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		text := normalizeFuzzText(rawData)

		writeSessionFile(t, session, "/tmp/ls/.hidden.txt", text)
		writeSessionFile(t, session, "/tmp/ls/plain.txt", text)
		writeSessionFile(t, session, "/tmp/ls/run.sh", []byte("#!/bin/sh\n"))
		writeSessionFile(t, session, "/tmp/ls/large.bin", bytes.Repeat([]byte("x"), 2048))
		writeSessionFile(t, session, "/tmp/ls/sub/nested.txt", text)
		writeSessionFile(t, session, "/tmp/ls/newer.txt", text)
		writeSessionFile(t, session, "/tmp/ls/older.txt", text)
		if err := session.FileSystem().Chmod(context.Background(), "/tmp/ls/run.sh", 0o755); err != nil {
			t.Fatalf("Chmod(run.sh) error = %v", err)
		}

		now := time.Now().UTC()
		old := now.Add(-2 * time.Hour)
		for _, item := range []struct {
			path string
			when time.Time
		}{
			{"/tmp/ls/newer.txt", now},
			{"/tmp/ls/older.txt", old},
		} {
			if err := session.FileSystem().Chtimes(context.Background(), item.path, item.when, item.when); err != nil {
				t.Fatalf("Chtimes(%q) error = %v", item.path, err)
			}
		}

		script := []byte(
			"ls --all /tmp/ls >/tmp/ls-all.out\n" +
				"ls --almost-all /tmp/ls >/tmp/ls-almost-all.out\n" +
				"ls --classify /tmp/ls >/tmp/ls-classify.out\n" +
				"ls --directory /tmp/ls >/tmp/ls-directory.out\n" +
				"ls --human-readable -l /tmp/ls/large.bin >/tmp/ls-human.out\n" +
				"ls --recursive /tmp/ls >/tmp/ls-recursive.out\n" +
				"ls --reverse /tmp/ls >/tmp/ls-reverse.out\n" +
				"ls -1 /tmp/ls >/tmp/ls-one.out\n" +
				"ls -A /tmp/ls >/tmp/ls-A.out\n" +
				"ls -F /tmp/ls >/tmp/ls-F.out\n" +
				"ls -R /tmp/ls >/tmp/ls-R.out\n" +
				"ls -S /tmp/ls >/tmp/ls-S.out\n" +
				"ls -a /tmp/ls >/tmp/ls-a.out\n" +
				"ls -d /tmp/ls >/tmp/ls-d.out\n" +
				"ls -h -l /tmp/ls/large.bin >/tmp/ls-hl.out\n" +
				"ls -l /tmp/ls >/tmp/ls-l.out\n" +
				"ls -r /tmp/ls >/tmp/ls-r.out\n" +
				"ls -t /tmp/ls >/tmp/ls-t.out\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
