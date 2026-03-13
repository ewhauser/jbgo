package runtime

import "testing"

func FuzzXArgsFlagsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		shell []byte
		null  []byte
	}{
		{[]byte("one two\nthree\n"), []byte("a\x00b\x00")},
		{[]byte("\"quoted value\"\nplain\n"), []byte("left\x00right\x00")},
		{[]byte("unterminated \"quote\n"), []byte("one\x00two\x00three\x00")},
	}
	for _, seed := range seeds {
		f.Add(seed.shell, seed.null)
	}

	f.Fuzz(func(t *testing.T, shellData, nullData []byte) {
		session := newFuzzSession(t, rt)
		shellPath := "/tmp/xargs-shell.txt"
		nullPath := "/tmp/xargs-null.bin"
		writeSessionFile(t, session, shellPath, clampFuzzData(shellData))
		writeSessionFile(t, session, nullPath, clampFuzzData(nullData))

		script := []byte(
			"cat " + shellQuote(shellPath) + " | xargs -n2 echo >/tmp/xargs-default.txt 2>/tmp/xargs-default.err || true\n" +
				"cat " + shellQuote(shellPath) + " | xargs -E STOP -L1 echo >/tmp/xargs-lines.txt 2>/tmp/xargs-lines.err || true\n" +
				"cat " + shellQuote(nullPath) + " | xargs --null --max-args 1 echo >/tmp/xargs-null.txt 2>/tmp/xargs-null.err || true\n" +
				"printf '' | xargs --show-limits --no-run-if-empty echo skip >/tmp/xargs-limits.txt 2>/tmp/xargs-limits.err || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
