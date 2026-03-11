package runtime

import "testing"

func FuzzTRFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("aaabbbccc"),
		[]byte("abc123"),
		{0x00, 0x01, 'a', 'a', 'b', 'b'},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/tr-input.txt"
		writeSessionFile(t, session, inputPath, clampFuzzData(rawData))

		script := []byte(
			"cat " + shellQuote(inputPath) + " | tr --squeeze-repeats abc >/tmp/tr-squeeze.txt || true\n" +
				"cat " + shellQuote(inputPath) + " | tr --delete '[:digit:]' >/tmp/tr-delete.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
