package runtime

import "testing"

func FuzzDircolorsCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	f.Add("owt 40;33\n", "screen", "truecolor", uint8(0))
	f.Add("TERM [!a]_negation\n.term_matching 00;38;5;61\n", "b_negation", "", uint8(1))
	f.Add("COLORTERM ?*\nexec 'echo Hello;:'\n", "", "24bit", uint8(2))

	f.Fuzz(func(t *testing.T, rawConfig, rawTerm, rawColorTerm string, mode uint8) {
		session := newFuzzSession(t, rt)
		config := string(normalizeFuzzText([]byte(rawConfig)))
		term := sanitizeFuzzToken(rawTerm)
		colorterm := sanitizeFuzzToken(rawColorTerm)
		if config == "" {
			config = "owt 40;33\n"
		}
		writeSessionFile(t, session, "/tmp/dircolors.conf", []byte(config))

		var script []byte
		switch mode % 4 {
		case 0:
			script = []byte(
				"env SHELL=/bin/bash TERM=" + shellQuote(term) + " COLORTERM=" + shellQuote(colorterm) + " dircolors -b /tmp/dircolors.conf >/tmp/dircolors.out || true\n",
			)
		case 1:
			script = []byte(
				"cat /tmp/dircolors.conf | env TERM=" + shellQuote(term) + " COLORTERM=" + shellQuote(colorterm) + " dircolors -c - >/tmp/dircolors.out || true\n",
			)
		case 2:
			script = []byte(
				"env TERM=" + shellQuote(term) + " COLORTERM=" + shellQuote(colorterm) + " dircolors --print-ls-colors >/tmp/dircolors-display.out || true\n",
			)
		default:
			script = []byte(
				"dircolors --print-database >/tmp/dircolors-db.out\n",
			)
		}

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}
