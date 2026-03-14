package runtime

import (
	"fmt"
	"testing"
)

func FuzzBase32Command(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("hello\n"),
		[]byte("alpha,beta,gamma\n"),
		{0x00, 0x01, 0x02, 0x03, 0xff},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/base32-input.bin"

		writeSessionFile(t, session, inputPath, clampFuzzData(rawData))

		script := fmt.Appendf(nil,
			"base32 --wrap 0 %s | base32 -d -i >/tmp/base32.out || true\n",
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
