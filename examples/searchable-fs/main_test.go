package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := run(context.Background(), &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	const want = "" +
		"direct search:\n" +
		"  /workspace/docs/readme.txt offsets=[6]\n" +
		"  /workspace/logs/app.log offsets=[4]\n" +
		"adapter used index: true\n" +
		"  /workspace/docs/readme.txt\n" +
		"  /workspace/logs/app.log\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
