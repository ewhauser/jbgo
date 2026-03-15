package runtime

import (
	"context"
	"os"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestNullDeviceSemanticsAcrossSandboxBackends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "in-memory",
			cfg:  Config{},
		},
		{
			name: "host-readwrite",
			cfg: Config{
				FileSystem: ReadWriteDirectoryFileSystem(t.TempDir(), ReadWriteDirectoryOptions{}),
				BaseEnv: map[string]string{
					"HOME": "/",
					"PATH": "/bin",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rt := newRuntime(t, &tt.cfg)
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: "" +
					"printf 'discard me\\n' >/dev/null\n" +
					"if test -s /dev/null; then echo sized; else echo empty; fi\n" +
					"wc -c </dev/null\n",
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got, want := result.Stdout, "empty\n0\n"; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestVirtualDeviceDirectoryMergesSandboxEntries(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/dev/tty1", []byte("tty1\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "ls /dev\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "null\ntty1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestVirtualDeviceChildrenCanBeCreatedOnHostReadWriteFS(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{
		FileSystem: ReadWriteDirectoryFileSystem(t.TempDir(), ReadWriteDirectoryOptions{}),
		BaseEnv: map[string]string{
			"HOME": "/",
			"PATH": "/bin",
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"printf 'tty1\\n' >/dev/tty1\n" +
			"ls /dev\n" +
			"cat /dev/tty1\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "null\ntty1\ntty1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestVirtualNullDeviceRejectsRemoval(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "rm /dev/null\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/dev/null"); err != nil {
		t.Fatalf("Stat(/dev/null) after rm error = %v", err)
	}
}

func TestVirtualNullDeviceReportsCharacterDevice(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	info, err := session.FileSystem().Stat(context.Background(), "/dev/null")
	if err != nil {
		t.Fatalf("Stat(/dev/null) error = %v", err)
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatalf("Mode = %v, want character device bits", info.Mode())
	}
}

func TestVirtualDeviceFSPreservesSearchCapabilityForBasePaths(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(gbfs.NewSearchableFactory(gbfs.Memory(), nil), "/workspace"),
	})
	writeSessionFile(t, session, "/workspace/docs/readme.txt", []byte("needle\n"))

	capable, ok := session.FileSystem().(gbfs.SearchCapable)
	if !ok {
		t.Fatalf("filesystem %T does not implement SearchCapable", session.FileSystem())
	}

	if provider, ok := capable.SearchProviderForPath("/"); ok {
		t.Fatalf("SearchProviderForPath(/) = %v, %v, want nil,false", provider, ok)
	}
	if provider, ok := capable.SearchProviderForPath("/dev"); ok {
		t.Fatalf("SearchProviderForPath(/dev) = %v, %v, want nil,false", provider, ok)
	}

	provider, ok := capable.SearchProviderForPath("/workspace")
	if !ok {
		t.Fatal("SearchProviderForPath(/workspace) = false, want true")
	}

	result, err := provider.Search(context.Background(), &gbfs.SearchQuery{
		Root:    "/workspace",
		Literal: "needle",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Path != "/workspace/docs/readme.txt" {
		t.Fatalf("Search hits = %#v, want [/workspace/docs/readme.txt]", result.Hits)
	}
}
