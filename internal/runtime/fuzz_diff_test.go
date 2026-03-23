package runtime

import (
	"bytes"
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
		{[]byte("tabs\tand  spaces\n\n"), []byte("tabs and spaces\r\n\n")},
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
		leftNoNLPath := "/tmp/left-noeol.txt"
		rightNoNLPath := "/tmp/right-noeol.txt"
		crlfPath := "/tmp/right-crlf.txt"
		dir1Path := "/tmp/dir1/a.txt"
		dir2Path := "/tmp/dir2/a.txt"
		dir2ExtraPath := "/tmp/dir2/extra.txt"

		writeSessionFile(t, session, leftPath, left)
		writeSessionFile(t, session, rightPath, right)
		writeSessionFile(t, session, upperPath, []byte(strings.ToUpper(string(left))))
		writeSessionFile(t, session, leftNoNLPath, bytes.TrimRight(left, "\n"))
		writeSessionFile(t, session, rightNoNLPath, bytes.TrimRight(right, "\n"))
		writeSessionFile(t, session, crlfPath, []byte(strings.ReplaceAll(string(right), "\n", "\r\n")))
		writeSessionFile(t, session, dir1Path, left)
		writeSessionFile(t, session, dir2Path, right)
		writeSessionFile(t, session, dir2ExtraPath, []byte("extra\n"))

		script := fmt.Appendf(nil,
			"diff --unified %s %s >/tmp/diff-unified.txt || true\n"+
				"diff --brief %s %s >/tmp/diff-brief.txt || true\n"+
				"diff --ignore-case %s %s >/tmp/diff-ignore.txt || true\n"+
				"diff --report-identical-files %s %s >/tmp/diff-same.txt || true\n"+
				"diff --context %s %s >/tmp/diff-context.txt || true\n"+
				"diff -e %s %s >/tmp/diff-ed.txt || true\n"+
				"diff -n %s %s >/tmp/diff-rcs.txt || true\n"+
				"diff -D NAME %s %s >/tmp/diff-ifdef.txt || true\n"+
				"diff -y -W 40 %s %s >/tmp/diff-side.txt || true\n"+
				"diff --ignore-space-change --ignore-blank-lines %s %s >/tmp/diff-space.txt || true\n"+
				"diff --strip-trailing-cr %s %s >/tmp/diff-crlf.txt || true\n"+
				"diff --label LEFT --label RIGHT -u %s %s >/tmp/diff-labels.txt || true\n"+
				"diff --old-line-format=-%%L --new-line-format=+%%L --unchanged-line-format= %%L %s %s >/tmp/diff-format.txt || true\n"+
				"diff -r -N /tmp/dir1 /tmp/dir2 >/tmp/diff-recursive.txt || true\n",
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
			shellQuote(leftPath),
			shellQuote(upperPath),
			shellQuote(leftPath),
			shellQuote(leftPath),
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
			shellQuote(leftPath),
			shellQuote(crlfPath),
			shellQuote(leftNoNLPath),
			shellQuote(rightNoNLPath),
			shellQuote(leftPath),
			shellQuote(rightPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
