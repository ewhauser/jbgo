package runtime

import "testing"

func FuzzGrepFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("alpha\nbeta\nbeta\ngamma\n"),
		[]byte("a.c\naxc\n"),
		[]byte("item1\nitem22\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/grep-input.txt"
		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))
		writeSessionFile(t, session, "/tmp/grep-fixed.txt", []byte("a.c\naxc\n"))
		writeSessionFile(t, session, "/tmp/grep-line.txt", []byte("foo\nfoobar\nfoo\n"))
		writeSessionFile(t, session, "/tmp/grep-perl.txt", []byte("item1\nitem22\n"))
		writeSessionFile(t, session, "/tmp/grep-context.txt", []byte("line1\nmatch\nline3\nline4\n"))
		writeSessionFile(t, session, "/tmp/grep-hit.txt", []byte("hit\nhit\nmiss\n"))
		writeSessionFile(t, session, "/tmp/grep-miss.txt", []byte("miss\n"))
		writeSessionFile(t, session, "/tmp/grep-a.txt", []byte("match\n"))
		writeSessionFile(t, session, "/tmp/grep-b.txt", []byte("match\n"))

		script := []byte(
			"grep --fixed-strings 'a.c' /tmp/grep-fixed.txt >/tmp/grep-fixed.out || true\n" +
				"grep -x foo /tmp/grep-line.txt >/tmp/grep-line.out || true\n" +
				"grep --only-matching match /tmp/grep-a.txt >/tmp/grep-only.out || true\n" +
				"grep -oP '[0-9]+' /tmp/grep-perl.txt >/tmp/grep-perl.out || true\n" +
				"grep -A1 match /tmp/grep-context.txt >/tmp/grep-a-context.out || true\n" +
				"grep -B1 match /tmp/grep-context.txt >/tmp/grep-b-context.out || true\n" +
				"grep -C1 match /tmp/grep-context.txt >/tmp/grep-c-context.out || true\n" +
				"grep --files-without-match hit /tmp/grep-hit.txt /tmp/grep-miss.txt >/tmp/grep-list.out || true\n" +
				"grep --max-count=1 hit /tmp/grep-hit.txt >/tmp/grep-max.out || true\n" +
				"grep --quiet hit /tmp/grep-hit.txt >/tmp/grep-quiet.out || true\n" +
				"grep -h match /tmp/grep-a.txt /tmp/grep-b.txt >/tmp/grep-h.out || true\n" +
				"grep -n beta " + shellQuote(inputPath) + " >/tmp/grep-input.out || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
