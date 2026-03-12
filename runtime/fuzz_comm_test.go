package runtime

import (
	"fmt"
	"testing"
)

func FuzzCommCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	f.Add([]byte("apple\nbanana\n"), []byte("banana\ncarrot\n"))
	f.Add([]byte("a\nb\nc\n"), []byte("b\nc\nd\n"))
	f.Add([]byte("same\n"), []byte("same\n"))

	f.Fuzz(func(t *testing.T, rawLeft, rawRight []byte) {
		session := newFuzzSession(t, rt)
		leftPath := "/tmp/comm-left.txt"
		rightPath := "/tmp/comm-right.txt"

		writeSessionFile(t, session, leftPath, normalizeFuzzText(rawLeft))
		writeSessionFile(t, session, rightPath, normalizeFuzzText(rawRight))

		script := fmt.Appendf(nil,
			"comm -1 %s %s >/tmp/comm-1.txt || true\n"+
				"comm -2 %s %s >/tmp/comm-2.txt || true\n"+
				"comm -3 %s %s >/tmp/comm-3.txt || true\n"+
				"comm --total --output-delimiter=, %s %s >/tmp/comm-total.txt || true\n"+
				"comm --nocheck-order %s %s >/tmp/comm-nocheck.txt || true\n"+
				"comm --check-order %s %s >/tmp/comm-check.txt || true\n",
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
