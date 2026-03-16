package fs_test

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestBenchmarkBackendsSymlinkSemantics(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			fsys := backend.new(t)
			writeFile(t, fsys, "/safe/target.txt", "hello\n")
			if err := fsys.Symlink(context.Background(), "target.txt", "/safe/link.txt"); err != nil {
				t.Fatalf("Symlink() error = %v", err)
			}

			lstatInfo, err := fsys.Lstat(context.Background(), "/safe/link.txt")
			if err != nil {
				t.Fatalf("Lstat() error = %v", err)
			}
			if lstatInfo.Mode()&stdfs.ModeSymlink == 0 {
				t.Fatalf("Lstat().Mode() = %v, want symlink", lstatInfo.Mode())
			}

			target, err := fsys.Readlink(context.Background(), "/safe/link.txt")
			if err != nil {
				t.Fatalf("Readlink() error = %v", err)
			}
			if got, want := target, "target.txt"; got != want {
				t.Fatalf("Readlink() = %q, want %q", got, want)
			}

			realpath, err := fsys.Realpath(context.Background(), "/safe/link.txt")
			if err != nil {
				t.Fatalf("Realpath() error = %v", err)
			}
			if got, want := realpath, "/safe/target.txt"; got != want {
				t.Fatalf("Realpath() = %q, want %q", got, want)
			}

			if got, want := readFile(t, fsys, "/safe/link.txt"), "hello\n"; got != want {
				t.Fatalf("Open(link) = %q, want %q", got, want)
			}
		})
	}
}

func TestBenchmarkBackendsSymlinkLoopFails(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			fsys := backend.new(t)
			if err := fsys.Symlink(context.Background(), "b", "/a"); err != nil {
				t.Fatalf("Symlink(a) error = %v", err)
			}
			if err := fsys.Symlink(context.Background(), "a", "/b"); err != nil {
				t.Fatalf("Symlink(b) error = %v", err)
			}

			_, err := fsys.Realpath(context.Background(), "/a")
			if err == nil {
				t.Fatal("Realpath() error = nil, want symlink loop")
			}
			if !strings.Contains(err.Error(), "too many levels of symbolic links") {
				t.Fatalf("Realpath() error = %v, want symlink loop message", err)
			}
		})
	}
}

func TestBenchmarkBackendsLazyHardLinksShareProvider(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			var calls atomic.Int32
			fsys := backend.seeded(t, gbfs.InitialFiles{
				"/lazy.txt": {
					Lazy: func(context.Context) ([]byte, error) {
						calls.Add(1)
						return []byte("lazy\n"), nil
					},
				},
			})

			if err := fsys.Link(context.Background(), "/lazy.txt", "/lazy-link.txt"); err != nil {
				t.Fatalf("Link() error = %v", err)
			}
			if got, want := readFile(t, fsys, "/lazy-link.txt"), "lazy\n"; got != want {
				t.Fatalf("read hard link = %q, want %q", got, want)
			}
			if got, want := calls.Load(), int32(1); got != want {
				t.Fatalf("provider calls = %d, want %d", got, want)
			}
		})
	}
}

func TestBenchmarkBackendsCloneIsolation(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			base := backend.seeded(t, gbfs.InitialFiles{
				"/data.txt": {Content: []byte("base\n")},
			})
			clone := backend.clone(t, base)

			writeFile(t, clone, "/data.txt", "clone\n")
			writeFile(t, clone, "/new.txt", "new\n")

			if got, want := readFile(t, clone, "/data.txt"), "clone\n"; got != want {
				t.Fatalf("clone /data.txt = %q, want %q", got, want)
			}
			if got, want := readFile(t, base, "/data.txt"), "base\n"; got != want {
				t.Fatalf("base /data.txt = %q, want %q", got, want)
			}
			if _, err := base.Stat(context.Background(), "/new.txt"); !errors.Is(err, stdfs.ErrNotExist) {
				t.Fatalf("base Stat(/new.txt) error = %v, want not exist", err)
			}
		})
	}
}

