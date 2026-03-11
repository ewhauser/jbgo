package runtime

import (
	"fmt"
	"testing"
)

func FuzzBasenameCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []string{
		"/tmp/example.txt",
		"/home/agent/archive.log",
		"relative/path/report.md",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawPath string) {
		session := newFuzzSession(t, rt)
		target := fuzzPath(rawPath) + ".txt"

		script := []byte(fmt.Sprintf(
			"basename --suffix .txt %s >/tmp/basename.txt\n",
			shellQuote(target),
		))

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}
