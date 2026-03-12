package runtime

import "testing"

func FuzzPasteFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("one\ntwo\n"),
		[]byte("left\nright\n"),
		[]byte("a\nb\nc\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/paste-input.txt"
		otherPath := "/tmp/paste-other.txt"
		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))
		writeSessionFile(t, session, otherPath, []byte("X\nY\n"))

		script := []byte(
			"paste --serial --delimiters=, " + shellQuote(inputPath) + " >/tmp/paste-serial.txt || true\n" +
				"paste --delimiters=: " + shellQuote(inputPath) + " " + shellQuote(otherPath) + " >/tmp/paste-parallel.txt || true\n" +
				"paste -z -d '\\0,' " + shellQuote(inputPath) + " " + shellQuote(otherPath) + " >/tmp/paste-zero.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
