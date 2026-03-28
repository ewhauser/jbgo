package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ewhauser/gbash"
)

func TestRunScriptHarnessHelp(t *testing.T) {
	workspaceDir := copyTreeToTemp(t, mustWorkspaceDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode, err := runWithWorkspace(context.Background(), workspaceDir, strings.NewReader(""), &stdout, &stderr, []string{
		"--script", "./bin/harness help",
	})
	if err != nil {
		t.Fatalf("runWithWorkspace() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"harness",
		"tools (discovered):",
		"bash",
		"session",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want substring %q", out, want)
		}
	}
}

func TestPersistentHarnessBashTool(t *testing.T) {
	workspaceDir := copyTreeToTemp(t, mustWorkspaceDir())

	rt, err := newRuntime(workspaceDir)
	if err != nil {
		t.Fatalf("newRuntime() error = %v", err)
	}

	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	first, err := session.Exec(context.Background(), &gbash.ExecutionRequest{
		Name:    exampleName,
		WorkDir: gbash.DefaultWorkspaceMountPoint,
		Script: `
mkdir -p /tmp/harness-session
jq -cn --arg command 'cd /tmp && export FOO=bar' '{command: $command}' \
  | HARNESS_SESSION=/tmp/harness-session ./.harness/tools/bash --exec
`,
	})
	if err != nil {
		t.Fatalf("first Exec() error = %v", err)
	}
	if first.ExitCode != 0 {
		t.Fatalf("first exit = %d, want 0; stdout=%q stderr=%q", first.ExitCode, first.Stdout, first.Stderr)
	}

	second, err := session.Exec(context.Background(), &gbash.ExecutionRequest{
		Name:    exampleName,
		WorkDir: gbash.DefaultWorkspaceMountPoint,
		Script: `
jq -cn --arg command 'printf "%s %s\n" "$PWD" "$FOO"' '{command: $command}' \
  | HARNESS_SESSION=/tmp/harness-session ./.harness/tools/bash --exec
`,
	})
	if err != nil {
		t.Fatalf("second Exec() error = %v", err)
	}
	if second.ExitCode != 0 {
		t.Fatalf("second exit = %d, want 0; stdout=%q stderr=%q", second.ExitCode, second.Stdout, second.Stderr)
	}
	if got, want := strings.TrimSpace(second.Stdout), "/tmp bar"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestOfflineHarnessLoopWithMockProvider(t *testing.T) {
	workspaceDir := copyTreeToTemp(t, mustWorkspaceDir())
	writeMockProvider(t, workspaceDir)

	t.Setenv("HARNESS_PROVIDER", "mock")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode, err := runWithWorkspace(context.Background(), workspaceDir, strings.NewReader(""), &stdout, &stderr, []string{
		"--script", `./bin/harness "test"`,
	})
	if err != nil {
		t.Fatalf("runWithWorkspace() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), "/tmp bar"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestUpdateHarnessScriptReproducesVendoredTree(t *testing.T) {
	exampleDir := copyTreeToTemp(t, filepath.Dir(mustWorkspaceDir()))
	upstreamDir := filepath.Join(t.TempDir(), "upstream")
	if err := os.MkdirAll(filepath.Join(upstreamDir, "bin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(upstreamDir, "plugins"), 0o755); err != nil {
		t.Fatalf("MkdirAll(plugins) error = %v", err)
	}

	sourceWorkspace := mustWorkspaceDir()
	for _, path := range []string{
		"bin",
		"plugins",
	} {
		if err := copyTree(filepath.Join(sourceWorkspace, path), filepath.Join(upstreamDir, path)); err != nil {
			t.Fatalf("copyTree(%s) error = %v", path, err)
		}
	}
	licenseData, err := os.ReadFile(filepath.Join(sourceWorkspace, "LICENSE.harness"))
	if err != nil {
		t.Fatalf("ReadFile(LICENSE.harness) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(upstreamDir, "LICENSE"), licenseData, 0o644); err != nil {
		t.Fatalf("WriteFile(LICENSE) error = %v", err)
	}

	runCommand(t, upstreamDir, "git", "init")
	runCommand(t, upstreamDir, "git", "config", "user.name", "Test User")
	runCommand(t, upstreamDir, "git", "config", "user.email", "test@example.com")
	runCommand(t, upstreamDir, "git", "add", ".")
	runCommand(t, upstreamDir, "git", "commit", "-m", "initial")
	ref := strings.TrimSpace(runCommand(t, upstreamDir, "git", "rev-parse", "HEAD"))

	targetFile := filepath.Join(exampleDir, "workspace", "bin", "harness")
	if err := os.Remove(targetFile); err != nil {
		t.Fatalf("Remove(%q) error = %v", targetFile, err)
	}

	cmd := osexec.Command(filepath.Join(exampleDir, "update-harness.sh"), "--ref", ref)
	cmd.Dir = exampleDir
	cmd.Env = append(os.Environ(), "HARNESS_UPSTREAM_REPO="+upstreamDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("update-harness.sh error = %v\n%s", err, output)
	}

	upstreamCommitData, err := os.ReadFile(filepath.Join(exampleDir, "UPSTREAM_COMMIT"))
	if err != nil {
		t.Fatalf("ReadFile(UPSTREAM_COMMIT) error = %v", err)
	}
	if got := strings.TrimSpace(string(upstreamCommitData)); got != ref {
		t.Fatalf("UPSTREAM_COMMIT = %q, want %q", got, ref)
	}

	for _, relative := range []string{
		"workspace/bin/harness",
		"workspace/plugins/core/tools/bash",
		"workspace/plugins/openai/providers/openai",
		"workspace/LICENSE.harness",
	} {
		got, err := os.ReadFile(filepath.Join(exampleDir, relative))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", relative, err)
		}

		wantPath := filepath.Join(upstreamDir, strings.TrimPrefix(relative, "workspace/"))
		if relative == "workspace/LICENSE.harness" {
			wantPath = filepath.Join(upstreamDir, "LICENSE")
		}
		want, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", wantPath, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s did not match refreshed upstream contents", relative)
		}
	}
}

func copyTreeToTemp(t *testing.T, src string) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}
	return dst
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch mode := info.Mode(); {
		case mode.IsDir():
			return os.MkdirAll(target, mode.Perm())
		case mode&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.WriteFile(target, data, mode.Perm())
		}
	})
}

func runCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()

	cmd := osexec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s error = %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func writeMockProvider(t *testing.T, workspaceDir string) {
	t.Helper()

	mockDir := filepath.Join(workspaceDir, "plugins", "mock")
	for _, dir := range []string{
		filepath.Join(mockDir, "providers"),
		filepath.Join(mockDir, "hooks.d", "assemble"),
		filepath.Join(mockDir, "hooks.d", "receive"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}

	for _, file := range []string{
		"hooks.d/assemble/10-messages",
		"hooks.d/receive/10-save",
	} {
		src := filepath.Join(workspaceDir, "plugins", "openai", file)
		dst := filepath.Join(mockDir, file)
		if err := copyFile(src, dst); err != nil {
			t.Fatalf("copyTree(%q) error = %v", file, err)
		}
	}

	providerScript := `#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  --describe)
    echo "Mock OpenAI-compatible provider"
    exit 0
    ;;
  --ready)
    exit 0
    ;;
  --defaults)
    echo "model=mock-model"
    exit 0
    ;;
  --env)
    exit 0
    ;;
esac

payload="$(cat)"
tool_count="$(echo "${payload}" | jq '[.messages[] | select(.role == "tool")] | length')"

if [[ "${tool_count}" == "0" ]]; then
  jq -n --arg args '{"command":"cd /tmp && export FOO=bar"}' '{
    model: "mock-model",
    choices: [{
      finish_reason: "tool_calls",
      message: {
        role: "assistant",
        tool_calls: [{
          id: "call_1",
          type: "function",
          function: {
            name: "bash",
            arguments: $args
          }
        }]
      }
    }],
    usage: {
      prompt_tokens: 1,
      completion_tokens: 1
    }
  }'
  exit 0
fi

if [[ "${tool_count}" == "1" ]]; then
  jq -n --arg args '{"command":"printf \"%s %s\\n\" \"$PWD\" \"$FOO\""}' '{
    model: "mock-model",
    choices: [{
      finish_reason: "tool_calls",
      message: {
        role: "assistant",
        tool_calls: [{
          id: "call_2",
          type: "function",
          function: {
            name: "bash",
            arguments: $args
          }
        }]
      }
    }],
    usage: {
      prompt_tokens: 1,
      completion_tokens: 1
    }
  }'
  exit 0
fi

final_output="$(echo "${payload}" | jq -r '[.messages[] | select(.role == "tool") | .content][1]')"
jq -n --arg output "${final_output}" '{
  model: "mock-model",
  choices: [{
    finish_reason: "stop",
    message: {
      role: "assistant",
      content: $output
    }
  }],
  usage: {
    prompt_tokens: 1,
    completion_tokens: 1
  }
}'
`
	providerPath := filepath.Join(mockDir, "providers", "mock")
	if err := os.WriteFile(providerPath, []byte(providerScript), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", providerPath, err)
	}
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}
