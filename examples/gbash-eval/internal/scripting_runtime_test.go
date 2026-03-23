package gbasheval

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/commands"
)

func TestParseFlagArgsAcceptsExplicitBooleanValue(t *testing.T) {
	t.Parallel()

	params, err := parseFlagArgs([]string{"--dry_run", "false", "--count", "2"}, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dry_run": map[string]any{"type": "boolean"},
			"count":   map[string]any{"type": "integer"},
		},
	})
	if err != nil {
		t.Fatalf("parseFlagArgs() error = %v", err)
	}

	if got := params["dry_run"]; got != false {
		t.Fatalf("dry_run = %#v, want false", got)
	}
	if got := params["count"]; got != int64(2) {
		t.Fatalf("count = %#v, want int64(2)", got)
	}
}

func TestParseFlagArgsRejectsMissingNonBooleanValue(t *testing.T) {
	t.Parallel()

	_, err := parseFlagArgs([]string{"--subject"}, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject": map[string]any{"type": "string"},
		},
	})
	if err == nil {
		t.Fatal("parseFlagArgs() error = nil, want missing value error")
	}
	if !strings.Contains(err.Error(), "missing value for --subject") {
		t.Fatalf("error = %q, want missing value detail", err)
	}
}

func TestDiscoverCommandRejectsMissingSelectorValue(t *testing.T) {
	t.Parallel()

	cmd := newDiscoverCommand([]MockToolDef{{
		Name:        "create_ticket",
		Description: "Create a ticket",
		Category:    "support",
	}}, &scriptedExecutionState{})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	inv := &commands.Invocation{
		Args:   []string{"--category", "--json"},
		Stdout: &stdout,
		Stderr: &stderr,
	}

	err := cmd.Run(context.Background(), inv)
	if err == nil {
		t.Fatal("Run() error = nil, want missing selector value error")
	}
	if code, ok := commands.ExitCode(err); !ok || code != 1 {
		t.Fatalf("ExitCode(err) = (%d, %t), want (1, true)", code, ok)
	}
	if got := stderr.String(); !strings.Contains(got, "missing value for --category") {
		t.Fatalf("stderr = %q, want missing value detail", got)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty output on error", got)
	}
}

func TestDiscoverCommandAcceptsInlineSelectorValue(t *testing.T) {
	t.Parallel()

	cmd := newDiscoverCommand([]MockToolDef{
		{
			Name:        "create_ticket",
			Description: "Create a ticket",
			Category:    "support",
		},
		{
			Name:        "check_inventory",
			Description: "Check inventory",
			Category:    "inventory",
		},
	}, &scriptedExecutionState{})

	var stdout bytes.Buffer
	inv := &commands.Invocation{
		Args:   []string{"--category=support", "--json"},
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
	}

	if err := cmd.Run(context.Background(), inv); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, `"name": "create_ticket"`) || strings.Contains(got, `"name": "check_inventory"`) {
		t.Fatalf("stdout = %q, want only support-category tool", got)
	}
}
