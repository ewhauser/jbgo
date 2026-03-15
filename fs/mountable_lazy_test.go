package fs

import (
	"context"
	"errors"
	stdfs "io/fs"
	"os"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMountableFSRejectsInvalidMountsAndAllowsRemount(t *testing.T) {
	t.Parallel()

	fs := NewMountable(NewMemory())

	if err := fs.Mount("/", NewMemory()); err == nil {
		t.Fatal("Mount(/) error = nil, want rejection")
	}
	if err := fs.Mount("relative", NewMemory()); err == nil {
		t.Fatal("Mount(relative) error = nil, want rejection")
	}
	if err := fs.Mount("/mnt/../data", NewMemory()); err == nil {
		t.Fatal("Mount(/mnt/../data) error = nil, want rejection")
	}

	first := NewMemory()
	if err := fs.Mount("/mnt/data", first); err != nil {
		t.Fatalf("Mount(first) error = %v", err)
	}
	if err := fs.Mount("/mnt/data/sub", NewMemory()); err == nil {
		t.Fatal("Mount(nested child) error = nil, want rejection")
	}
	if err := fs.Mount("/mnt", NewMemory()); err == nil {
		t.Fatal("Mount(parent) error = nil, want rejection")
	}

	second := NewMemory()
	if err := fs.Mount("/mnt/data", second); err != nil {
		t.Fatalf("Mount(remount) error = %v", err)
	}

	mounts := fs.Mounts()
	if len(mounts) != 1 {
		t.Fatalf("len(Mounts()) = %d, want 1", len(mounts))
	}
	if mounts[0].FileSystem != second {
		t.Fatalf("Mounts()[0].FileSystem = %T, want remounted filesystem", mounts[0].FileSystem)
	}
}

func TestMountableFSSynthesizesParentsAndRoutesToMountedRoots(t *testing.T) {
	t.Parallel()

	base := seededMemory(t, map[string]string{
		"/mnt/base.txt": "base\n",
	})
	mounted := seededMemory(t, map[string]string{
		"/mounted.txt": "mounted\n",
	})

	fs := NewMountable(base)
	if err := fs.Mount("/mnt/data", mounted); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	info, err := fs.Stat(context.Background(), "/mnt")
	if err != nil {
		t.Fatalf("Stat(/mnt) error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat(/mnt).IsDir() = false, want true")
	}

	entries, err := fs.ReadDir(context.Background(), "/mnt")
	if err != nil {
		t.Fatalf("ReadDir(/mnt) error = %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if got, want := names, []string{"base.txt", "data"}; !slices.Equal(got, want) {
		t.Fatalf("ReadDir(/mnt) names = %v, want %v", got, want)
	}

	if err := fs.Chdir("/mnt"); err != nil {
		t.Fatalf("Chdir(/mnt) error = %v", err)
	}
	if got, want := fs.Getwd(), "/mnt"; got != want {
		t.Fatalf("Getwd() = %q, want %q", got, want)
	}

	if got, want := readTestFile(t, fs, "/mnt/data/mounted.txt"), "mounted\n"; got != want {
		t.Fatalf("mounted read = %q, want %q", got, want)
	}

	rootEntries, err := fs.ReadDir(context.Background(), "/mnt/data")
	if err != nil {
		t.Fatalf("ReadDir(/mnt/data) error = %v", err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != "mounted.txt" {
		t.Fatalf("ReadDir(/mnt/data) = %v, want mounted.txt", rootEntries)
	}
}

func TestMountableFSMkdirAllMountAndSyntheticParentsIsNoop(t *testing.T) {
	t.Parallel()

	fs := NewMountable(NewMemory())
	if err := fs.Mount("/mnt/data", NewMemory()); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	if err := fs.MkdirAll(context.Background(), "/mnt", 0o755); err != nil {
		t.Fatalf("MkdirAll(/mnt) error = %v", err)
	}
	if err := fs.MkdirAll(context.Background(), "/mnt/data", 0o755); err != nil {
		t.Fatalf("MkdirAll(/mnt/data) error = %v", err)
	}
}

func TestMountableFSRemoveAndRenameRejectBusyMountPaths(t *testing.T) {
	t.Parallel()

	base := seededMemory(t, map[string]string{
		"/plain.txt": "plain\n",
	})
	fs := NewMountable(base)
	if err := fs.Mount("/mnt/data", seededMemory(t, map[string]string{"/x.txt": "x\n"})); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	if err := fs.Remove(context.Background(), "/mnt/data", true); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Remove(mount) error = %v, want EBUSY", err)
	}
	if err := fs.Remove(context.Background(), "/mnt", true); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Remove(parent) error = %v, want EBUSY", err)
	}
	if err := fs.Rename(context.Background(), "/mnt", "/elsewhere"); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Rename(parent) error = %v, want EBUSY", err)
	}
	if err := fs.Rename(context.Background(), "/plain.txt", "/mnt/data"); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Rename(dest mount) error = %v, want EBUSY", err)
	}
}

