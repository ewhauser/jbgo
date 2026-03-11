package runtime

import (
	"fmt"
	"strings"
	"testing"
)

func FuzzDiffCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		left  []byte
		right []byte
	}{
		{[]byte("alpha\nbeta\n"), []byte("alpha\ngamma\n")},
		{[]byte("One\nTwo\n"), []byte("one\ntwo\n")},
		{[]byte("same\n"), []byte("same\n")},
	}
	for _, seed := range seeds {
		f.Add(seed.left, seed.right)
	}

	f.Fuzz(func(t *testing.T, rawLeft, rawRight []byte) {
		session := newFuzzSession(t, rt)
		left := normalizeFuzzText(rawLeft)
		right := normalizeFuzzText(rawRight)
		leftPath := "/tmp/left.txt"
		rightPath := "/tmp/right.txt"
		upperPath := "/tmp/right-upper.txt"

		writeSessionFile(t, session, leftPath, left)
		writeSessionFile(t, session, rightPath, right)
		writeSessionFile(t, session, upperPath, []byte(strings.ToUpper(string(left))))

		script := []byte(fmt.Sprintf(
			"diff --unified %s %s >/tmp/diff-unified.txt || true\n"+
				"diff --brief %s %s >/tmp/diff-brief.txt || true\n"+
				"diff --ignore-case %s %s >/tmp/diff-ignore.txt || true\n"+
				"diff --report-identical-files %s %s >/tmp/diff-same.txt || true\n",
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(upperPath),
			shellQuote(leftPath),
			shellQuote(leftPath),
		))

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
