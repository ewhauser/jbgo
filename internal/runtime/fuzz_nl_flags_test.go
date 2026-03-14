package runtime

import "testing"

func FuzzNLFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("one\ntwo\n"),
		[]byte("left\n\nright\n"),
		[]byte("123\n456\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/nl-input.txt"
		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))

		script := []byte(
			"nl -ba -n rz -w 3 " + shellQuote(inputPath) + " >/tmp/nl-rz.txt || true\n" +
				"nl -ba -n ln -w 3 -s : " + shellQuote(inputPath) + " >/tmp/nl-ln.txt || true\n" +
				"nl -ha -fa -d x " + shellQuote(inputPath) + " >/tmp/nl-sections.txt || true\n" +
				"nl -p -l 2 -i -10 " + shellQuote(inputPath) + " >/tmp/nl-neg.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
