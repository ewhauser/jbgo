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
			"cut --only-delimited -d: -f2 " + shellQuote(inputPath) + " >/tmp/cut-long.txt || true\n" +
				"cut -b1 " + shellQuote(inputPath) + " >/tmp/cut-bytes.txt || true\n" +
				"cut --complement -c2-4 " + shellQuote(inputPath) + " >/tmp/cut-complement.txt || true\n" +
				"cut -z -d: -f1 " + shellQuote(inputPath) + " >/tmp/cut-zero.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
