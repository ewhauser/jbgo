package runtime

import (
	"fmt"
	"testing"
)

func FuzzFoldCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("hello world\n"),
		[]byte("one two three four five six\n"),
		[]byte{0x00, 0x01, 0x02, 0xff},
		[]byte("a\tb\tc\n"),
		[]byte("\u00e9\u00e9\u00e9\n"),
		[]byte("\uff1a\uff1a\uff1a\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		data := clampFuzzData(rawData)
		inputPath := "/tmp/input.txt"

		writeSessionFile(t, session, inputPath, data)

		script := fmt.Appendf(nil,
			"fold %s > /tmp/fold-default.txt\n"+
				"fold -w 20 %s > /tmp/fold-w20.txt\n"+
				"fold -b %s > /tmp/fold-bytes.txt\n"+
				"fold -c %s > /tmp/fold-chars.txt\n"+
				"fold -s -w 20 %s > /tmp/fold-spaces.txt\n"+
				"fold -b -s -w 10 %s > /tmp/fold-bs.txt\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
