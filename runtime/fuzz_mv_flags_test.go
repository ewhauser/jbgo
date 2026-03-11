package runtime

import "testing"

func FuzzMVFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("move this\n"),
		[]byte("rename me\n"),
		[]byte("force accepted\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		payload := normalizeFuzzText(rawData)
		srcPath := "/tmp/mv-src.txt"
		movePath := "/tmp/mv-move.txt"
		forcePath := "/tmp/mv-force.txt"
		destDir := "/tmp/mv-dst"
		destPath := destDir + "/mv-src.txt"
		occupiedPath := "/tmp/mv-occupied.txt"
		forcedPath := "/tmp/mv-forced.txt"
		skipSourcePath := "/tmp/mv-skip-source.txt"

		writeSessionFile(t, session, srcPath, payload)
		writeSessionFile(t, session, movePath, payload)
		writeSessionFile(t, session, forcePath, payload)
		writeSessionFile(t, session, destPath, []byte("keep\n"))
		writeSessionFile(t, session, occupiedPath, []byte("keep\n"))
		writeSessionFile(t, session, skipSourcePath, payload)

		script := []byte(
			"mv -n " + shellQuote(srcPath) + " " + shellQuote(destPath) + " >/tmp/mv-skip.txt || true\n" +
				"mv --verbose " + shellQuote(movePath) + " " + shellQuote(destDir) + " >/tmp/mv-verbose.txt || true\n" +
				"mv -f " + shellQuote(forcePath) + " " + shellQuote(forcedPath) + " >/tmp/mv-force.txt || true\n" +
				"mv --force --no-clobber " + shellQuote(skipSourcePath) + " " + shellQuote(occupiedPath) + " >/tmp/mv-long.txt || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