func TestMountableFSCrossMountRenameCopiesAndDeletes(t *testing.T) {
	t.Parallel()

	srcMount := seededMemory(t, map[string]string{
		"/dir/a.txt": "a\n",
		"/dir/b.txt": "b\n",
	})
	dstMount := NewMemory()

	fs := NewMountable(NewMemory())
	if err := fs.Mount("/src", srcMount); err != nil {
		t.Fatalf("Mount(src) error = %v", err)
	}
	if err := fs.Mount("/dst", dstMount); err != nil {
		t.Fatalf("Mount(dst) error = %v", err)
	}

	if err := fs.Rename(context.Background(), "/src/dir", "/dst/copied"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	if got, want := readTestFile(t, fs, "/dst/copied/a.txt"), "a\n"; got != want {
		t.Fatalf("copied a.txt = %q, want %q", got, want)
	}
	if got, want := readTestFile(t, fs, "/dst/copied/b.txt"), "b\n"; got != want {
		t.Fatalf("copied b.txt = %q, want %q", got, want)
	}
	if _, err := fs.Stat(context.Background(), "/src/dir"); !errors.Is(err, stdfs.ErrNotExist) {
		t.Fatalf("Stat(/src/dir) error = %v, want not exist", err)
	}
}

func TestMountableFSCrossMountLinkRejected(t *testing.T) {
	t.Parallel()

	fs := NewMountable(NewMemory())
	if err := fs.Mount("/a", seededMemory(t, map[string]string{"/file.txt": "a\n"})); err != nil {
		t.Fatalf("Mount(/a) error = %v", err)
	}
	if err := fs.Mount("/b", NewMemory()); err != nil {
		t.Fatalf("Mount(/b) error = %v", err)
	}

	err := fs.Link(context.Background(), "/a/file.txt", "/b/file.txt")
	if err == nil {
		t.Fatal("Link() error = nil, want EXDEV")
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || !errors.Is(linkErr.Err, syscall.EXDEV) {
		t.Fatalf("Link() error = %v, want LinkError with EXDEV", err)
	}
}

func TestMountableFSMountSnapshotSortedAndUnmountBusy(t *testing.T) {
	t.Parallel()

	fs := NewMountable(NewMemory())
	if err := fs.Mount("/mnt/b", NewMemory()); err != nil {
		t.Fatalf("Mount(/mnt/b) error = %v", err)
	}
	if err := fs.Mount("/mnt/a", NewMemory()); err != nil {
		t.Fatalf("Mount(/mnt/a) error = %v", err)
	}

	mounts := fs.Mounts()
	if got := []string{mounts[0].MountPoint, mounts[1].MountPoint}; !slices.Equal(got, []string{"/mnt/a", "/mnt/b"}) {
		t.Fatalf("Mounts() order = %v, want sorted order", got)
	}

	if err := fs.Chdir("/mnt/a"); err != nil {
		t.Fatalf("Chdir(/mnt/a) error = %v", err)
	}
	if err := fs.Unmount("/mnt/a"); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("Unmount(/mnt/a) error = %v, want EBUSY", err)
	}

	if err := fs.Chdir("/"); err != nil {
		t.Fatalf("Chdir(/) error = %v", err)
	}
	if err := fs.Unmount("/mnt/a"); err != nil {
		t.Fatalf("Unmount(/mnt/a) error = %v", err)
	}
	if fs.IsMountPoint("/mnt/a") {
		t.Fatal("IsMountPoint(/mnt/a) = true, want false")
	}
}

func TestLazyInitialFilesMaterializeOnDemand(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	factory := SeededMemory(InitialFiles{
		"/lazy.txt": {
			Lazy: func(context.Context) ([]byte, error) {
				calls.Add(1)
				return []byte("lazy\n"), nil
			},
		},
		"/dir/info.txt": {
			Lazy: func(context.Context) ([]byte, error) {
				calls.Add(1)
				return []byte("dir-info\n"), nil
			},
		},
	})

	fsys, err := factory.New(context.Background())
	if err != nil {
		t.Fatalf("SeededMemory.New() error = %v", err)
	}

	rootEntries, err := fsys.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir(/) error = %v", err)
	}
	if got, want := calls.Load(), int32(0); got != want {
		t.Fatalf("provider calls after ReadDir = %d, want %d", got, want)
	}

	info, err := fsys.Stat(context.Background(), "/lazy.txt")
	if err != nil {
		t.Fatalf("Stat(/lazy.txt) error = %v", err)
	}
	if got, want := info.Size(), int64(len("lazy\n")); got != want {
		t.Fatalf("Stat(/lazy.txt).Size() = %d, want %d", got, want)
	}
	if got, want := calls.Load(), int32(1); got != want {
		t.Fatalf("provider calls after Stat = %d, want %d", got, want)
	}

	dirEntries, err := fsys.ReadDir(context.Background(), "/dir")
	if err != nil {
		t.Fatalf("ReadDir(/dir) error = %v", err)
	}
	if len(dirEntries) != 1 {
		t.Fatalf("len(ReadDir(/dir)) = %d, want 1", len(dirEntries))
	}
	if _, err := dirEntries[0].Info(); err != nil {
		t.Fatalf("DirEntry.Info() error = %v", err)
	}
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("provider calls after DirEntry.Info = %d, want %d", got, want)
	}

	if got, want := readTestFile(t, fsys, "/lazy.txt"), "lazy\n"; got != want {
		t.Fatalf("Open(/lazy.txt) = %q, want %q", got, want)
	}
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("provider calls after read = %d, want %d", got, want)
	}

	_ = rootEntries
}

