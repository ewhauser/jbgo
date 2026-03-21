package interp

import (
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestTimesBuiltinFormatsTwoLines(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("times uses getrusage")
	}

	stdout, stderr, err := runInterpScript(t, "times\n")
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout = %q, want two lines", stdout)
	}
	pattern := regexp.MustCompile(`^[0-9]+m[0-9]+\.[0-9]{3}s [0-9]+m[0-9]+\.[0-9]{3}s$`)
	for _, line := range lines {
		if !pattern.MatchString(line) {
			t.Fatalf("line = %q, want XmY.ZZZs XmY.ZZZs", line)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
