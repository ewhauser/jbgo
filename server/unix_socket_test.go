package server

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func newShortSocketPath(t testing.TB) string {
	t.Helper()

	file, err := os.CreateTemp("", "gbs-")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q) error = %v", path, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(%q) error = %v", path, err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func TestListenUnixSocketSetsSocketPermissions0600(t *testing.T) {
	t.Parallel()
	socket := newShortSocketPath(t)

	_, ln, err := listenUnixSocket(t.Context(), socket)
	if err != nil {
		t.Fatalf("listenUnixSocket(%q) error = %v", socket, err)
	}
	defer func() { _ = ln.Close() }()

	info, err := os.Stat(socket)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", socket, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("mode = %v, want socket bit set", info.Mode())
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("socket mode = %o, want %o", got, want)
	}
}

func TestListenUnixSocketRejectsExistingNonSocketPath(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "gbash.sock")
	if err := os.WriteFile(socket, []byte("not a socket"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", socket, err)
	}

	_, _, err := listenUnixSocket(t.Context(), socket)
	if err == nil {
		t.Fatal("listenUnixSocket() error = nil, want non-socket path rejection")
	}
	if !strings.Contains(err.Error(), "path exists and is not a socket") {
		t.Fatalf("error = %v, want non-socket path rejection", err)
	}
}

func TestListenUnixSocketRejectsActiveSocketPath(t *testing.T) {
	t.Parallel()
	socket := newShortSocketPath(t)

	active, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	defer func() { _ = active.Close() }()

	_, _, err = listenUnixSocket(t.Context(), socket)
	if err == nil {
		t.Fatal("listenUnixSocket() error = nil, want active socket rejection")
	}
	if !strings.Contains(err.Error(), "socket already in use") {
		t.Fatalf("error = %v, want active socket rejection", err)
	}
}

func TestListenUnixSocketRemovesStaleSocket(t *testing.T) {
	t.Parallel()
	socket := newShortSocketPath(t)

	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("Socket(AF_UNIX) error = %v", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: socket}); err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("Bind(%q) error = %v", socket, err)
	}
	if err := syscall.Listen(fd, 1); err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("Listen(stale fd) error = %v", err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatalf("Close(stale fd) error = %v", err)
	}
	if _, err := os.Lstat(socket); err != nil {
		t.Fatalf("Lstat(%q) error = %v", socket, err)
	}

	_, ln, err := listenUnixSocket(t.Context(), socket)
	if err != nil {
		t.Fatalf("listenUnixSocket(%q) error = %v", socket, err)
	}
	_ = ln.Close()
}
