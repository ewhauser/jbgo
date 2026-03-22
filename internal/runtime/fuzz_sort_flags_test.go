package runtime

import "testing"

func FuzzSortFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("alpha\nbeta\n"),
		[]byte("  zebra\n alpha\n"),
		[]byte("v1.10\nv1.2\nv1.1\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/sort-input.txt"
		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))
		writeSessionFile(t, session, "/tmp/sort-random-seed.txt", []byte("0123456789abcdef"))
		writeSessionFile(t, session, "/tmp/sort-months.txt", []byte("Feb\nJan\nDec\n"))
		writeSessionFile(t, session, "/tmp/sort-human.txt", []byte("2K\n100\n1M\n"))
		writeSessionFile(t, session, "/tmp/sort-version.txt", []byte("v1.10\nv1.2\nv1.1\n"))
		writeSessionFile(t, session, "/tmp/sort-key.csv", []byte("zebra,10\nalpha,2\nbeta,1\n"))
		writeSessionFile(t, session, "/tmp/sort-check.txt", []byte("alpha\nbeta\n"))
		writeSessionFile(t, session, "/tmp/sort-merge-a.txt", []byte("1\n3\n"))
		writeSessionFile(t, session, "/tmp/sort-merge-b.txt", []byte("2\n4\n"))

		script := []byte(
			"sort --ignore-leading-blanks --dictionary-order " + shellQuote(inputPath) + " >/tmp/sort-blank-dict.txt || true\n" +
				"sort -h /tmp/sort-human.txt >/tmp/sort-human.out || true\n" +
				"sort -M /tmp/sort-months.txt >/tmp/sort-month.out || true\n" +
				"sort -V /tmp/sort-version.txt >/tmp/sort-version.out || true\n" +
				"sort --field-separator=, --key=2,2n /tmp/sort-key.csv >/tmp/sort-key.out || true\n" +
				"sort -R --random-source=/tmp/sort-random-seed.txt " + shellQuote(inputPath) + " >/tmp/sort-random.out || true\n" +
				"sort -m /tmp/sort-merge-a.txt /tmp/sort-merge-b.txt >/tmp/sort-merge.out || true\n" +
				"sort --debug --field-separator=, --key=2,2n /tmp/sort-key.csv >/tmp/sort-debug.out || true\n" +
				"sort --compress-program=does-not-exist " + shellQuote(inputPath) + " >/tmp/sort-compress.out || true\n" +
				"sort --check /tmp/sort-check.txt >/tmp/sort-check.out || true\n" +
				"sort -s -o /tmp/sort-out.txt " + shellQuote(inputPath) + " || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
