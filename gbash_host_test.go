package gbash_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/host"
)

func TestWithHostSystemPropagatesHostMetadata(t *testing.T) {
	t.Parallel()

	rt, err := gbash.New(gbash.WithHost(host.NewSystem()))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &gbash.ExecutionRequest{
		Script: "" +
			"printf '%s\\n' \"$OSTYPE\" \"$(uname -s)\" \"$(uname -m)\" \"$(hostname)\" \"$(arch)\" \"$$\" \"$PPID\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if got, want := len(lines), 7; got != want {
		t.Fatalf("stdout lines = %d, want %d; stdout=%q", got, want, result.Stdout)
	}
	for i := range 5 {
		if strings.TrimSpace(lines[i]) == "" {
			t.Fatalf("stdout line %d is empty; stdout=%q", i, result.Stdout)
		}
	}
	if pid, err := strconv.Atoi(lines[5]); err != nil || pid <= 0 {
		t.Fatalf("pid line = %q, want positive integer; err=%v", lines[5], err)
	}
	if ppid, err := strconv.Atoi(lines[6]); err != nil || ppid < 0 {
		t.Fatalf("ppid line = %q, want non-negative integer; err=%v", lines[6], err)
	}
}
