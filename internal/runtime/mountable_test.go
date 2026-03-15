package runtime

import (
	"context"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestMountableFileSystemHelperSupportsCrossMountMove(t *testing.T) {
	rt := newRuntime(t, &Config{
		FileSystem: MountableFileSystem(MountableFileSystemOptions{
			Mounts: []gbfs.MountConfig{
				{
					MountPoint: "/src",
					Factory: seededFSFactory{files: map[string]string{
						"/dir/file.txt": "moved\n",
					}},
				},
				{
					MountPoint: "/dst",
					Factory:    gbfs.Memory(),
				},
			},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mv /src/dir /dst/copied\ncat /dst/copied/file.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "moved\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
