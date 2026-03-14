package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestTSortSupportsStdinInput(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'all install\\ninstall test\\ntest deploy\\n' | tsort\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "all\ninstall\ntest\ndeploy\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTSortSupportsFileInputAndHiddenWarnFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a c\\nb c\\nc d\\n' > /tmp/in.txt\ntsort -w /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\nc\nd\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTSortRejectsExtraOperand(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "tsort /tmp/left /tmp/right\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "extra operand '/tmp/right'") {
		t.Fatalf("Stderr = %q, want extra-operand error", result.Stderr)
	}
}

func TestTSortReportsOddTokenCount(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a b c\\n' | tsort\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "tsort: -: input contains an odd number of tokens") {
		t.Fatalf("Stderr = %q, want odd-token error", result.Stderr)
	}
}

func TestTSortIgnoresSelfLoops(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'solo solo\\n' | tsort\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "solo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTSortReportsCyclesAndReturnsPartialOrder(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a b\\nb c\\nc a\\n' | tsort\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\nc\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"tsort: -: input contains a loop:",
		"tsort: a",
		"tsort: b",
		"tsort: c",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Stderr = %q, want substring %q", result.Stderr, want)
		}
	}
}

func TestTSortReportsDirectoryInput(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir /tmp/input.d\ntsort /tmp/input.d\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "tsort: /tmp/input.d: read error: Is a directory") {
		t.Fatalf("Stderr = %q, want directory error", result.Stderr)
	}
}

func TestTSortSupportsHelpAndVersion(t *testing.T) {
	rt := newRuntime(t, &Config{})

	helpResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "tsort --help\n",
	})
	if err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if helpResult.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", helpResult.ExitCode, helpResult.Stderr)
	}
	if !strings.Contains(helpResult.Stdout, "Usage: tsort [OPTION]... [FILE]") {
		t.Fatalf("help Stdout = %q, want usage", helpResult.Stdout)
	}
	if strings.Contains(helpResult.Stdout, "-w") {
		t.Fatalf("help Stdout = %q, hidden -w flag should not be rendered", helpResult.Stdout)
	}

	versionResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "tsort --version\n",
	})
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if versionResult.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", versionResult.ExitCode, versionResult.Stderr)
	}
	if !strings.Contains(versionResult.Stdout, "tsort (gbash)") {
		t.Fatalf("version Stdout = %q, want version banner", versionResult.Stdout)
	}
}
