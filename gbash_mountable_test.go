package gbash_test

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/ewhauser/gbash"
	gbfs "github.com/ewhauser/gbash/fs"
)

func TestSeededInMemoryFileSystemHelper(t *testing.T) {
	t.Parallel()

	rt, err := gbash.New(
		gbash.WithFileSystem(gbash.SeededInMemoryFileSystem(gbfs.InitialFiles{
			"/home/agent/lazy.txt": {
				Lazy: func(context.Context) ([]byte, error) {
					return []byte("seeded\n"), nil
				},
			},
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &gbash.ExecutionRequest{
		Script: "cat /home/agent/lazy.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.Stdout, "seeded\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMountableFileSystemSupportsShellMvAcrossMounts(t *testing.T) {
	t.Parallel()

	rt, err := gbash.New(
		gbash.WithFileSystem(gbash.MountableFileSystem(gbash.MountableFileSystemOptions{
			Mounts: []gbfs.MountConfig{
				{MountPoint: "/src", Factory: seededFactory(map[string]string{"/dir/a.txt": "a\n"})},
				{MountPoint: "/dst", Factory: gbfs.Memory()},
			},
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &gbash.ExecutionRequest{
		Script: "mv /src/dir /dst/copied\ncat /dst/copied/a.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSessionFileSystemExposesConcreteMountableFS(t *testing.T) {
	t.Parallel()

	rt, err := gbash.New(
		gbash.WithFileSystem(gbash.MountableFileSystem(gbash.MountableFileSystemOptions{
			Mounts: []gbfs.MountConfig{
				{MountPoint: "/src", Factory: gbfs.Memory()},
			},
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	mountable, ok := session.FileSystem().(*gbfs.MountableFS)
	if !ok {
		t.Fatalf("Session.FileSystem() = %T, want *fs.MountableFS", session.FileSystem())
	}
	if err := mountable.Mount("/dynamic", gbfs.NewMemory()); err != nil {
		t.Fatalf("Mount(/dynamic) error = %v", err)
	}

	result, err := session.Exec(context.Background(), &gbash.ExecutionRequest{
		Script: "echo hi > /dynamic/note.txt\ncat /dynamic/note.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "hi\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func seededFactory(files map[string]string) gbfs.Factory {
	return gbfs.FactoryFunc(func(ctx context.Context) (gbfs.FileSystem, error) {
		mem := gbfs.NewMemory()
		for name, contents := range files {
			if err := writeFSFile(ctx, mem, name, contents); err != nil {
				return nil, err
			}
		}
		return mem, nil
	})
}

func writeFSFile(ctx context.Context, fsys gbfs.FileSystem, name, contents string) error {
	file, err := fsys.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = io.WriteString(file, contents)
	return err
}
