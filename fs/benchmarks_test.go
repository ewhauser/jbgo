package fs_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

func BenchmarkFileSystemStatDeepPath(b *testing.B) {
	files, target := deepPathFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			fsys := backend.seeded(b, files)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := fsys.Stat(context.Background(), target); err != nil {
					b.Fatalf("Stat(%q) error = %v", target, err)
				}
			}
		})
	}
}

func BenchmarkFileSystemOpenReadSmallFile(b *testing.B) {
	files, target, size := smallFileFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			fsys := backend.seeded(b, files)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				readAllBenchmarkFile(b, fsys, target)
			}
		})
	}
}

func BenchmarkFileSystemOpenReadHardLink(b *testing.B) {
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			fsys, target, size := hardLinkFixture(b, backend)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				readAllBenchmarkFile(b, fsys, target)
			}
		})
	}
}

func BenchmarkFileSystemOpenLazyFile(b *testing.B) {
	files, target, size := lazyFileFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			base := backend.seeded(b, files)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				fsys := backend.clone(b, base)
				b.StartTimer()
				readAllBenchmarkFile(b, fsys, target)
			}
		})
	}
}

func BenchmarkFileSystemReadDirWide(b *testing.B) {
	files, dir := wideDirFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			fsys := backend.seeded(b, files)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				entries, err := fsys.ReadDir(context.Background(), dir)
				if err != nil {
					b.Fatalf("ReadDir(%q) error = %v", dir, err)
				}
				if got, want := len(entries), 4096; got != want {
					b.Fatalf("ReadDir(%q) count = %d, want %d", dir, got, want)
				}
			}
		})
	}
}

func BenchmarkHostBackedReadDirTraversal(b *testing.B) {
	root, totalFiles := hostTraversalFixture(b)
	ctx := context.Background()

	benchmarks := []struct {
		name string
		new  func(testing.TB) gbfs.FileSystem
	}{
		{
			name: "host",
			new: func(tb testing.TB) gbfs.FileSystem {
				tb.Helper()
				fsys, err := gbfs.NewHost(gbfs.HostOptions{Root: root})
				if err != nil {
					tb.Skipf("host-backed filesystem unavailable: %v", err)
				}
				return fsys
			},
		},
		{
			name: "overlay_host",
			new: func(tb testing.TB) gbfs.FileSystem {
				tb.Helper()
				fsys, err := gbfs.NewHost(gbfs.HostOptions{Root: root})
				if err != nil {
					tb.Skipf("host-backed filesystem unavailable: %v", err)
				}
				return gbfs.NewOverlay(fsys)
			},
		},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			fsys := benchmark.new(b)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := walkBenchmarkTree(ctx, b, fsys, gbfs.DefaultHostVirtualRoot); got != totalFiles {
					b.Fatalf("walk file count = %d, want %d", got, totalFiles)
				}
			}
		})
	}
}

func BenchmarkFileSystemRealpathSymlinkChain(b *testing.B) {
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			fsys, target := symlinkChainFixture(b, backend)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				realpath, err := fsys.Realpath(context.Background(), target)
				if err != nil {
					b.Fatalf("Realpath(%q) error = %v", target, err)
				}
				if got, want := realpath, "/bench/symlink/target.txt"; got != want {
					b.Fatalf("Realpath(%q) = %q, want %q", target, got, want)
				}
			}
		})
	}
}

func BenchmarkFileSystemParallelStat(b *testing.B) {
	files, target := deepPathFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			fsys := backend.seeded(b, files)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := fsys.Stat(context.Background(), target); err != nil {
						b.Fatalf("Stat(%q) error = %v", target, err)
					}
				}
			})
		})
	}
}

func BenchmarkFileSystemParallelOpenRead(b *testing.B) {
	files, target, size := smallFileFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			fsys := backend.seeded(b, files)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					readAllBenchmarkFile(b, fsys, target)
				}
			})
		})
	}
}

func BenchmarkFileSystemRenameSubtree(b *testing.B) {
	files := renameRemoveFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			base := backend.seeded(b, files)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				fsys := backend.clone(b, base)
				b.StartTimer()
				if err := fsys.Rename(context.Background(), "/bench/rename/src", "/bench/rename/dst"); err != nil {
					b.Fatalf("Rename() error = %v", err)
				}
				if _, err := fsys.Stat(context.Background(), "/bench/rename/dst/dir07/file007.txt"); err != nil {
					b.Fatalf("Stat(renamed) error = %v", err)
				}
			}
		})
	}
}

func BenchmarkFileSystemRemoveSubtree(b *testing.B) {
	files := renameRemoveFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			base := backend.seeded(b, files)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				fsys := backend.clone(b, base)
				b.StartTimer()
				if err := fsys.Remove(context.Background(), "/bench/rename/src", true); err != nil {
					b.Fatalf("Remove() error = %v", err)
				}
				if _, err := fsys.Stat(context.Background(), "/bench/rename/src"); err == nil {
					b.Fatal("Stat(/bench/rename/src) error = nil, want not exist")
				}
			}
		})
	}
}

func BenchmarkFileSystemClone(b *testing.B) {
	files := cloneFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			base := backend.seeded(b, files)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				clone := backend.clone(b, base)
				if _, err := clone.Stat(context.Background(), "/bench/clone/dir03/file003.txt"); err != nil {
					b.Fatalf("clone Stat() error = %v", err)
				}
			}
		})
	}
}

func BenchmarkFileSystemBootstrap(b *testing.B) {
	files := cloneFixture()
	for _, backend := range benchmarkBackends() {
		b.Run(backend.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fsys := backend.seeded(b, files)
				if _, err := fsys.Stat(context.Background(), "/bench/clone/dir03/file003.txt"); err != nil {
					b.Fatalf("Stat() error = %v", err)
				}
			}
		})
	}
}

