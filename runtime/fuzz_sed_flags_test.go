package runtime

import "testing"

func FuzzSedFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("foo\nfoo\n"),
		[]byte("alpha\nbeta\n"),
		[]byte("foo bar\nbaz\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		scriptPath := "/tmp/sed-script.sed"
		inputPath := "/tmp/sed-input.txt"
		writeSessionFile(t, session, scriptPath, []byte("s/foo/bar/\n2p\n"))
		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))

		script := []byte(
			"sed -f " + shellQuote(scriptPath) + " " + shellQuote(inputPath) + " >/tmp/sed-out.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
