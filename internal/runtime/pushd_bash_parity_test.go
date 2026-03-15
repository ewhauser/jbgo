package runtime

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var bashLinePrefixPattern = regexp.MustCompile(`(?m)^(?:[^:\n]+/)?bash: line \d+: `)

// These cases track observable directory-stack command behavior. They
// intentionally do not cover shell introspection like `type pushd`, which
// still differs because gbash currently injects shell functions here.
func TestDirectoryStackMatchesBashBehavior(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	testCases := []struct {
		name   string
		script string
	}{
		{
			name: "basic pushd and popd",
			script: "" +
				"mkdir -p a b\n" +
				"pushd a\n" +
				"pushd ../b\n" +
				"popd\n",
		},
		{
			name: "dirs option matrix",
			script: "" +
				"mkdir -p a b\n" +
				"pushd a >/dev/null\n" +
				"pushd ../b >/dev/null\n" +
				"dirs\n" +
				"dirs -p\n" +
				"dirs -v\n" +
				"dirs -l\n" +
				"dirs +1\n" +
				"dirs -1\n",
		},
		{
			name: "pushd with no args rotates",
			script: "" +
				"mkdir -p a\n" +
				"pushd a >/dev/null\n" +
				"pushd\n",
		},
		{
			name: "pushd index rotation",
			script: "" +
				"mkdir -p a b c\n" +
				"pushd a >/dev/null\n" +
				"pushd ../b >/dev/null\n" +
				"pushd ../c >/dev/null\n" +
				"pushd +1\n" +
				"pushd -1\n",
		},
		{
			name: "pushd n keeps cwd while rotating",
			script: "" +
				"mkdir -p a b\n" +
				"pushd a >/dev/null\n" +
				"pushd ../b >/dev/null\n" +
				"pushd -n +1\n" +
				"pwd\n" +
				"dirs -v -l\n",
		},
		{
			name: "pushd n defers relative path resolution",
			script: "" +
				"mkdir -p rel\n" +
				"pushd -n rel\n" +
				"pushd +1\n" +
				"pwd\n" +
				"dirs -v -l\n",
		},
		{
			name: "popd indexed removal",
			script: "" +
				"mkdir -p a b c\n" +
				"pushd a >/dev/null\n" +
				"pushd ../b >/dev/null\n" +
				"pushd ../c >/dev/null\n" +
				"popd +1\n" +
				"popd -1\n",
		},
		{
			name: "popd n keeps cwd",
			script: "" +
				"mkdir -p a b\n" +
				"pushd a >/dev/null\n" +
				"pushd ../b >/dev/null\n" +
				"popd -n +1\n" +
				"pwd\n" +
				"dirs -v -l\n",
		},
		{
			name: "dirs clear resets stack to cwd",
			script: "" +
				"mkdir -p a\n" +
				"pushd a >/dev/null\n" +
				"dirs -c\n" +
				"dirs -v\n",
		},
		{
			name: "cd syncs stack top",
			script: "" +
				"mkdir -p a b\n" +
				"pushd a >/dev/null\n" +
				"pushd ../b >/dev/null\n" +
				"cd ..\n" +
				"dirs -v -l\n",
		},
		{
			name:   "nonexistent target errors",
			script: "pushd /no/such/dir\n",
		},
		{
			name: "non directory target errors",
			script: "" +
				"printf 'x' > file.txt\n" +
				"pushd file.txt\n",
		},
		{
			name: "invalid number diagnostics",
			script: "" +
				"pushd -x\n" +
				"popd +x\n" +
				"dirs -x\n",
		},
		{
			name: "out of range diagnostics",
			script: "" +
				"mkdir -p a\n" +
				"pushd a >/dev/null\n" +
				"pushd +9\n" +
				"popd +9\n" +
				"dirs +9\n",
		},
		{
			name: "dash directories require double dash",
			script: "" +
				"mkdir -- -dir\n" +
				"pushd -- -dir\n" +
				"popd\n",
		},
		{
			name: "duplicates are preserved",
			script: "" +
				"mkdir -p a\n" +
				"pushd a >/dev/null\n" +
				"pushd -n a >/dev/null\n" +
				"dirs -v -l\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			homeDir := filepath.ToSlash(t.TempDir())
			gbash := runDirectoryStackGBash(t, tc.script)
			bash := runDirectoryStackBash(t, bashPath, homeDir, tc.script)

			if gbash.ExitCode != bash.ExitCode || gbash.Stdout != bash.Stdout || gbash.Stderr != bash.Stderr {
				t.Fatalf(
					"directory stack parity mismatch\nscript:\n%s\n\ngbash:\n%s\n\nbash:\n%s",
					tc.script,
					mustJSON(t, gbash),
					mustJSON(t, bash),
				)
			}
		})
	}
}

func runDirectoryStackGBash(t testing.TB, script string) normalizedExecutionResult {
	t.Helper()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	return normalizedExecutionResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}
}

func runDirectoryStackBash(t testing.TB, bashPath, homeDir, script string) normalizedExecutionResult {
	t.Helper()

	cmd := exec.Command(bashPath, "--noprofile", "--norc", "-c", "cd \"$HOME\"\n"+script)
	cmd.Env = []string{
		"HOME=" + homeDir,
		"PWD=" + homeDir,
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
		"LANG=C",
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("bash Run() error = %v", err)
		}
		exitCode = exitErr.ExitCode()
	}

	return normalizedExecutionResult{
		ExitCode: exitCode,
		Stdout:   normalizeDirectoryStackOutput(stdout.String(), homeDir),
		Stderr:   normalizeDirectoryStackOutput(stderr.String(), homeDir),
	}
}

func normalizeDirectoryStackOutput(value, homeDir string) string {
	value = strings.ReplaceAll(value, filepath.ToSlash(homeDir), "/home/agent")
	return bashLinePrefixPattern.ReplaceAllString(value, "")
}