func TestLazyInitialFilesWriteRemoveRenameAndHardLinkInteractions(t *testing.T) {
	t.Parallel()

	t.Run("write before read bypasses provider", func(t *testing.T) {
		var calls atomic.Int32
		fsys, err := SeededMemory(InitialFiles{
			"/lazy.txt": {
				Lazy: func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("lazy\n"), nil
				},
			},
		}).New(context.Background())
		if err != nil {
			t.Fatalf("SeededMemory.New() error = %v", err)
		}

		writeTestFile(t, fsys, "/lazy.txt", "written\n")
		if got, want := readTestFile(t, fsys, "/lazy.txt"), "written\n"; got != want {
			t.Fatalf("read after write = %q, want %q", got, want)
		}
		if got, want := calls.Load(), int32(0); got != want {
			t.Fatalf("provider calls = %d, want %d", got, want)
		}
	})

	t.Run("remove before read bypasses provider", func(t *testing.T) {
		var calls atomic.Int32
		fsys, err := SeededMemory(InitialFiles{
			"/lazy.txt": {
				Lazy: func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("lazy\n"), nil
				},
			},
		}).New(context.Background())
		if err != nil {
			t.Fatalf("SeededMemory.New() error = %v", err)
		}

		if err := fsys.Remove(context.Background(), "/lazy.txt", false); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if got, want := calls.Load(), int32(0); got != want {
			t.Fatalf("provider calls = %d, want %d", got, want)
		}
	})

	t.Run("rename preserves laziness until new path is read", func(t *testing.T) {
		var calls atomic.Int32
		fsys, err := SeededMemory(InitialFiles{
			"/lazy.txt": {
				Lazy: func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("lazy\n"), nil
				},
			},
		}).New(context.Background())
		if err != nil {
			t.Fatalf("SeededMemory.New() error = %v", err)
		}

		if err := fsys.Rename(context.Background(), "/lazy.txt", "/moved.txt"); err != nil {
			t.Fatalf("Rename() error = %v", err)
		}
		if got, want := calls.Load(), int32(0); got != want {
			t.Fatalf("provider calls after rename = %d, want %d", got, want)
		}
		if got, want := readTestFile(t, fsys, "/moved.txt"), "lazy\n"; got != want {
			t.Fatalf("read moved file = %q, want %q", got, want)
		}
		if got, want := calls.Load(), int32(1); got != want {
			t.Fatalf("provider calls after read = %d, want %d", got, want)
		}
	})

	t.Run("hard links share one provider-backed node", func(t *testing.T) {
		var calls atomic.Int32
		fsys, err := SeededMemory(InitialFiles{
			"/lazy.txt": {
				Lazy: func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("lazy\n"), nil
				},
			},
		}).New(context.Background())
		if err != nil {
			t.Fatalf("SeededMemory.New() error = %v", err)
		}

		if err := fsys.Link(context.Background(), "/lazy.txt", "/lazy-link.txt"); err != nil {
			t.Fatalf("Link() error = %v", err)
		}
		if got, want := readTestFile(t, fsys, "/lazy-link.txt"), "lazy\n"; got != want {
			t.Fatalf("read hard link = %q, want %q", got, want)
		}
		if got, want := calls.Load(), int32(1); got != want {
			t.Fatalf("provider calls = %d, want %d", got, want)
		}
	})
}