func TestBenchmarkBackendsChownPreservesModTime(t *testing.T) {
	t.Parallel()

	wantModTime := time.Unix(1_700_000_000, 123_456_789).UTC()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			fsys := backend.seeded(t, gbfs.InitialFiles{
				"/data.txt": {Content: []byte("data\n")},
			})
			if err := fsys.Chtimes(context.Background(), "/data.txt", wantModTime, wantModTime); err != nil {
				t.Fatalf("Chtimes() error = %v", err)
			}

			before, err := fsys.Stat(context.Background(), "/data.txt")
			if err != nil {
				t.Fatalf("Stat(before) error = %v", err)
			}
			if err := fsys.Chown(context.Background(), "/data.txt", 1234, 5678, true); err != nil {
				t.Fatalf("Chown() error = %v", err)
			}
			after, err := fsys.Stat(context.Background(), "/data.txt")
			if err != nil {
				t.Fatalf("Stat(after) error = %v", err)
			}

			if got := before.ModTime(); !got.Equal(wantModTime) {
				t.Fatalf("before ModTime() = %v, want %v", got, wantModTime)
			}
			if got := after.ModTime(); !got.Equal(wantModTime) {
				t.Fatalf("after ModTime() = %v, want %v", got, wantModTime)
			}
		})
	}
}

func TestBenchmarkBackendsRenameIntoSymlinkedDirectoryAllowsMissingLeaf(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			fsys := backend.new(t)
			writeFile(t, fsys, "/from.txt", "payload\n")
			writeFile(t, fsys, "/real/existing.txt", "existing\n")
			if err := fsys.Symlink(context.Background(), "/real", "/link"); err != nil {
				t.Fatalf("Symlink() error = %v", err)
			}

			if err := fsys.Rename(context.Background(), "/from.txt", "/link/new.txt"); err != nil {
				t.Fatalf("Rename() error = %v", err)
			}
			if _, err := fsys.Stat(context.Background(), "/from.txt"); !errors.Is(err, stdfs.ErrNotExist) {
				t.Fatalf("Stat(/from.txt) error = %v, want not exist", err)
			}
			if got, want := readFile(t, fsys, "/real/new.txt"), "payload\n"; got != want {
				t.Fatalf("read renamed target = %q, want %q", got, want)
			}
			if got, want := readFile(t, fsys, "/link/new.txt"), "payload\n"; got != want {
				t.Fatalf("read through symlink = %q, want %q", got, want)
			}
		})
	}
}

func TestBenchmarkBackendsSymlinkCreationInSymlinkedDirectory(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			t.Run("missing leaf", func(t *testing.T) {
				fsys := backend.new(t)
				writeFile(t, fsys, "/real/existing.txt", "existing\n")
				if err := fsys.Symlink(context.Background(), "/real", "/link"); err != nil {
					t.Fatalf("Symlink(/link) error = %v", err)
				}

				if err := fsys.Symlink(context.Background(), "existing.txt", "/link/new.txt"); err != nil {
					t.Fatalf("Symlink(/link/new.txt) error = %v", err)
				}

				target, err := fsys.Readlink(context.Background(), "/real/new.txt")
				if err != nil {
					t.Fatalf("Readlink(/real/new.txt) error = %v", err)
				}
				if got, want := target, "existing.txt"; got != want {
					t.Fatalf("Readlink(/real/new.txt) = %q, want %q", got, want)
				}
				if got, want := readFile(t, fsys, "/link/new.txt"), "existing\n"; got != want {
					t.Fatalf("read through symlink = %q, want %q", got, want)
				}
			})

			t.Run("existing leaf", func(t *testing.T) {
				fsys := backend.new(t)
				writeFile(t, fsys, "/real/new.txt", "original\n")
				if err := fsys.Symlink(context.Background(), "/real", "/link"); err != nil {
					t.Fatalf("Symlink(/link) error = %v", err)
				}

				err := fsys.Symlink(context.Background(), "target.txt", "/link/new.txt")
				if !errors.Is(err, stdfs.ErrExist) {
					t.Fatalf("Symlink(/link/new.txt) error = %v, want exist", err)
				}
				if got, want := readFile(t, fsys, "/real/new.txt"), "original\n"; got != want {
					t.Fatalf("read existing target = %q, want %q", got, want)
				}
			})

			t.Run("final symlink literal path", func(t *testing.T) {
				fsys := backend.new(t)
				if err := fsys.Symlink(context.Background(), "/real/missing.txt", "/link"); err != nil {
					t.Fatalf("Symlink(/link) error = %v", err)
				}

				err := fsys.Symlink(context.Background(), "target.txt", "/link")
				if !errors.Is(err, stdfs.ErrExist) {
					t.Fatalf("Symlink(/link) error = %v, want exist", err)
				}
				target, err := fsys.Readlink(context.Background(), "/link")
				if err != nil {
					t.Fatalf("Readlink(/link) error = %v", err)
				}
				if got, want := target, "/real/missing.txt"; got != want {
					t.Fatalf("Readlink(/link) = %q, want %q", got, want)
				}
				if _, err := fsys.Lstat(context.Background(), "/real/missing.txt"); !errors.Is(err, stdfs.ErrNotExist) {
					t.Fatalf("Lstat(/real/missing.txt) error = %v, want not exist", err)
				}
			})
		})
	}
}

