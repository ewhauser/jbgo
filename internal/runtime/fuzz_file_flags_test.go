package runtime

import (
	"fmt"
	"testing"
)

func FuzzFileCommandFlags(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("hello\n"),
		[]byte("#!/bin/sh\necho hi\n"),
		{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/file-input.bin"

		writeSessionFile(t, session, inputPath, clampFuzzData(rawData))

		script := fmt.Appendf(nil,
			"file --brief %s >/tmp/file-brief.txt\nfile --mime %s >/tmp/file-mime.txt\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
