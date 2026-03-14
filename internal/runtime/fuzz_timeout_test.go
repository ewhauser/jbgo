package runtime

import (
	"strconv"
	"testing"
)

func FuzzTimeoutCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	f.Add("0.01", "0.01")
	f.Add("0.02", "0.005")
	f.Add("0.05", "0.001")

	f.Fuzz(func(t *testing.T, rawKillAfter, rawTimeout string) {
		session := newFuzzSession(t, rt)
		killAfter := sanitizePositiveDurationToken(rawKillAfter, "0.01")
		timeoutValue := sanitizePositiveDurationToken(rawTimeout, "0.01")

		script := []byte(
			"timeout --signal TERM --kill-after " + shellQuote(killAfter) + " " + shellQuote(timeoutValue) + " sleep 1 || true\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}

func sanitizePositiveDurationToken(raw, fallback string) string {
	token := sanitizeFuzzToken(raw)
	if _, err := strconv.ParseFloat(token, 64); err != nil {
		return fallback
	}
	if value, _ := strconv.ParseFloat(token, 64); value <= 0 {
		return fallback
	}
	return token
}
