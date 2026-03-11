package runtime

import "testing"

func FuzzCutFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("left:right\nplain\n"),
		[]byte("a:b:c\n"),
		[]byte("no-delimiter\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/cut-input.txt"
		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))

		script := []byte(
			"cut --only-delimited -d: -f2 " + shellQuote(inputPath) + " >/tmp/cut-long.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