func TestBenchmarkBackendsConcurrentReadDirOnDirtyDirectory(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			fsys := backend.new(t)
			for i := range 64 {
				writeFile(t, fsys, fmt.Sprintf("/dir/file%03d.txt", i), "x\n")
			}

			const goroutines = 16
			const iterations = 32
			var wg sync.WaitGroup
			wg.Add(goroutines)
			for range goroutines {
				go func() {
					defer wg.Done()
					for range iterations {
						entries, err := fsys.ReadDir(context.Background(), "/dir")
						if err != nil {
							t.Errorf("ReadDir(/dir) error = %v", err)
							return
						}
						if got, want := len(entries), 64; got != want {
							t.Errorf("len(ReadDir(/dir)) = %d, want %d", got, want)
							return
						}
					}
				}()
			}
			wg.Wait()
		})
	}
}

func TestBenchmarkBackendsSnapshotAndOverlay(t *testing.T) {
	t.Parallel()

	for _, backend := range benchmarkBackends() {
		t.Run(backend.name, func(t *testing.T) {
			base := backend.seeded(t, gbfs.InitialFiles{
				"/base.txt":       {Content: []byte("base\n")},
				"/shared/old.txt": {Content: []byte("old\n")},
				"/dir/lower.txt":  {Content: []byte("lower\n")},
				"/dir/keep.txt":   {Content: []byte("keep\n")},
			})
			snapshot, err := gbfs.NewSnapshot(context.Background(), base)
			if err != nil {
				t.Fatalf("NewSnapshot() error = %v", err)
			}

			writeFile(t, base, "/base.txt", "after\n")
			if got, want := readFile(t, snapshot, "/base.txt"), "base\n"; got != want {
				t.Fatalf("snapshot /base.txt = %q, want %q", got, want)
			}

			overlay := gbfs.NewOverlay(base)
			if got, want := readFile(t, overlay, "/base.txt"), "after\n"; got != want {
				t.Fatalf("overlay read = %q, want %q", got, want)
			}
			writeFile(t, overlay, "/shared/new.txt", "new\n")
			if _, err := base.Stat(context.Background(), "/shared/new.txt"); !errors.Is(err, stdfs.ErrNotExist) {
				t.Fatalf("base Stat(new.txt) error = %v, want not exist", err)
			}
			if err := overlay.Remove(context.Background(), "/dir/lower.txt", false); err != nil {
				t.Fatalf("Remove() error = %v", err)
			}
			entries := mustReadDir(t, overlay, "/dir")
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			if got, want := names, []string{"keep.txt"}; !slices.Equal(got, want) {
				t.Fatalf("ReadDir(/dir) = %v, want %v", got, want)
			}
		})
	}
}
