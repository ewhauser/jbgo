package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestCPCopiesRecursiveDirectoryTree(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src/nested\n echo root > /src/root.txt\n echo leaf > /src/nested/leaf.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "cp -r /src /dst\n find /dst -type f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/dst/nested/leaf.txt", "/dst/root.txt"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
}

func TestCPCopiesBinaryFileBytes(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/src.bin", []byte{0x00, 0xff, 0x41, 0x42, 0x00})

	result := mustExecSession(t, session, "cp /src.bin /dst.bin\n wc -c /dst.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "5 /dst.bin") {
		t.Fatalf("Stdout = %q, want copied byte count", result.Stdout)
	}
}

func TestCPRejectsDirectoryWithoutRecursiveFlag(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src\n echo hi > /src/file.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "cp /src /dst\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "omitting directory") {
		t.Fatalf("Stderr = %q, want directory omission error", result.Stderr)
	}
}

func TestCPSupportsNoClobberPreserveAndVerbose(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "echo new > /src.txt\necho old > /dst.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "cp --no-clobber --preserve --verbose /src.txt /dst.txt\ncat /dst.txt\ncp -pv /src.txt /fresh.txt\ncat /fresh.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "old\n'/src.txt' -> '/fresh.txt'\nnew\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVCanMoveDirectoryIntoExistingDirectory(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src/sub /dst\n echo hi > /src/sub/file.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv /src /dst\n ls /dst/src/sub\n cat /dst/src/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "file.txt\nhi\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVOverwritesExistingDestinationFile(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "echo new > /src.txt\n echo old > /dst.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv /src.txt /dst.txt\n cat /dst.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "new\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVPreservesTraversalForLaterFindCommands(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src/sub /dst\n echo hi > /src/sub/file.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv /src /dst\n find /dst -type f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "/dst/src/sub/file.txt"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVRejectsMissingSource(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mv /missing.txt /dst.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "cannot stat") {
		t.Fatalf("Stderr = %q, want missing-source error", result.Stderr)
	}
}

func TestMVSupportsNoClobberVerboseAndMovingFileIntoDirectory(t *testing.T) {
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /dst\necho src > /src.txt\necho keep > /dst/src.txt\necho move > /move.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv --no-clobber /src.txt /dst/src.txt\ncat /dst/src.txt\nmv -v /move.txt /dst\ncat /dst/move.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "keep\nrenamed '/move.txt' -> '/dst/move.txt'\nmove\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsRelativeRootAndNameFilter(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p docs src\n echo readme > docs/README.md\n echo note > docs/notes.txt\n find . -name \"*.md\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "./docs/README.md"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsTypeAndMaxDepth(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p src/sub\n echo one > src/one.txt\n echo two > src/sub/two.txt\n find /home/agent/src -maxdepth 1 -type f\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "/home/agent/src/one.txt"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindTypeFilterTraversesNestedFiles(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p src/sub\n echo one > src/one.txt\n echo two > src/sub/two.txt\n find /home/agent/src -type f\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for _, want := range []string{"/home/agent/src/one.txt", "/home/agent/src/sub/two.txt"} {
		if !containsLine(lines, want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
}

func TestFindReturnsPartialResultsWhenOneRootIsMissing(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p a\n echo one > a/one.txt\n find /home/agent/a /missing -type f\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "/home/agent/a/one.txt") {
		t.Fatalf("Stdout = %q, want partial success output", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "/missing") {
		t.Fatalf("Stderr = %q, want missing-root error", result.Stderr)
	}
}
