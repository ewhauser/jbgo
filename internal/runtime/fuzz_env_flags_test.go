package runtime

import "testing"

func FuzzEnvCommandFlags(f *testing.F) {
	rt := newFuzzRuntime(f)

	f.Add("VALUE")
	f.Add("nested-value")
	f.Add("with spaces")

	f.Fuzz(func(t *testing.T, rawValue string) {
		session := newFuzzSession(t, rt)
		value := sanitizeFuzzToken(rawValue)

		script := []byte(
			"env --ignore-environment ONLY=" + shellQuote(value) + " printenv ONLY >/tmp/env.txt\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
