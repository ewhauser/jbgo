package runtime

import (
	"strings"
	"testing"
)

func FuzzNumfmtCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("1000\n1K\n2Mi\n"),
		[]byte("header\n1  K\n2  M\n"),
		[]byte("4096\n9001\n-9001\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)

		textData := normalizeFuzzText(rawData)
		delimited := strings.ReplaceAll(strings.TrimSuffix(string(textData), "\n"), "\n", "|") + "\n"
		zeroTerminated := strings.ReplaceAll(strings.TrimSuffix(string(textData), "\n"), "\n", "\x00") + "\x00"

		writeSessionFile(t, session, "/tmp/numfmt-input.txt", textData)
		writeSessionFile(t, session, "/tmp/numfmt-delim.txt", []byte(delimited))
		writeSessionFile(t, session, "/tmp/numfmt-zero.txt", []byte(zeroTerminated))

		script := []byte(
			"cat /tmp/numfmt-input.txt | numfmt --from=auto >/tmp/numfmt-auto.out || true\n" +
				"cat /tmp/numfmt-input.txt | numfmt --from-unit=512 --to=si >/tmp/numfmt-units.out || true\n" +
				"cat /tmp/numfmt-input.txt | numfmt --invalid=warn --format=%06f --padding=8 >/tmp/numfmt-format.out 2>/tmp/numfmt-format.err || true\n" +
				"cat /tmp/numfmt-input.txt | numfmt --round=near --to-unit=1024 >/tmp/numfmt-round.out || true\n" +
				"cat /tmp/numfmt-input.txt | numfmt --debug --header --field=1-2 --suffix=b --unit-separator=' ' --to=si >/tmp/numfmt-fields.out 2>/tmp/numfmt-fields.err || true\n" +
				"cat /tmp/numfmt-delim.txt | numfmt -d '|' --field=- --to=iec-i >/tmp/numfmt-delim.out || true\n" +
				"cat /tmp/numfmt-zero.txt | numfmt -z --from=auto --field=- >/tmp/numfmt-zero.out || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
