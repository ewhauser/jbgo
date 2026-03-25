//go:build cgo && !(darwin && amd64)

package python

import (
	"context"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

func TestPythonEvalSupportsPythonAndPython3(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python -c 'pass'\npython3 -c 'value = 3'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, ""; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPythonFileExecutionWorks(t *testing.T) {
	t.Parallel()

	session := newPythonSession(t)
	writePythonSessionFile(t, session, "/home/agent/main.py", []byte("value = 1\n"))

	result := mustExecPythonSession(t, session, "python ./main.py\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, ""; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPythonReadsSourceFromStdin(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf \"value = 1\\n\" | python\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, ""; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPythonRejectsBarePrint(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python -c 'print(\"alias\")'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "print is not supported yet") {
		t.Fatalf("Stderr = %q, want unsupported print diagnostic", result.Stderr)
	}
}

func TestPythonRejectsAliasedPrint(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python -c 'alias = print\nalias(\"alias\")'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "print is not supported yet") {
		t.Fatalf("Stderr = %q, want unsupported print diagnostic", result.Stderr)
	}
}

func TestPythonRejectsPrintFileTargets(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python -c 'import sys\nprint(\"out\", file=sys.stdout)'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "print is not supported yet") {
		t.Fatalf("Stderr = %q, want unsupported print diagnostic", result.Stderr)
	}
}

func TestPythonHelpAndVersionIdentifyAlias(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python --help\npython3 --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Usage: python [-c command] [script.py]") {
		t.Fatalf("Stdout = %q, want python help usage", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "python3 (gbash)\n") {
		t.Fatalf("Stdout = %q, want python3 version line", result.Stdout)
	}
}

func TestPythonRejectsUnsupportedFlags(t *testing.T) {
	t.Parallel()

	result, err := newPythonRuntime(t).Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "python -m json.tool\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "invalid option -- 'm'") {
		t.Fatalf("Stderr = %q, want unsupported flag diagnostic", result.Stderr)
	}
}

func TestPythonRejectsExtraScriptArguments(t *testing.T) {
	t.Parallel()

	session := newPythonSession(t)
	writePythonSessionFile(t, session, "/home/agent/main.py", []byte("value = 1\n"))

	result := mustExecPythonSession(t, session, "python main.py extra\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "extra script arguments are not supported") {
		t.Fatalf("Stderr = %q, want extra-args diagnostic", result.Stderr)
	}
}

func TestPythonUsesSandboxEnvironment(t *testing.T) {
	t.Parallel()

	session := newPythonSession(t)

	result := mustExecPythonSession(t, session, ""+
		"FOO=bar python -c 'import os\n"+
		"from pathlib import Path\n"+
		"Path(\"env.txt\").write_text(os.getenv(\"FOO\") + \"\\n\" + os.environ[\"FOO\"] + \"\\n\")'\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := string(readPythonSessionFile(t, session, "/home/agent/env.txt")); got != "bar\nbar\n" {
		t.Fatalf("env.txt = %q, want %q", got, "bar\nbar\n")
	}
}

func TestPythonUsesSandboxFilesystemAndRelativePaths(t *testing.T) {
	t.Parallel()

	session := newPythonSession(t)

	result := mustExecPythonSession(t, session, ""+
		"mkdir -p /home/agent/project\n"+
		"cd /home/agent/project\n"+
		"python -c 'from pathlib import Path\n"+
		"Path(\"note.txt\").write_text(\"hello\")\n"+
		"copy = Path(\"note.txt\").read_text()\n"+
		"Path(\"copy.txt\").write_text(copy)'\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := string(readPythonSessionFile(t, session, "/home/agent/project/note.txt")); got != "hello" {
		t.Fatalf("file contents = %q, want hello", got)
	}
	if got := string(readPythonSessionFile(t, session, "/home/agent/project/copy.txt")); got != "hello" {
		t.Fatalf("copy contents = %q, want hello", got)
	}
}

