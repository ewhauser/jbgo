package builtins

import (
	"bytes"
	"testing"
)

func parseChgrpSpec(t *testing.T, args ...string) (parsed *ParsedCommand, action string, err error) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	inv := &Invocation{
		Args:   args,
		Stdout: &stdout,
		Stderr: &stderr,
	}
	spec := NewChgrp().Spec()
	return ParseCommandSpec(inv, &spec)
}

func TestParseChgrpSpecInfersLongOptionsAfterPositionals(t *testing.T) {
	matches, action, err := parseChgrpSpec(t, "--verb", "target.txt", "--ref=ref.txt")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("verbose") {
		t.Fatalf("verbose option not parsed: %#v", matches.OptionOrder())
	}
	if got, want := matches.Value("reference"), "ref.txt"; got != want {
		t.Fatalf("reference = %q, want %q", got, want)
	}
	if got, want := matches.Positionals(), []string{"target.txt"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("positionals = %v, want %v", got, want)
	}
}

func TestParseChgrpSpecTreatsShortHAsNoDereference(t *testing.T) {
	matches, action, err := parseChgrpSpec(t, "-h", "group", "file")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if matches.Has("help") {
		t.Fatalf("help option unexpectedly parsed")
	}
	if !matches.Has("no-dereference") {
		t.Fatalf("no-dereference option not parsed")
	}
}

func TestParseChgrpSpecParsesGroupedShortTraversalFlags(t *testing.T) {
	matches, action, err := parseChgrpSpec(t, "-HR", "group", "file")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("traverse-first") {
		t.Fatalf("traverse-first option not parsed")
	}
	if !matches.Has("recursive") {
		t.Fatalf("recursive option not parsed")
	}
}

func TestParseChgrpSpecInfersQuietAlias(t *testing.T) {
	matches, action, err := parseChgrpSpec(t, "--qui", "group", "file")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("quiet") {
		t.Fatalf("quiet option not parsed")
	}
}

func TestParseChgrpSpecParsesManualHelpAndAutoVersion(t *testing.T) {
	helpMatches, action, err := parseChgrpSpec(t, "--help")
	if err != nil {
		t.Fatalf("ParseCommandSpec(--help) error = %v", err)
	}
	if action != "" {
		t.Fatalf("help action = %q, want empty for manual help handling", action)
	}
	if !helpMatches.Has("help") {
		t.Fatalf("help option not parsed")
	}

	_, action, err = parseChgrpSpec(t, "--vers")
	if err != nil {
		t.Fatalf("ParseCommandSpec(--vers) error = %v", err)
	}
	if got, want := action, "version"; got != want {
		t.Fatalf("version action = %q, want %q", got, want)
	}
}
