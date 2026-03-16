package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunEmitsTelemetryForSessionFlow(t *testing.T) {
	t.Parallel()
	var telemetry bytes.Buffer
	var status bytes.Buffer

	if err := run(context.Background(), &telemetry, &status); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	output := telemetry.String()
	for _, needle := range []string{
		firstExecName,
		secondExecName,
		"exec.start",
		"exec.finish",
		"file.mutation",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("telemetry missing %q\n%s", needle, output)
		}
	}
	if !strings.Contains(output, "file.access") && !strings.Contains(output, "command.start") {
		t.Fatalf("telemetry missing file.access or command.start\n%s", output)
	}
	if count := strings.Count(output, demoFileContents); count < 2 {
		t.Fatalf("telemetry contains %q %d times, want at least 2\n%s", demoFileContents, count, output)
	}

	statusOutput := status.String()
	for _, needle := range []string{firstExecName, secondExecName} {
		if !strings.Contains(statusOutput, needle) {
			t.Fatalf("status output missing %q\n%s", needle, statusOutput)
		}
	}
}
