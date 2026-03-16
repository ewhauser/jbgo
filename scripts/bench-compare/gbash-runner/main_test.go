package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseOptions(t *testing.T) {
	t.Parallel()
	opts, err := parseOptions([]string{
		"--workspace", "/tmp/workspace",
		"--cwd", "/home/agent/project",
		"--command", "echo benchmark",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if opts.workspace != "/tmp/workspace" {
		t.Fatalf("workspace = %q, want %q", opts.workspace, "/tmp/workspace")
	}
	if opts.cwd != "/home/agent/project" {
		t.Fatalf("cwd = %q, want %q", opts.cwd, "/home/agent/project")
	}
	if opts.command != "echo benchmark" {
		t.Fatalf("command = %q, want %q", opts.command, "echo benchmark")
	}
}

func TestParseOptionsRequiresCommand(t *testing.T) {
	t.Parallel()
	if _, err := parseOptions([]string{"--workspace", "/tmp/workspace"}); err == nil {
		t.Fatalf("parseOptions() error = nil, want error")
	}
}

func TestGbashRunnerSmoke(t *testing.T) {
	t.Parallel()
	repoRoot, err := findModuleRoot(".")
	if err != nil {
		t.Fatalf("findModuleRoot() error = %v", err)
	}

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(a.txt) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "nested", "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(b.txt) error = %v", err)
	}

	cmd := exec.Command("go", "run", "./scripts/bench-compare/gbash-runner", //nolint:noctx // test helper
		"--workspace", workspace,
		"-c", "find . -type f | grep -c '^'",
	)
	cmd.Dir = repoRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("cmd.Run() error = %v; stderr=%q", err, stderr.String())
	}
	if got, want := stdout.String(), "2\n"; got != want {
		t.Fatalf("stdout = %q, want %q; stderr=%q", got, want, stderr.String())
	}
}

func findModuleRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
