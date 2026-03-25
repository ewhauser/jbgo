package builtins_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/policy"
)

func TestMkdirSupportsModeFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -m 0700 /home/agent/secure\nmkdir --mode=u=rwx,go= /home/agent/symbolic\nstat -c '%a' /home/agent/secure /home/agent/symbolic\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "700\n700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMkdirWithoutParentsRequiresExistingParent(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir /home/agent/missing/child\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-parent error", result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/missing/child"); err == nil {
		t.Fatalf("Stat(child) unexpectedly succeeded")
	}
}

func TestMkdirParentsVerboseReportsEachCreatedDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "cd /home/agent\nmkdir -pv foo/a/b/c/d\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "mkdir: created directory 'foo'\nmkdir: created directory 'foo/a'\nmkdir: created directory 'foo/a/b'\nmkdir: created directory 'foo/a/b/c'\nmkdir: created directory 'foo/a/b/c/d'\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestMkdirParentsVerbosePreservesDotDotRelativePaths(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "cd /home/agent\nmkdir -pv ../c/d\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "mkdir: created directory '../c'\nmkdir: created directory '../c/d'\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/c/d"); err != nil {
		t.Fatalf("Stat(/home/c/d) error = %v, want created directory", err)
	}
}

func TestMkdirParentsVerbosePreservesDotSlashPrefix(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "cd /home/agent\nmkdir -pv ./c/d\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "mkdir: created directory './c'\nmkdir: created directory './c/d'\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/c/d"); err != nil {
		t.Fatalf("Stat(/home/agent/c/d) error = %v, want created directory", err)
	}
}

func TestMkdirParentsCreatesLexicalAncestorsBeforeDotDot(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "cd /home/agent\nmkdir -p d2/..\nprintf 'status=%s\\n' \"$?\"\ntest -d d2\nprintf 'exists=%s\\n' \"$?\"\n")
	if got, want := result.Stdout, "status=0\nexists=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q; stderr=%q", got, want, result.Stderr)
	}
}

func TestMkdirParentsUsesShellUmaskForAncestorsAndExplicitModeForLeaf(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "umask 077\nmkdir -p -m 703 /home/agent/a/b/c/d\nstat -c '%a' /home/agent/a /home/agent/a/b /home/agent/a/b/c /home/agent/a/b/c/d\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "700\n700\n700\n703"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMkdirSupportsRareEqualsPlusSymbolicModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "umask 027\nmkdir -m =+x /home/agent/mode-x\nstat -c '%A' /home/agent/mode-x\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "d--x--x---"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMkdirParentsKeepsUserWriteExecuteOnIntermediateDirectories(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "umask 160\nmkdir -p /home/agent/parent/sub\nstat -c '%a' /home/agent/parent /home/agent/parent/sub\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "717\n617"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMkdirRemapsCompatHostAbsolutePaths(t *testing.T) {
	t.Parallel()
	env := defaultBaseEnv()
	env["GBASH_COMPAT_ROOT"] = "/compat"
	session := newSession(t, &Config{BaseEnv: env})

	result := mustExecSession(t, session, "mkdir /testdir\ncd /testdir\nmkdir -p /compat/testdir/t\nprintf 'status=%s\\n' \"$?\"\ntest -d /testdir/t\nprintf 'exists=%s\\n' \"$?\"\n")
	if got, want := result.Stdout, "status=0\nexists=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q; stderr=%q", got, want, result.Stderr)
	}
}

func TestMkdirParentsFollowsSymlinkedDirectoryAncestorsWhenAllowed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatalf("MkdirAll(real) error = %v", err)
	}
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}

	session := newSession(t, &Config{
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/", "/usr/bin", "/bin"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result := mustExecSession(t, session, "mkdir -p /link/sub\nprintf 'status=%s\\n' \"$?\"\ntest -d /real/sub\nprintf 'exists=%s\\n' \"$?\"\nmkdir -p /link\nprintf 'status_link=%s\\n' \"$?\"\n")
	if got, want := result.Stdout, "status=0\nexists=0\nstatus_link=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q; stderr=%q", got, want, result.Stderr)
	}
}
