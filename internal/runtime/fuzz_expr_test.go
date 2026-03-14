package runtime

import (
	"fmt"
	"testing"
)

func FuzzExprCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	f.Add("12", "*", "4")
	f.Add("./tests/init.sh", ":", ".*/\\(.*\\)$")
	f.Add("1", "<", "2")

	f.Fuzz(func(t *testing.T, left, op, right string) {
		session := newFuzzSession(t, rt)
		script := fmt.Appendf(nil,
			"expr %s %s %s >/tmp/expr.out 2>/tmp/expr.err || true\n",
			shellQuote(sanitizeFuzzToken(left)),
			shellQuote(sanitizeFuzzToken(op)),
			shellQuote(sanitizeFuzzToken(right)),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
