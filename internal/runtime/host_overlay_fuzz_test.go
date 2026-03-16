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

func FuzzHostOverlaySymlinkPolicy(f *testing.F) {
	f.Add([]byte{0, 0, 0})
	f.Add([]byte{1, 1, 1})
	f.Add([]byte{2, 2, 0})
	f.Add([]byte{3, 0, 1})
	f.Add([]byte{4, 1, 0})

	linkTargets := []string{"target.txt", "./target.txt", "", "outside", "missing"}
	commands := []string{"cat link.txt\n", "readlink link.txt\n", "realpath -P link.txt || true\n"}

	f.Fuzz(func(t *testing.T, raw []byte) {
		cursor := newHostOverlayFuzzCursor(raw)
		root := t.TempDir()
		outsideRoot := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(target) error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(outsideRoot, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(secret) error = %v", err)
		}

		linkCase := linkTargets[cursor.Intn(len(linkTargets))]
		linkTarget := "target.txt"
		switch linkCase {
		case "./target.txt":
			linkTarget = "./target.txt"
		case "":
			linkTarget = filepath.Join(root, "target.txt")
		case "outside":
			linkTarget = filepath.Join(outsideRoot, "secret.txt")
		case "missing":
			linkTarget = "missing.txt"
		}
		if err := os.Symlink(linkTarget, filepath.Join(root, "link.txt")); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}

		follow := cursor.Intn(2) == 1
		command := commands[cursor.Intn(len(commands))]

		rt := newRuntime(t, &Config{
			FileSystem: HostProjectFileSystem(root, HostProjectOptions{
				VirtualRoot: hostOverlayVirtualRoot,
			}),
			Policy: policy.NewStatic(&policy.Config{
				ReadRoots:   []string{hostOverlayVirtualRoot, "/usr/bin", "/bin"},
				WriteRoots:  []string{hostOverlayVirtualRoot},
				SymlinkMode: policy.SymlinkMode(map[bool]string{false: string(policy.SymlinkDeny), true: string(policy.SymlinkFollow)}[follow]),
			}),
		})

		result, err := rt.Run(context.Background(), &ExecutionRequest{
			Script: command,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		outOfRoot := linkCase == "outside"
		missingTarget := linkCase == "missing"
		if strings.HasPrefix(command, "cat ") {
			if outOfRoot || missingTarget || !follow {
				if result.ExitCode == 0 {
					t.Fatalf("cat unexpectedly succeeded; linkCase=%q follow=%v stdout=%q stderr=%q", linkCase, follow, result.Stdout, result.Stderr)
				}
				return
			}
			if result.ExitCode != 0 {
				t.Fatalf("cat exit=%d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
		}
	})
}

type hostOverlayFuzzCursor struct {
	data []byte
	idx  int
}

func newHostOverlayFuzzCursor(data []byte) *hostOverlayFuzzCursor {
	return &hostOverlayFuzzCursor{data: data}
}

func (c *hostOverlayFuzzCursor) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	if len(c.data) == 0 {
		c.idx++
		return c.idx % n
	}
	value := int(c.data[c.idx%len(c.data)])
	c.idx++
	return value % n
}
