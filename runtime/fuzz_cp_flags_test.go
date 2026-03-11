package runtime

import "testing"

func FuzzCPFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("alpha\nbeta\n"),
		[]byte("copy me\n"),
		[]byte("preserve and verbose\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		srcPath := "/tmp/cp-src.txt"
		dstPath := "/tmp/cp-dst.txt"
		freshPath := "/tmp/cp-fresh.txt"
		writeSessionFile(t, session, srcPath, normalizeFuzzText(rawData))
		writeSessionFile(t, session, dstPath, []byte("keep\n"))

		script := []byte(
			"cp --no-clobber --preserve --verbose " + shellQuote(srcPath) + " " + shellQuote(dstPath) + " >/tmp/cp-skip.txt || true\n" +
				"cp -p -v " + shellQuote(srcPath) + " " + shellQuote(freshPath) + " >/tmp/cp-copy.txt || true\n" +
				"cat " + shellQuote(freshPath) + " >/tmp/cp-out.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
