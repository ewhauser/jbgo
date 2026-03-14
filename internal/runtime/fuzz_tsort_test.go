package runtime

import "testing"

func FuzzTSortCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("a b\nb c\n"),
		[]byte("a b\nb a\n"),
		[]byte("solo solo\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/tsort-input.txt"
		writeSessionFile(t, session, inputPath, clampFuzzData(rawData))

		script := []byte(
			"tsort " + shellQuote(inputPath) + " >/tmp/tsort-file.txt || true\n" +
				"cat " + shellQuote(inputPath) + " | tsort >/tmp/tsort-stdin.txt || true\n" +
				"printf 'x y\\ny x\\n' | tsort -w >/tmp/tsort-cycle.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
