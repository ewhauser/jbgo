//go:build windows

package cli

import "testing"

func TestSandboxPathForMountedHostPathCrossVolumeReturnsUnmapped(t *testing.T) {
	t.Parallel()

	sandboxPath, ok, err := sandboxPathForMountedHostPath(`D:\scripts\main.sh`, `C:\workspace`, `/home/agent/project`)
	if err != nil {
		t.Fatalf("sandboxPathForMountedHostPath() error = %v", err)
	}
	if ok {
		t.Fatalf("sandboxPathForMountedHostPath() mapped = true, want false; sandboxPath=%q", sandboxPath)
	}
	if sandboxPath != "" {
		t.Fatalf("sandboxPath = %q, want empty", sandboxPath)
	}
}
