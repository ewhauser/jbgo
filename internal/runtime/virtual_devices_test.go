package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
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
	if got, want := result.Stdout, "full\nnull\ntty1\nurandom\nzero\n"; got != want {
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
	if got, want := result.Stdout, "full\nnull\ntty1\nurandom\nzero\ntty1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFullDeviceReadAndWriteSemantics(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})

	file, err := session.FileSystem().OpenFile(context.Background(), "/dev/full", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenFile(/dev/full) error = %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("Close(/dev/full) error = %v", err)
		}
	}()

	buf := make([]byte, 4)
	if _, err := io.ReadFull(file, buf); err != nil {
		t.Fatalf("ReadFull(/dev/full) error = %v", err)
	}
	if !bytes.Equal(buf, make([]byte, len(buf))) {
		t.Fatalf("Read(/dev/full) = %v, want all zeros", buf)
	}
	if _, err := file.Write([]byte("x")); err == nil {
		t.Fatal("Write(/dev/full) unexpectedly succeeded")
	}
}

func TestBuiltinWritesToFullDeviceFail(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"echo hi >/dev/full\n" +
			"echo echo=$?\n" +
			"printf '%s\\n' hi >/dev/full\n" +
			"echo printf=$?\n" +
			"type echo >/dev/full\n" +
			"echo type=$?\n" +
			"ulimit -a >/dev/full\n" +
			"echo ulimit=$?\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "echo=1\nprintf=1\ntype=1\nulimit=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q; stderr=%q", got, want, result.Stderr)
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

func TestZeroDeviceSemanticsAcrossSandboxBackends(t *testing.T) {
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
			session, err := rt.NewSession(context.Background())
			if err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}

			info, err := session.FileSystem().Stat(context.Background(), "/dev/zero")
			if err != nil {
				t.Fatalf("Stat(/dev/zero) error = %v", err)
			}
			if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice == 0 {
				t.Fatalf("Mode = %v, want character device bits", info.Mode())
			}

			file, err := session.FileSystem().Open(context.Background(), "/dev/zero")
			if err != nil {
				t.Fatalf("Open(/dev/zero) error = %v", err)
			}
			defer func() {
				if err := file.Close(); err != nil {
					t.Errorf("Close(/dev/zero) error = %v", err)
				}
			}()

			buf := make([]byte, 8)
			if _, err := io.ReadFull(file, buf); err != nil {
				t.Fatalf("ReadFull(/dev/zero) error = %v", err)
			}
			if !bytes.Equal(buf, make([]byte, len(buf))) {
				t.Fatalf("Read(/dev/zero) = %v, want all zeros", buf)
			}
		})
	}
}

func TestUrandomDeviceSemanticsAcrossSandboxBackends(t *testing.T) {
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
			session, err := rt.NewSession(context.Background())
			if err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}

			info, err := session.FileSystem().Stat(context.Background(), "/dev/urandom")
			if err != nil {
				t.Fatalf("Stat(/dev/urandom) error = %v", err)
			}
			if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice == 0 {
				t.Fatalf("Mode = %v, want character device bits", info.Mode())
			}

			firstFile, err := session.FileSystem().Open(context.Background(), "/dev/urandom")
			if err != nil {
				t.Fatalf("Open(/dev/urandom first) error = %v", err)
			}
			defer func() {
				if err := firstFile.Close(); err != nil {
					t.Errorf("Close(/dev/urandom first) error = %v", err)
				}
			}()

			secondFile, err := session.FileSystem().Open(context.Background(), "/dev/urandom")
			if err != nil {
				t.Fatalf("Open(/dev/urandom second) error = %v", err)
			}
			defer func() {
				if err := secondFile.Close(); err != nil {
					t.Errorf("Close(/dev/urandom second) error = %v", err)
				}
			}()

			first := make([]byte, 16)
			if _, err := io.ReadFull(firstFile, first); err != nil {
				t.Fatalf("ReadFull(/dev/urandom first) error = %v", err)
			}
			second := make([]byte, len(first))
			if _, err := io.ReadFull(secondFile, second); err != nil {
				t.Fatalf("ReadFull(/dev/urandom second) error = %v", err)
			}
			if bytes.Equal(first, make([]byte, len(first))) {
				t.Fatalf("Read(/dev/urandom) = %v, want non-zero pseudo-random bytes", first)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("Read(/dev/urandom) differs across opens: %v vs %v", first, second)
			}
		})
	}
}
