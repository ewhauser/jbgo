package shell

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"strings"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/policy"
)

func newTestProcSubstManager(pol policy.Policy) *procSubstManager {
	return newProcSubstManager(pol, "/home/agent/project", "/home/agent", nil)
}

func TestProcSubstFSReadPathIsNamedPipeAndOneShot(t *testing.T) {
	t.Parallel()

	manager := newTestProcSubstManager(nil)
	defer manager.Close()
	endpoint, err := manager.endpoint(context.Background(), &syntax.ProcSubst{Op: syntax.CmdIn})
	if err != nil {
		t.Fatalf("endpoint() error = %v", err)
	}

	fsys := newProcSubstFS(gbfs.NewMemory(), manager)
	info, err := fsys.Stat(context.Background(), endpoint.Path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", endpoint.Path, err)
	}
	if info.Mode()&stdfs.ModeNamedPipe == 0 {
		t.Fatalf("Mode(%q) = %v, want named pipe", endpoint.Path, info.Mode())
	}
	if resolved, err := fsys.Realpath(context.Background(), endpoint.Path); err != nil {
		t.Fatalf("Realpath(%q) error = %v", endpoint.Path, err)
	} else if resolved != endpoint.Path {
		t.Fatalf("Realpath(%q) = %q, want identical path", endpoint.Path, resolved)
	}

	file, err := fsys.Open(context.Background(), endpoint.Path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", endpoint.Path, err)
	}
	defer func() { _ = file.Close() }()

	if _, err := fsys.Stat(context.Background(), endpoint.Path); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("Stat(%q) after open error = %v, want not exist", endpoint.Path, err)
	}
	if _, err := fsys.Realpath(context.Background(), endpoint.Path); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("Realpath(%q) after open error = %v, want not exist", endpoint.Path, err)
	}
	if _, err := fsys.Open(context.Background(), endpoint.Path); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("second Open(%q) error = %v, want not exist", endpoint.Path, err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = endpoint.Writer.Write([]byte("hello\n"))
		_ = endpoint.Writer.Close()
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", endpoint.Path, err)
	}
	<-done
	if got, want := string(data), "hello\n"; got != want {
		t.Fatalf("ReadAll(%q) = %q, want %q", endpoint.Path, got, want)
	}
}

func TestProcSubstFSWritePathEnforcesWriteOnly(t *testing.T) {
	t.Parallel()

	manager := newTestProcSubstManager(nil)
	defer manager.Close()
	endpoint, err := manager.endpoint(context.Background(), &syntax.ProcSubst{Op: syntax.CmdOut})
	if err != nil {
		t.Fatalf("endpoint() error = %v", err)
	}

	fsys := newProcSubstFS(gbfs.NewMemory(), manager)
	if _, err := fsys.Open(context.Background(), endpoint.Path); !errors.Is(err, stdfs.ErrPermission) {
		t.Fatalf("Open(%q) error = %v, want permission", endpoint.Path, err)
	}

	file, err := fsys.OpenFile(context.Background(), endpoint.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", endpoint.Path, err)
	}

	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(endpoint.Reader)
		done <- data
	}()

	if _, err := file.Write([]byte("payload")); err != nil {
		t.Fatalf("Write(%q) error = %v", endpoint.Path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q) error = %v", endpoint.Path, err)
	}

	if got, want := string(<-done), "payload"; got != want {
		t.Fatalf("reader saw %q, want %q", got, want)
	}
}

func TestProcSubstConsumerCloseUnblocksProducer(t *testing.T) {
	t.Parallel()

	manager := newTestProcSubstManager(nil)
	defer manager.Close()
	endpoint, err := manager.endpoint(context.Background(), &syntax.ProcSubst{Op: syntax.CmdIn})
	if err != nil {
		t.Fatalf("endpoint() error = %v", err)
	}

	fsys := newProcSubstFS(gbfs.NewMemory(), manager)
	file, err := fsys.Open(context.Background(), endpoint.Path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", endpoint.Path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q) error = %v", endpoint.Path, err)
	}
	if _, ok := manager.entry(endpoint.Path, true); ok {
		t.Fatalf("entry(%q) still present after consumer close", endpoint.Path)
	}

	if _, err := endpoint.Writer.Write([]byte("blocked")); err == nil {
		t.Fatalf("Writer.Write(%q) error = nil, want closed-pipe style failure", endpoint.Path)
	}
}

func TestProcSubstManagerCloseUnblocksEndpoints(t *testing.T) {
	t.Parallel()

	manager := newTestProcSubstManager(nil)
	endpoint, err := manager.endpoint(context.Background(), &syntax.ProcSubst{Op: syntax.CmdIn})
	if err != nil {
		t.Fatalf("endpoint() error = %v", err)
	}

	manager.Close()

	if _, err := endpoint.Writer.Write([]byte("after-close")); err == nil {
		t.Fatal("Writer.Write() error = nil, want failure after manager close")
	}
}

func TestProcSubstManagerChoosesPolicyAllowedRoot(t *testing.T) {
	t.Parallel()

	manager := newTestProcSubstManager(policy.NewStatic(&policy.Config{
		ReadRoots:  []string{"/home/agent"},
		WriteRoots: []string{"/home/agent"},
	}))
	defer manager.Close()

	endpoint, err := manager.endpoint(context.Background(), &syntax.ProcSubst{Op: syntax.CmdOut})
	if err != nil {
		t.Fatalf("endpoint() error = %v", err)
	}
	if !strings.HasPrefix(endpoint.Path, "/home/agent/") {
		t.Fatalf("endpoint.Path = %q, want /home/agent/ prefix", endpoint.Path)
	}
}
