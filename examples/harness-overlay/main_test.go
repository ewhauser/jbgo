package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ewhauser/gbash"
)

var (
	preparedWorkspaceOnce sync.Once
	preparedWorkspaceBase string
	preparedWorkspaceErr  error
)

func TestRunScriptHarnessHelp(t *testing.T) {
	t.Parallel()

	workspaceDir := preparedWorkspaceForTests(t)

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
	t.Parallel()

	workspaceDir := preparedWorkspaceForTests(t)

	rt, err := newRuntime(context.Background(), workspaceDir)
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
	t.Parallel()

	workspaceDir := preparedWorkspaceForTests(t)
	writeMockProvider(t, workspaceDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode, err := runWithWorkspace(context.Background(), workspaceDir, strings.NewReader(""), &stdout, &stderr, []string{
		"--script", `HARNESS_PROVIDER=mock ./bin/harness "test"`,
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

func TestUpdateHarnessScriptStagesPreparedWorkspace(t *testing.T) {
	t.Parallel()

	exampleDir := copyTreeToTemp(t, filepath.Dir(mustWorkspaceDir()))
	upstreamDir := filepath.Join(t.TempDir(), "upstream")
	writeFile(t, filepath.Join(upstreamDir, "bin", "harness"), "#!/usr/bin/env bash\necho harness\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "bin", "hs"), "#!/usr/bin/env bash\necho hs\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "plugins", "auth", "commands", "login"), "auth-login\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "plugins", "core", "tools", "bash"), "core-bash\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "plugins", "openai", "providers", "openai"), "openai-provider\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "plugins", "anthropic", "providers", "anthropic"), "anthropic-provider\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "plugins", "chatgpt", "providers", "chatgpt"), "chatgpt-provider\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "plugins", "skills", "tools", "skills"), "skills-tool\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "plugins", "subagents", "tools", "subagents"), "subagents-tool\n", 0o755)
	writeFile(t, filepath.Join(upstreamDir, "LICENSE"), "upstream license\n", 0o644)

	runGitCommand(t, upstreamDir, "init")
	runGitCommand(t, upstreamDir, "config", "user.name", "Test User")
	runGitCommand(t, upstreamDir, "config", "user.email", "test@example.com")
	runGitCommand(t, upstreamDir, "add", ".")
	runGitCommand(t, upstreamDir, "commit", "-m", "initial")
	ref := strings.TrimSpace(runGitCommand(t, upstreamDir, "rev-parse", "HEAD"))

	cacheDir := filepath.Join(t.TempDir(), "cache")
	cmd := osexec.CommandContext(
		t.Context(),
		filepath.Join(exampleDir, "update-harness.sh"),
		"--ref", ref,
		"--cache-dir", cacheDir,
	)
	cmd.Dir = exampleDir
	cmd.Env = append(os.Environ(), "HARNESS_UPSTREAM_REPO="+upstreamDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("update-harness.sh error = %v\n%s", err, output)
	}
	workspaceDir := strings.TrimSpace(string(output))
	if workspaceDir == "" {
		t.Fatal("update-harness.sh returned an empty workspace path")
	}

	for _, relative := range []string{
		"bin/harness",
		"bin/hs",
		"plugins/core/tools/bash",
		"plugins/openai/providers/openai",
		"plugins/anthropic/providers/anthropic",
		"plugins/chatgpt/providers/chatgpt",
		"plugins/skills/tools/skills",
		"plugins/subagents/tools/subagents",
		"LICENSE.harness",
	} {
		got, err := os.ReadFile(filepath.Join(workspaceDir, relative))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", relative, err)
		}

		wantPath := filepath.Join(upstreamDir, relative)
		if relative == "LICENSE.harness" {
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

	if info, err := os.Stat(filepath.Join(workspaceDir, "bin", "harness")); err != nil {
		t.Fatalf("Stat(bin/harness) error = %v", err)
	} else if info.Mode().Perm() != 0o755 {
		t.Fatalf("bin/harness mode = %v, want 0755", info.Mode().Perm())
	}

	for _, relative := range []string{
		".harness/tools/bash",
		".harness/hooks.d/assemble/10-messages",
		"AGENTS.md",
	} {
		got, err := os.ReadFile(filepath.Join(workspaceDir, relative))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", relative, err)
		}
		want, err := os.ReadFile(filepath.Join(exampleDir, "workspace", relative))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", relative, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s did not match committed overlay contents", relative)
		}
	}
}

func preparedWorkspaceForTests(t *testing.T) string {
	t.Helper()

	preparedWorkspaceOnce.Do(func() {
		cacheDir, err := os.MkdirTemp("", "harness-overlay-cache-")
		if err != nil {
			preparedWorkspaceErr = fmt.Errorf("create cache dir: %w", err)
			return
		}

		cmd := osexec.CommandContext(
			context.Background(),
			filepath.Join(mustExampleDir(), "update-harness.sh"),
			"--cache-dir", cacheDir,
		)
		cmd.Dir = mustExampleDir()
		output, err := cmd.CombinedOutput()
		if err != nil {
			preparedWorkspaceErr = fmt.Errorf("prepare workspace: %w\n%s", err, output)
			return
		}

		preparedWorkspaceBase = strings.TrimSpace(string(output))
		if preparedWorkspaceBase == "" {
			preparedWorkspaceErr = fmt.Errorf("prepare workspace returned an empty path")
			return
		}
	})

	if preparedWorkspaceErr != nil {
		t.Fatalf("preparedWorkspaceForTests() error = %v", preparedWorkspaceErr)
	}

	return copyTreeToTemp(t, preparedWorkspaceBase)
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

func runGitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := osexec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v\n%s", strings.Join(args, " "), err, output)
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

func writeFile(t *testing.T, path, contents string, perm os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), perm); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
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
