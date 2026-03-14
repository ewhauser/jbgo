package runtime

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

type endlessClosableReader struct {
	closed atomic.Bool
}

func (r *endlessClosableReader) Read(p []byte) (int, error) {
	if r.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}
	for i := range p {
		if i%2 == 0 {
			p[i] = 'y'
		} else {
			p[i] = '\n'
		}
	}
	return len(p), nil
}

func (r *endlessClosableReader) Close() error {
	r.closed.Store(true)
	return nil
}

func TestTimeoutCancelsBlockedSplitStdinRead(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout 0.02 split --filter='head -c1 >/dev/null' -n r/1 - || echo timed\n",
		Stdin:  &endlessClosableReader{},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "timed\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout marker", result.Stderr)
	}
}