func TestPythonFileSystemUsesSandboxPolicyAndTraces(t *testing.T) {
	t.Parallel()

	rt, err := gbruntime.New(gbruntime.WithConfig(&gbruntime.Config{
		Registry: newPythonRegistry(t),
		Tracing:  gbruntime.TraceConfig{Mode: gbruntime.TraceRaw},
		Policy: policy.NewStatic(&policy.Config{
			AllowedCommands: []string{"python", "python3"},
			ReadRoots:       []string{"/allowed", "/tmp", "/usr/bin", "/bin", "/home/agent"},
			WriteRoots:      []string{"/tmp", "/usr/bin", "/bin", "/home/agent"},
			Limits: policy.Limits{
				MaxStdoutBytes: 1 << 20,
				MaxStderrBytes: 1 << 20,
				MaxFileBytes:   8 << 20,
			},
			SymlinkMode: policy.SymlinkDeny,
		}),
	}))
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}

	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("Runtime.NewSession() error = %v", err)
	}

	writePythonSessionFile(t, session, "/allowed/input.txt", []byte("ok\n"))
	writePythonSessionFile(t, session, "/denied.txt", []byte("secret\n"))

	allowed := mustExecPythonSession(t, session, ""+
		"python -c 'from pathlib import Path\n"+
		"text = Path(\"/allowed/input.txt\").read_text()\n"+
		"Path(\"/tmp/out.txt\").write_text(text)'\n")
	if allowed.ExitCode != 0 {
		t.Fatalf("allowed ExitCode = %d, want 0; stderr=%q", allowed.ExitCode, allowed.Stderr)
	}
	if got, want := allowed.Stdout, ""; got != want {
		t.Fatalf("allowed Stdout = %q, want %q", got, want)
	}
	if got := string(readPythonSessionFile(t, session, "/tmp/out.txt")); got != "ok\n" {
		t.Fatalf("/tmp/out.txt = %q, want %q", got, "ok\n")
	}
	if !hasFileAccess(allowed.Events, "read", "/allowed/input.txt") {
		t.Fatalf("allowed events missing read access: %#v", allowed.Events)
	}
	if !hasFileAccess(allowed.Events, "write", "/tmp/out.txt") {
		t.Fatalf("allowed events missing write access: %#v", allowed.Events)
	}

	denied := mustExecPythonSession(t, session, ""+
		"python -c 'from pathlib import Path\n"+
		"Path(\"/denied.txt\").read_text()'\n")
	if denied.ExitCode == 0 {
		t.Fatalf("denied ExitCode = %d, want non-zero", denied.ExitCode)
	}
	if !strings.Contains(denied.Stderr, "outside allowed roots") {
		t.Fatalf("denied stderr = %q, want sandbox denial", denied.Stderr)
	}
	if !hasPolicyPath(denied.Events, "/denied.txt") {
		t.Fatalf("denied events missing policy path: %#v", denied.Events)
	}
}

func TestPythonShebangViaEnvWorks(t *testing.T) {
	t.Parallel()

	session := newPythonSession(t)
	writePythonSessionFile(t, session, "/home/agent/tool.py", []byte("#!/usr/bin/env python3\nfrom pathlib import Path\nPath('/home/agent/shebang.txt').write_text('ok')\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/home/agent/tool.py", 0o755); err != nil {
		t.Fatalf("Chmod(tool.py) error = %v", err)
	}

	result := mustExecPythonSession(t, session, "/home/agent/tool.py\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := string(readPythonSessionFile(t, session, "/home/agent/shebang.txt")); got != "ok" {
		t.Fatalf("shebang.txt = %q, want ok", got)
	}
}

func hasFileAccess(events []trace.Event, action, name string) bool {
	for i := range events {
		event := &events[i]
		if event.Kind != trace.EventFileAccess || event.File == nil {
			continue
		}
		if event.File.Action == action && event.File.Path == name {
			return true
		}
	}
	return false
}

func hasPolicyPath(events []trace.Event, name string) bool {
	for i := range events {
		event := &events[i]
		if event.Kind != trace.EventPolicyDenied || event.Policy == nil {
			continue
		}
		if event.Policy.Path == name {
			return true
		}
	}
	return false
}