func deepPathFixture() (files gbfs.InitialFiles, target string) {
	var parts []string
	for i := range 24 {
		parts = append(parts, fmt.Sprintf("seg%02d", i))
	}
	target = "/bench/deep/" + strings.Join(parts, "/") + "/target.txt"
	files = gbfs.InitialFiles{
		target: {Content: []byte("deep target\n")},
	}
	return files, target
}

func smallFileFixture() (files gbfs.InitialFiles, target string, size int64) {
	content := strings.Repeat("small file payload\n", 16)
	files = gbfs.InitialFiles{
		"/bench/small/file.txt": {Content: []byte(content)},
	}
	target = "/bench/small/file.txt"
	size = int64(len(content))
	return files, target, size
}

func wideDirFixture() (files gbfs.InitialFiles, dir string) {
	files = make(gbfs.InitialFiles, 4096)
	for i := range 4096 {
		name := fmt.Sprintf("/bench/wide/file%04d.txt", i)
		files[name] = gbfs.InitialFile{Content: []byte("wide\n")}
	}
	dir = "/bench/wide"
	return files, dir
}

func lazyFileFixture() (files gbfs.InitialFiles, target string, size int64) {
	content := []byte(strings.Repeat("lazy payload\n", 16))
	files = gbfs.InitialFiles{
		"/bench/lazy/file.txt": {
			Lazy: func(context.Context) ([]byte, error) {
				return append([]byte(nil), content...), nil
			},
		},
	}
	target = "/bench/lazy/file.txt"
	size = int64(len(content))
	return files, target, size
}

func hardLinkFixture(tb testing.TB, backend filesystemBackend) (fsys gbfs.FileSystem, target string, size int64) {
	tb.Helper()

	content := strings.Repeat("hard link payload\n", 16)
	fsys = backend.seeded(tb, gbfs.InitialFiles{
		"/bench/hard/source.txt": {Content: []byte(content)},
	})
	if err := fsys.Link(context.Background(), "/bench/hard/source.txt", "/bench/hard/link.txt"); err != nil {
		tb.Fatalf("Link() error = %v", err)
	}
	target = "/bench/hard/link.txt"
	size = int64(len(content))
	return fsys, target, size
}

func symlinkChainFixture(tb testing.TB, backend filesystemBackend) (fsys gbfs.FileSystem, target string) {
	tb.Helper()

	fsys = backend.new(tb)
	writeFile(tb, fsys, "/bench/symlink/target.txt", "symlink target\n")
	prev := "target.txt"
	for i := range 16 {
		name := fmt.Sprintf("/bench/symlink/link%02d", i)
		if err := fsys.Symlink(context.Background(), prev, name); err != nil {
			tb.Fatalf("Symlink(%q) error = %v", name, err)
		}
		prev = pathBase(name)
	}
	target = "/bench/symlink/link15"
	return fsys, target
}

func renameRemoveFixture() gbfs.InitialFiles {
	files := make(gbfs.InitialFiles, 160)
	for dir := range 16 {
		for file := range 10 {
			name := fmt.Sprintf("/bench/rename/src/dir%02d/file%03d.txt", dir, file)
			files[name] = gbfs.InitialFile{Content: []byte("rename payload\n")}
		}
	}
	return files
}

func cloneFixture() gbfs.InitialFiles {
	files := make(gbfs.InitialFiles, 200)
	for dir := range 10 {
		for file := range 20 {
			name := fmt.Sprintf("/bench/clone/dir%02d/file%03d.txt", dir, file)
			files[name] = gbfs.InitialFile{Content: []byte("clone payload\n")}
		}
	}
	return files
}

func hostTraversalFixture(tb testing.TB) (root string, totalFiles int) {
	tb.Helper()

	root = tb.TempDir()
	for pkg := range 12 {
		for file := range 25 {
			dir := filepath.Join(root, fmt.Sprintf("pkg%02d", pkg), fmt.Sprintf("section%02d", file%5))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				tb.Fatalf("MkdirAll(%q) error = %v", dir, err)
			}
			target := filepath.Join(dir, fmt.Sprintf("file%03d.txt", file))
			content := fmt.Sprintf("package=%02d\nsection=%02d\nfile=%03d\n", pkg, file%5, file)
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				tb.Fatalf("WriteFile(%q) error = %v", target, err)
			}
			totalFiles++
		}
	}
	return root, totalFiles
}

func walkBenchmarkTree(ctx context.Context, tb testing.TB, fsys gbfs.FileSystem, root string) int {
	tb.Helper()

	seenFiles := 0
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := fsys.ReadDir(ctx, dir)
		if err != nil {
			tb.Fatalf("ReadDir(%q) error = %v", dir, err)
		}
		for _, entry := range entries {
			child := path.Join(dir, entry.Name())
			if entry.IsDir() {
				stack = append(stack, child)
				continue
			}
			seenFiles++
		}
	}
	return seenFiles
}

func readAllBenchmarkFile(tb testing.TB, fsys gbfs.FileSystem, name string) {
	tb.Helper()

	file, err := fsys.Open(context.Background(), name)
	if err != nil {
		tb.Fatalf("Open(%q) error = %v", name, err)
	}
	if _, err := io.Copy(io.Discard, file); err != nil {
		_ = file.Close()
		tb.Fatalf("Read(%q) error = %v", name, err)
	}
	if err := file.Close(); err != nil {
		tb.Fatalf("Close(%q) error = %v", name, err)
	}
}

func pathBase(name string) string {
	idx := strings.LastIndex(name, "/")
	if idx < 0 {
		return name
	}
	return name[idx+1:]
}
