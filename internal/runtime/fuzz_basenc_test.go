package runtime

import (
	"fmt"
	"testing"
)

func FuzzBasencCommand(f *testing.F) {
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
		inputPath := "/tmp/basenc-input.bin"

		writeSessionFile(t, session, inputPath, clampFuzzData(rawData))

		script := fmt.Appendf(nil,
			"basenc --base64 --wrap=0 %s | basenc --base64 -d >/tmp/basenc-base64.out || true\n"+
				"basenc --base32 --wrap=0 %s | basenc --base32 -d >/tmp/basenc-base32.out || true\n"+
				"basenc --base16 --wrap=0 %s | basenc --base16 -d >/tmp/basenc-base16.out || true\n"+
				"basenc --base2msbf --wrap=0 %s | basenc --base2msbf -d >/tmp/basenc-base2.out || true\n"+
				"basenc --base58 --wrap=0 %s | basenc --base58 -d >/tmp/basenc-base58.out || true\n"+
				"basenc --z85 --wrap=0 %s | basenc --z85 -d >/tmp/basenc-z85.out || true\n",
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
