package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunQuietDemo(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := run(context.Background(), strings.NewReader(""), &stdout, &stderr, []string{"--quiet"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty stderr", stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"snapshot create before-cleanup",
		"workspace restore before-cleanup",
		"workspace fork before-cleanup fix-filter",
		"workspace merge fix-filter",
		"fix-filter: kept rows=9",
		"keep-everything: kept rows=12",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q\n%s", needle, output)
		}
	}
}
