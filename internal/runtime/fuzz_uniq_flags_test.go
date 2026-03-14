package runtime

import "testing"

func FuzzUniqFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("Apple\napple\nBanana\n"),
		[]byte("one\none\ntwo\n"),
		{0x00, 'A', '\n', 'a', '\n'},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/uniq-input.txt"
		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))

		script := []byte(
			"uniq --ignore-case " + shellQuote(inputPath) + " >/tmp/uniq-long.txt || true\n" +
				"uniq -i " + shellQuote(inputPath) + " >/tmp/uniq-short.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
