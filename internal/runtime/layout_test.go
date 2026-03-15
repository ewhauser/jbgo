package runtime

import (
	"context"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestDefaultSandboxLayout(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo \"$HOME\"\necho \"$PATH\"\nls /\nls /bin\nls /dev\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("Stdout = %q, want at least two lines", result.Stdout)
	}
	if got, want := lines[0], defaultHomeDir; got != want {
		t.Fatalf("HOME = %q, want %q", got, want)
	}
	if got, want := lines[1], defaultPath; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}

	for _, entry := range []string{"bin", "dev", "home", "tmp", "usr"} {
		if !containsLine(lines, entry) {
			t.Fatalf("Stdout missing root entry %q: %q", entry, result.Stdout)
		}
	}
	for _, entry := range []string{"cat", "echo", "ls", "mkdir", "pwd", "rm"} {
		if !containsLine(lines, entry) {
			t.Fatalf("Stdout missing /bin stub %q: %q", entry, result.Stdout)
		}
	}
	if containsLine(lines, "__jb_cd_resolve") {
		t.Fatalf("Stdout should not expose internal command stubs: %q", result.Stdout)
	}
	if !containsLine(lines, "null") {
		t.Fatalf("Stdout missing /dev entry %q: %q", "null", result.Stdout)
	}
}

func TestNewSessionHasPreparedDefaultLayout(t *testing.T) {
	rt := newRuntime(t, &Config{})

	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/bin/echo"); err != nil {
		t.Fatalf("Stat(/bin/echo) error = %v", err)
	}
	if _, err := session.FileSystem().Stat(context.Background(), defaultHomeDir); err != nil {
		t.Fatalf("Stat(%q) error = %v", defaultHomeDir, err)
	}
}

func TestWorkDirUpdatesPWD(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		WorkDir: "/tmp",
		Script:  "echo \"$PWD\"\npwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "/tmp\n/tmp\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRelativePathsUseVirtualWorkDir(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hi > note.txt\ncat note.txt\npwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "hi\n/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestVirtualCDUpdatesPWD(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "pwd\ncd /tmp\npwd\ncd \"$HOME\"\npwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "/home/agent\n/tmp\n/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDirectoryStackBuiltinsManageVirtualPWD(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"mkdir -p a b\n" +
			"pushd a >/dev/null\n" +
			"pushd ../b >/dev/null\n" +
			"dirs -v -l\n" +
			"pushd +1 >/dev/null\n" +
			"dirs -v -l\n" +
			"popd >/dev/null\n" +
			"dirs -v -l\n" +
			"cd /tmp\n" +
			"dirs -v -l\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "" +
		" 0  /home/agent/b\n" +
		" 1  /home/agent/a\n" +
		" 2  /home/agent\n" +
		" 0  /home/agent/a\n" +
		" 1  /home/agent\n" +
		" 2  /home/agent/b\n" +
		" 0  /home/agent\n" +
		" 1  /home/agent/b\n" +
		" 0  /tmp\n" +
		" 1  /home/agent/b\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestDirectoryStackBuiltinsResolveDeferredEntries(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"mkdir -p rel\n" +
			"pushd -n rel >/dev/null\n" +
			"dirs -v -l\n" +
			"dirs +1\n" +
			"pushd +1 >/dev/null\n" +
			"pwd\n" +
			"dirs -v -l\n" +
			"popd -n +0 >/dev/null\n" +
			"dirs -v -l\n" +
			"pwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "" +
		" 0  /home/agent\n" +
		" 1  rel\n" +
		"rel\n" +
		"/home/agent/rel\n" +
		" 0  /home/agent/rel\n" +
		" 1  /home/agent\n" +
		" 0  /home/agent/rel\n" +
		"/home/agent/rel\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestDirectoryStackBuiltinsReportErrors(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"pushd\n" +
			"popd\n" +
			"dirs +9\n" +
			"mkdir -p a\n" +
			"pushd a >/dev/null\n" +
			"pushd +9\n" +
			"popd +9\n" +
			"dirs +9\n" +
			"pushd /no/such/dir\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}

	wantStderr := "" +
		"pushd: no other directory\n" +
		"popd: directory stack empty\n" +
		"dirs: directory stack empty\n" +
		"pushd: +9: directory stack index out of range\n" +
		"popd: +9: directory stack index out of range\n" +
		"dirs: 9: directory stack index out of range\n" +
		"pushd: /no/such/dir: No such file or directory\n"
	if got := result.Stderr; got != wantStderr {
		t.Fatalf("Stderr = %q, want %q", got, wantStderr)
	}
}

func TestPwdHonorsLogicalAndPhysicalModes(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"mkdir -p a/b\n" +
			"ln -s a/b c\n" +
			"cd c\n" +
			"pwd -L\n" +
			"pwd -P\n" +
			"pwd\n" +
			"POSIXLY_CORRECT=1 pwd\n" +
			"PWD=\"$PWD/.\" pwd -L\n" +
			"PWD=bogus pwd -L\n" +
			"PWD=\"/home/agent\" pwd -L\n" +
			"PWD=\"/home/agent/a/../c\" pwd -L\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "" +
		"/home/agent/c\n" +
		"/home/agent/a/b\n" +
		"/home/agent/a/b\n" +
		"/home/agent/c\n" +
		"/home/agent/a/b\n" +
		"/home/agent/a/b\n" +
		"/home/agent/a/b\n" +
		"/home/agent/a/b\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnsureCommandStubDoesNotCloneExistingLowerStub(t *testing.T) {
	ctx := context.Background()
	lower := gbfs.NewMemory()
	file, err := lower.OpenFile(ctx, "/bin/echo", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("OpenFile(/bin/echo) error = %v", err)
	}
	if _, err := file.Write([]byte("lower-v1\n")); err != nil {
		t.Fatalf("Write(/bin/echo) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(/bin/echo) error = %v", err)
	}

	countingLower := &openCountingFS{FileSystem: lower}
	overlay := gbfs.NewOverlay(countingLower)
	if err := ensureCommandStub(ctx, overlay, "/bin", "echo"); err != nil {
		t.Fatalf("ensureCommandStub() error = %v", err)
	}

	file, err = lower.OpenFile(ctx, "/bin/echo", os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("OpenFile(/bin/echo rewrite) error = %v", err)
	}
	if _, err := file.Write([]byte("lower-v2\n")); err != nil {
		t.Fatalf("Write(/bin/echo rewrite) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(/bin/echo rewrite) error = %v", err)
	}

	if got, want := readLayoutTestFile(t, overlay, "/bin/echo"), "lower-v2\n"; got != want {
		t.Fatalf("overlay /bin/echo = %q, want %q", got, want)
	}
	if got := countingLower.opens.Load(); got != 0 {
		t.Fatalf("lower Open() count = %d, want 0", got)
	}
}

type openCountingFS struct {
	gbfs.FileSystem
	opens atomic.Int32
}

func (fs *openCountingFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	fs.opens.Add(1)
	return fs.FileSystem.Open(ctx, name)
}

func readLayoutTestFile(t *testing.T, fsys gbfs.FileSystem, name string) string {
	t.Helper()

	file, err := fsys.Open(context.Background(), name)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", name, err)
	}
	return string(data)
}
