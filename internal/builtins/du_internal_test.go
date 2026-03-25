package builtins

import (
	stdfs "io/fs"
	"syscall"
	"testing"
	"time"
)

type duStubFileInfo struct {
	mode stdfs.FileMode
	sys  any
}

func (info duStubFileInfo) Name() string         { return "" }
func (info duStubFileInfo) Size() int64          { return 0 }
func (info duStubFileInfo) Mode() stdfs.FileMode { return info.mode }
func (info duStubFileInfo) ModTime() time.Time   { return time.Time{} }
func (info duStubFileInfo) IsDir() bool          { return info.mode.IsDir() }
func (info duStubFileInfo) Sys() any             { return info.sys }

func TestDUPreferredRootDeviceInfoPrefersDereferencedInfo(t *testing.T) {
	t.Parallel()

	got := duPreferredRootDeviceInfo(
		duStubFileInfo{mode: stdfs.ModeDir, sys: &syscall.Stat_t{Dev: 22, Ino: 2}},
		duStubFileInfo{mode: stdfs.ModeSymlink, sys: &syscall.Stat_t{Dev: 11, Ino: 1}},
	)
	device, ok := fileInfoDevice(got)
	if !ok {
		t.Fatal("fileInfoDevice(got) = unknown, want known device")
	}
	if got, want := device, uint64(22); got != want {
		t.Fatalf("device = %d, want %d", got, want)
	}
}

func TestDUPreferredRootDeviceInfoFallsBackToLstatInfo(t *testing.T) {
	t.Parallel()

	got := duPreferredRootDeviceInfo(
		duStubFileInfo{mode: stdfs.ModeDir},
		duStubFileInfo{mode: stdfs.ModeSymlink, sys: &syscall.Stat_t{Dev: 33, Ino: 3}},
	)
	device, ok := fileInfoDevice(got)
	if !ok {
		t.Fatal("fileInfoDevice(got) = unknown, want known device")
	}
	if got, want := device, uint64(33); got != want {
		t.Fatalf("device = %d, want %d", got, want)
	}
}
