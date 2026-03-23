package runtime

import (
	"fmt"
	"testing"
)

func FuzzDdCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("alpha\nbeta\ngamma\n"),
		[]byte("[]~\n"),
		{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		data := clampFuzzData(rawData)
		inputPath := "/tmp/dd-input.bin"

		writeSessionFile(t, session, inputPath, data)

		script := fmt.Appendf(nil,
			"dd if=%s of=/tmp/dd-copy.bin bs=1 count=64 status=none || true\n"+
				"dd if=%s of=/tmp/dd-skip.bin skip=1B count=64B status=none || true\n"+
				"dd if=%s of=/tmp/dd-append.bin bs=1 count=32 seek=1 oflag=append conv=notrunc status=none || true\n"+
				"dd if=%s of=/tmp/dd-swab.bin conv=swab status=none || true\n"+
				"dd if=%s of=/tmp/dd-block.bin conv=block cbs=4 status=none || true\n"+
				"dd if=%s of=/tmp/dd-unblock.bin conv=unblock cbs=4 status=none || true\n"+
				"dd if=%s of=/tmp/dd-ebcdic.bin conv=ebcdic status=none || true\n"+
				"dd if=/tmp/dd-ebcdic.bin of=/tmp/dd-ascii.bin conv=ascii status=none || true\n"+
				"dd if=%s of=/tmp/dd-noxfer.bin count=2 status=noxfer || true\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
