package main

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/contrib/bashtool"
)

func TestSeedLabCreatesFixturesAndExtrasRegistryTools(t *testing.T) {
	t.Parallel()

	registry := newBashRegistry()
	tool, err := newPersistentBashTool(context.Background(), registry)
	if err != nil {
		t.Fatalf("newPersistentBashTool() error = %v", err)
	}

	first := tool.runScript(context.Background(), bashtool.Request{
		Commands: "test -f /home/agent/lab/README.md && test -f /home/agent/lab/incidents.db && sqlite3 /home/agent/lab/incidents.db 'select count(*) from incidents;' && printf 'a,b\\n' | awk -F, 'NR==1 {print $2}' && printf '{\"name\":\"alice\"}\\n' | jq -r '.name'",
	})
	if first.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", first.ExitCode, first.Stderr)
	}
	if got, want := strings.TrimSpace(first.Stdout), "4\nb\nalice"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestPersistentBashToolCarriesWorkDirAndEnv(t *testing.T) {
	t.Parallel()

	registry := newBashRegistry()
	tool, err := newPersistentBashTool(context.Background(), registry)
	if err != nil {
		t.Fatalf("newPersistentBashTool() error = %v", err)
	}

	first := tool.runScript(context.Background(), bashtool.Request{
		Script: "cd /home/agent/work\nexport REPORT_NAME=summary.md\npwd\n",
	})
	if first.ExitCode != 0 {
		t.Fatalf("first exit = %d, stderr = %q", first.ExitCode, first.Stderr)
	}
	if strings.TrimSpace(first.Stdout) != "/home/agent/work" {
		t.Fatalf("first stdout = %q", first.Stdout)
	}
	if first.FinalEnv["PWD"] != "/home/agent/work" {
		t.Fatalf("first pwd = %q, want %q", first.FinalEnv["PWD"], "/home/agent/work")
	}

	second := tool.runScript(context.Background(), bashtool.Request{
		Commands: "printf '%s %s\\n' \"$PWD\" \"$REPORT_NAME\"",
	})
	if second.ExitCode != 0 {
		t.Fatalf("second exit = %d, stderr = %q", second.ExitCode, second.Stderr)
	}
	if got, want := strings.TrimSpace(second.Stdout), "/home/agent/work summary.md"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestPersistentBashToolRejectsEmptyRequest(t *testing.T) {
	t.Parallel()

	registry := newBashRegistry()
	tool, err := newPersistentBashTool(context.Background(), registry)
	if err != nil {
		t.Fatalf("newPersistentBashTool() error = %v", err)
	}

	resp := tool.runScript(context.Background(), bashtool.Request{})
	if resp.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1", resp.ExitCode)
	}
	if resp.Error != "parse_error" {
		t.Fatalf("error = %q, want %q", resp.Error, "parse_error")
	}
}
