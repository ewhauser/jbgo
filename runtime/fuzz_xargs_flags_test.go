package runtime

import "testing"

func FuzzXArgsFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("a\x00b\x00"),
		[]byte("left\x00right\x00"),
		[]byte("one\x00two\x00three\x00"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/xargs-null.bin"
		writeSessionFile(t, session, inputPath, clampFuzzData(rawData))

		script := []byte(
			"cat " + shellQuote(inputPath) + " | xargs --null --verbose --max-args 1 echo >/tmp/xargs-out.txt 2>/tmp/xargs-err.txt || true\n" +
				"printf '' | xargs --no-run-if-empty echo skip >/tmp/xargs-empty.txt 2>/tmp/xargs-empty.err || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
