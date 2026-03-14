package runtime

import (
	"context"
	"testing"
)

func TestMVSupportsParityFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/dst\n" +
			"echo keep > /tmp/dst/src.txt\n" +
			"echo src > /tmp/src.txt\n" +
			"echo move > /tmp/move.txt\n" +
			"echo force > /tmp/force.txt\n" +
			"echo occupied > /tmp/occupied.txt\n" +
			"echo skip > /tmp/skip.txt\n" +
			"mv -n /tmp/src.txt /tmp/dst/src.txt\n" +
			"cat /tmp/dst/src.txt\n" +
			"mv --verbose /tmp/move.txt /tmp/dst\n" +
			"cat /tmp/dst/move.txt\n" +
			"mv -f /tmp/force.txt /tmp/forced.txt\n" +
			"cat /tmp/forced.txt\n" +
			"mv --force --no-clobber /tmp/skip.txt /tmp/occupied.txt\n" +
			"cat /tmp/occupied.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "keep\nrenamed '/tmp/move.txt' -> '/tmp/dst/move.txt'\nmove\nforce\noccupied\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
