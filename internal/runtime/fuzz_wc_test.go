package runtime

import (
	"fmt"
	"testing"
)

func FuzzWCCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		data []byte
		alt  []byte
	}{
		{data: []byte("alpha\nbeta\n"), alt: []byte("one line\n")},
		{data: []byte("A界🙂\n"), alt: []byte("=\u00A0=\n")},
		{data: []byte{0x00, 0xff, 'x', '\n'}, alt: []byte("binary\x00tail")},
	}
	for _, seed := range seeds {
		f.Add(seed.data, seed.alt)
	}

	f.Fuzz(func(t *testing.T, rawData, rawAlt []byte) {
		session := newFuzzSession(t, rt)

		inputPath := "/tmp/wc-input.txt"
		altPath := "/tmp/wc-alt.txt"
		namesPath := "/tmp/wc-names.bin"

		writeSessionFile(t, session, inputPath, normalizeFuzzText(rawData))
		writeSessionFile(t, session, altPath, clampFuzzData(rawAlt))
		writeSessionFile(t, session, namesPath, append(
			append(
				append([]byte(inputPath), 0),
				0,
			),
			append([]byte(altPath), 0)...,
		))

		script := fmt.Appendf(nil,
			"wc -m %s >/tmp/wc-chars.txt\n"+
				"wc -L %s >/tmp/wc-width.txt\n"+
				"wc -mL %s >/tmp/wc-combo.txt\n"+
				"wc --total=always %s %s >/tmp/wc-total.txt\n"+
				"wc --files0-from=%s >/tmp/wc-files0.txt 2>/tmp/wc-files0.err || true\n"+
				"printf '%%s\\0\\0%%s\\0-\\0' %s %s | wc --files0-from=- >/tmp/wc-files0-stdin.txt 2>/tmp/wc-files0-stdin.err || true\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(altPath),
			shellQuote(namesPath),
			shellQuote(inputPath),
			shellQuote(altPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