func TestLazyInitialFilesRetryAndMetadata(t *testing.T) {
	t.Parallel()

	modTime := time.Unix(1_700_000_000, 0).UTC()
	var calls atomic.Int32
	fsys, err := SeededMemory(InitialFiles{
		"/retry.txt": {
			Lazy: func(context.Context) ([]byte, error) {
				if calls.Add(1) == 1 {
					return nil, errors.New("boom")
				}
				return []byte("ok\n"), nil
			},
			Mode:    0o640,
			ModTime: modTime,
		},
	}).New(context.Background())
	if err != nil {
		t.Fatalf("SeededMemory.New() error = %v", err)
	}

	if _, err := fsys.Open(context.Background(), "/retry.txt"); err == nil {
		t.Fatal("first Open() error = nil, want provider failure")
	}
	if got, want := calls.Load(), int32(1); got != want {
		t.Fatalf("provider calls after first failure = %d, want %d", got, want)
	}

	if got, want := readTestFile(t, fsys, "/retry.txt"), "ok\n"; got != want {
		t.Fatalf("read after retry = %q, want %q", got, want)
	}
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("provider calls after retry = %d, want %d", got, want)
	}

	info, err := fsys.Stat(context.Background(), "/retry.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got, want := info.Mode().Perm(), stdfs.FileMode(0o640); got != want {
		t.Fatalf("Mode().Perm() = %v, want %v", got, want)
	}
	if got, want := info.ModTime(), modTime; !got.Equal(want) {
		t.Fatalf("ModTime() = %v, want %v", got, want)
	}
}
