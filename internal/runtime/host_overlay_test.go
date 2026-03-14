//go:build !windows

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/policy"
)

const hostOverlayVirtualRoot = "/home/agent/project"

func TestOverlayFactoryWithHostLowerSupportsCopyOnWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: HostProjectFileSystem(root, HostProjectOptions{
			VirtualRoot: hostOverlayVirtualRoot,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat seed.txt\necho upper > seed.txt\ncat seed.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "seed\nupper\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	data, err := os.ReadFile(filepath.Join(root, "seed.txt"))
	if err != nil {
		t.Fatalf("ReadFile(host) error = %v", err)
	}
	if got, want := string(data), "seed\n"; got != want {
		t.Fatalf("host file = %q, want %q", got, want)
	}
}

func TestOverlayHostLowerTombstonesDoNotDeleteHostFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	session := newSession(t, &Config{
		FileSystem: HostProjectFileSystem(root, HostProjectOptions{
			VirtualRoot: hostOverlayVirtualRoot,
		}),
	})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "rm seed.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if _, err := session.FileSystem().Stat(context.Background(), hostOverlayVirtualRoot+"/seed.txt"); !os.IsNotExist(err) {
		t.Fatalf("session Stat(seed.txt) error = %v, want not exist", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "seed.txt"))
	if err != nil {
		t.Fatalf("ReadFile(host) error = %v", err)
	}
	if got, want := string(data), "seed\n"; got != want {
		t.Fatalf("host file = %q, want %q", got, want)
	}
}

func TestOverlayHostLowerDefaultPolicyDeniesSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: HostProjectFileSystem(root, HostProjectOptions{
			VirtualRoot: hostOverlayVirtualRoot,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "symlink traversal denied") {
		t.Fatalf("Stderr = %q, want symlink denial", result.Stderr)
	}
}

func TestOverlayHostLowerFollowModeAllowsInRootSymlinkReads(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: HostProjectFileSystem(root, HostProjectOptions{
			VirtualRoot: hostOverlayVirtualRoot,
		}),
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{hostOverlayVirtualRoot, "/usr/bin", "/bin"},
			WriteRoots:  []string{hostOverlayVirtualRoot},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
