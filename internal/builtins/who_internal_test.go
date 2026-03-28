package builtins

import (
	"bytes"
	"context"
	stdfs "io/fs"
	"os"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

type statCountingFS struct {
	gbfs.FileSystem
	stats []string
}

func (f *statCountingFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	f.stats = append(f.stats, name)
	return f.FileSystem.Stat(ctx, name)
}

func TestWhoWriteUserSkipsDeviceMetadataWhenNotRendered(t *testing.T) {
	t.Parallel()

	mem := gbfs.NewMemory()
	if err := mem.MkdirAll(context.Background(), "/dev/pts", 0o755); err != nil {
		t.Fatalf("MkdirAll(/dev/pts) error = %v", err)
	}
	file, err := mem.OpenFile(context.Background(), "/dev/pts/0", os.O_CREATE|os.O_WRONLY, 0o620)
	if err != nil {
		t.Fatalf("OpenFile(/dev/pts/0) error = %v", err)
	}
	_ = file.Close()
	devFS := &statCountingFS{FileSystem: mem}

	var stdout bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		FileSystem: devFS,
		Stdout:     &stdout,
	})

	record := &whoRecord{
		recordType: whoUserType,
		line:       "pts/0",
		user:       "alice",
	}
	opts := whoOptions{
		shortOutput: true,
		includeIdle: true,
	}

	if err := whoWriteUser(context.Background(), inv, opts, record); err != nil {
		t.Fatalf("whoWriteUser() error = %v", err)
	}
	if len(devFS.stats) != 0 {
		t.Fatalf("Stat() calls = %v, want none", devFS.stats)
	}
}

func TestWhoWriteUserReadsDeviceMetadataWhenRendered(t *testing.T) {
	t.Parallel()

	mem := gbfs.NewMemory()
	if err := mem.MkdirAll(context.Background(), "/dev/pts", 0o755); err != nil {
		t.Fatalf("MkdirAll(/dev/pts) error = %v", err)
	}
	file, err := mem.OpenFile(context.Background(), "/dev/pts/0", os.O_CREATE|os.O_WRONLY, 0o620)
	if err != nil {
		t.Fatalf("OpenFile(/dev/pts/0) error = %v", err)
	}
	_ = file.Close()
	devFS := &statCountingFS{FileSystem: mem}

	var stdout bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		FileSystem: devFS,
		Stdout:     &stdout,
	})

	record := &whoRecord{
		recordType: whoUserType,
		line:       "pts/0",
		user:       "alice",
	}
	opts := whoOptions{
		includeMesg: true,
		includeIdle: true,
	}

	if err := whoWriteUser(context.Background(), inv, opts, record); err != nil {
		t.Fatalf("whoWriteUser() error = %v", err)
	}
	if len(devFS.stats) != 1 || devFS.stats[0] != "/dev/pts/0" {
		t.Fatalf("Stat() calls = %v, want [/dev/pts/0]", devFS.stats)
	}
}
