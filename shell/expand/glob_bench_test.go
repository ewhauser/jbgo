package expand

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"
)

var benchmarkExpandPathFieldSink []string

func BenchmarkExpandPathField(b *testing.B) {
	for _, tc := range []struct {
		name  string
		field []fieldPart
		want  []string
	}{
		{
			name:  "literal",
			field: []fieldPart{{val: "workspace/project/cmd/script.sh"}},
			want:  []string{"workspace/project/cmd/script.sh"},
		},
		{
			name:  "quoted_meta_literal",
			field: []fieldPart{{quote: quoteSingle, val: "workspace/*/[abc].sh"}},
			want:  []string{"workspace/*/[abc].sh"},
		},
		{
			name: "multipart_literal",
			field: []fieldPart{
				{val: "workspace/"},
				{quote: quoteSingle, val: "*literal*"},
				{val: "/cmd.sh"},
			},
			want: []string{"workspace/*literal*/cmd.sh"},
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			spy := newTrackedGlobReadDirSpy(nil)
			cfg := prepareConfig(&Config{ReadDir: spy.ReadDir})

			got, err := cfg.expandPathField("/", tc.field)
			if err != nil {
				b.Fatalf("expandPathField(%q) error = %v", tc.name, err)
			}
			if !slices.Equal(got, tc.want) {
				b.Fatalf("expandPathField(%q) = %#v, want %#v", tc.name, got, tc.want)
			}
			if spy.totalCalls != 0 {
				b.Fatalf("expandPathField(%q) read %d directories during setup, want 0", tc.name, spy.totalCalls)
			}
			spy.resetCounts()

			var totalReadDir int
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				before := spy.totalCalls
				got, err := cfg.expandPathField("/", tc.field)
				if err != nil {
					b.Fatalf("expandPathField(%q) error = %v", tc.name, err)
				}
				benchmarkExpandPathFieldSink = got
				totalReadDir += spy.totalCalls - before
			}
			b.ReportMetric(float64(totalReadDir)/float64(b.N), "readdir/op")
		})
	}
}

func BenchmarkGlob(b *testing.B) {
	for _, tc := range []struct {
		name  string
		pat   string
		cfg   func(*globOSReadDirSpy) *Config
		setup func(testing.TB) string
		want  int
	}{
		{
			name:  "literal_prefix",
			pat:   "workspace/project/*/cmd/*.sh",
			cfg:   func(spy *globOSReadDirSpy) *Config { return &Config{ReadDir: spy.ReadDir} },
			setup: benchmarkLiteralPrefixDir,
			want:  256,
		},
		{
			name: "globstar_tree",
			pat:  "tree/**/*.txt",
			cfg: func(spy *globOSReadDirSpy) *Config {
				return &Config{ReadDir: spy.ReadDir, GlobStar: true}
			},
			setup: benchmarkGlobStarDir,
			want:  189,
		},
		{
			name:  "symlink_dirs",
			pat:   "links/*/*/*.sh",
			cfg:   func(spy *globOSReadDirSpy) *Config { return &Config{ReadDir: spy.ReadDir} },
			setup: benchmarkSymlinkDirTree,
			want:  288,
		},
		{
			name: "locale_collation",
			pat:  "locale/**/*.txt",
			cfg: func(spy *globOSReadDirSpy) *Config {
				return &Config{
					Env: testEnv{
						"LC_COLLATE": {Set: true, Kind: String, Str: "en_US.UTF-8"},
					},
					ReadDir:  spy.ReadDir,
					GlobStar: true,
				}
			},
			setup: benchmarkLocaleDir,
			want:  48,
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			base := tc.setup(b)
			spy := &globOSReadDirSpy{}
			cfg := tc.cfg(spy)
			matches, err := cfg.glob(base, tc.pat)
			if err != nil {
				b.Fatalf("glob(%q) error = %v", tc.pat, err)
			}
			if len(matches) != tc.want {
				b.Fatalf("len(glob(%q)) = %d, want %d", tc.pat, len(matches), tc.want)
			}

			var totalReadDir int
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				before := spy.totalCalls
				matches, err := cfg.glob(base, tc.pat)
				if err != nil {
					b.Fatalf("glob(%q) error = %v", tc.pat, err)
				}
				if len(matches) != tc.want {
					b.Fatalf("len(glob(%q)) = %d, want %d", tc.pat, len(matches), tc.want)
				}
				totalReadDir += spy.totalCalls - before
			}
			b.ReportMetric(float64(totalReadDir)/float64(b.N), "readdir/op")
		})
	}
}

func BenchmarkSyntheticGlob(b *testing.B) {
	for _, tc := range []struct {
		name      string
		pat       string
		cfg       func(*globReadDirSpy) *Config
		newFS     func() *globReadDirSpy
		wantCount int
	}{
		{
			name:      "literal_prefix",
			pat:       "workspace/project/*/cmd/*.sh",
			cfg:       func(spy *globReadDirSpy) *Config { return &Config{ReadDir: spy.ReadDir} },
			newFS:     benchmarkLiteralPrefixFS,
			wantCount: 256,
		},
		{
			name: "globstar_tree",
			pat:  "tree/**/*.txt",
			cfg: func(spy *globReadDirSpy) *Config {
				return &Config{ReadDir: spy.ReadDir, GlobStar: true}
			},
			newFS:     benchmarkGlobStarFS,
			wantCount: 189,
		},
		{
			name:      "symlink_dirs",
			pat:       "links/*/*/*.sh",
			cfg:       func(spy *globReadDirSpy) *Config { return &Config{ReadDir: spy.ReadDir} },
			newFS:     benchmarkSymlinkDirFS,
			wantCount: 288,
		},
		{
			name: "locale_collation",
			pat:  "locale/**/*.txt",
			cfg: func(spy *globReadDirSpy) *Config {
				return &Config{
					Env: testEnv{
						"LC_COLLATE": {Set: true, Kind: String, Str: "en_US.UTF-8"},
					},
					ReadDir:  spy.ReadDir,
					GlobStar: true,
				}
			},
			newFS:     benchmarkLocaleFS,
			wantCount: 48,
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			spy := tc.newFS()
			cfg := tc.cfg(spy)
			matches, err := cfg.glob("/", tc.pat)
			if err != nil {
				b.Fatalf("glob(%q) error = %v", tc.pat, err)
			}
			if len(matches) != tc.wantCount {
				b.Fatalf("len(glob(%q)) = %d, want %d", tc.pat, len(matches), tc.wantCount)
			}
			spy.resetCounts()

			var totalReadDir int
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				before := spy.totalCalls
				matches, err := cfg.glob("/", tc.pat)
				if err != nil {
					b.Fatalf("glob(%q) error = %v", tc.pat, err)
				}
				if len(matches) != tc.wantCount {
					b.Fatalf("len(glob(%q)) = %d, want %d", tc.pat, len(matches), tc.wantCount)
				}
				totalReadDir += spy.totalCalls - before
			}
			b.ReportMetric(float64(totalReadDir)/float64(b.N), "readdir/op")
		})
	}
}

type globOSReadDirSpy struct {
	totalCalls int
}

func (spy *globOSReadDirSpy) ReadDir(name string) ([]fs.DirEntry, error) {
	spy.totalCalls++
	return os.ReadDir(name)
}

var errGlobBenchNotDir = errors.New("not a directory")

type globReadDirSpy struct {
	nodes      map[string]globReadDirNode
	totalCalls int
	pathCalls  map[string]int
	trackPaths bool
}

type globReadDirNode struct {
	entries []fs.DirEntry
	err     error
}

func newGlobReadDirSpy(nodes map[string]globReadDirNode) *globReadDirSpy {
	return &globReadDirSpy{
		nodes: nodes,
	}
}

func newTrackedGlobReadDirSpy(nodes map[string]globReadDirNode) *globReadDirSpy {
	return &globReadDirSpy{
		nodes:      nodes,
		pathCalls:  make(map[string]int),
		trackPaths: true,
	}
}

func (spy *globReadDirSpy) ReadDir(name string) ([]fs.DirEntry, error) {
	spy.totalCalls++
	if spy.trackPaths {
		spy.pathCalls[name]++
	}
	node, ok := spy.nodes[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return cloneDirEntries(node.entries), node.err
}

func (spy *globReadDirSpy) resetCounts() {
	spy.totalCalls = 0
	clear(spy.pathCalls)
}

func (spy *globReadDirSpy) callsFor(name string) int {
	return spy.pathCalls[name]
}

func dirNode(entries ...fs.DirEntry) globReadDirNode {
	return globReadDirNode{entries: sortDirEntries(entries)}
}

func fileNode() globReadDirNode {
	return globReadDirNode{err: errGlobBenchNotDir}
}

func dirEntry(name string) fs.DirEntry {
	return &mockFileInfo{name: name, typ: fs.ModeDir}
}

func fileEntry(name string) fs.DirEntry {
	return &mockFileInfo{name: name}
}

func symlinkEntry(name string) fs.DirEntry {
	return &mockFileInfo{name: name, typ: fs.ModeSymlink}
}

func sortDirEntries(entries []fs.DirEntry) []fs.DirEntry {
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return cmp.Compare(a.Name(), b.Name())
	})
	return entries
}

func cloneDirEntries(entries []fs.DirEntry) []fs.DirEntry {
	cloned := make([]fs.DirEntry, len(entries))
	for i, entry := range entries {
		cloned[i] = &mockFileInfo{name: entry.Name(), typ: entry.Type()}
	}
	return cloned
}

func benchmarkLiteralPrefixDir(tb testing.TB) string {
	tb.Helper()

	root := tb.TempDir()
	for i := range 32 {
		cmdDir := filepath.Join(root, "workspace", "project", benchmarkName("module", i), "cmd")
		mustMkdirAll(tb, cmdDir)
		for j := range 8 {
			mustWriteFile(tb, filepath.Join(cmdDir, benchmarkName("tool", j)+".sh"))
		}
	}
	return filepath.ToSlash(root)
}

func benchmarkGlobStarDir(tb testing.TB) string {
	tb.Helper()

	root := tb.TempDir()
	treeDir := filepath.Join(root, "tree")
	mustMkdirAll(tb, treeDir)
	for _, name := range []string{"a.txt", "a0.txt", "b.txt"} {
		mustWriteFile(tb, filepath.Join(treeDir, name))
	}
	for _, top := range []string{"a", "a0", "b"} {
		topDir := filepath.Join(treeDir, top)
		branchDir := filepath.Join(topDir, "branch")
		mustMkdirAll(tb, branchDir)
		mustWriteFile(tb, filepath.Join(topDir, top+"-root.txt"))
		mustWriteFile(tb, filepath.Join(topDir, top+"0.txt"))
		for i := range 12 {
			subDir := filepath.Join(branchDir, benchmarkName("sub", i))
			deepDir := filepath.Join(subDir, "deep")
			mustMkdirAll(tb, deepDir)
			mustWriteFile(tb, filepath.Join(branchDir, benchmarkName("match", i)+".txt"))
			mustWriteFile(tb, filepath.Join(subDir, "leaf.txt"))
			mustWriteFile(tb, filepath.Join(subDir, "leaf0.txt"))
			mustWriteFile(tb, filepath.Join(deepDir, "deep-leaf.txt"))
			mustWriteFile(tb, filepath.Join(deepDir, "deep0.txt"))
		}
	}
	return filepath.ToSlash(root)
}

func benchmarkSymlinkDirTree(tb testing.TB) string {
	tb.Helper()

	root := tb.TempDir()
	linksDir := filepath.Join(root, "links")
	targetsDir := filepath.Join(root, "targets")
	mustMkdirAll(tb, linksDir)
	mustMkdirAll(tb, targetsDir)
	for i := range 24 {
		app := benchmarkName("app", i)
		targetAppDir := filepath.Join(targetsDir, app)
		mustMkdirAll(tb, targetAppDir)
		for j := range 3 {
			cmdDir := filepath.Join(targetAppDir, benchmarkName("cmd", j))
			mustMkdirAll(tb, cmdDir)
			for k := range 4 {
				mustWriteFile(tb, filepath.Join(cmdDir, benchmarkName("script", k)+".sh"))
			}
		}
		linkPath := filepath.Join(linksDir, app)
		if err := os.Symlink(targetAppDir, linkPath); err != nil {
			tb.Skipf("symlink setup failed: %v", err)
		}
	}
	return filepath.ToSlash(root)
}

func benchmarkLocaleDir(tb testing.TB) string {
	tb.Helper()

	root := tb.TempDir()
	localeDir := filepath.Join(root, "locale")
	mustMkdirAll(tb, localeDir)
	for _, name := range []string{"hello", "hello-preamble", "hello_test"} {
		mustWriteFile(tb, filepath.Join(localeDir, name+".txt"))
		dirPath := filepath.Join(localeDir, name)
		mustMkdirAll(tb, dirPath)
		for i := range 15 {
			mustWriteFile(tb, filepath.Join(dirPath, benchmarkName("doc", i)+".txt"))
		}
	}
	return filepath.ToSlash(root)
}

func mustMkdirAll(tb testing.TB, name string) {
	tb.Helper()
	if err := os.MkdirAll(name, 0o755); err != nil {
		tb.Fatalf("MkdirAll(%q) error = %v", name, err)
	}
}

func mustWriteFile(tb testing.TB, name string) {
	tb.Helper()
	if err := os.WriteFile(name, nil, 0o644); err != nil {
		tb.Fatalf("WriteFile(%q) error = %v", name, err)
	}
}

func benchmarkLiteralPrefixFS() *globReadDirSpy {
	nodes := map[string]globReadDirNode{
		"/":                  dirNode(dirEntry("workspace")),
		"/workspace":         dirNode(dirEntry("project")),
		"/workspace/project": dirNode(),
	}

	projectEntries := make([]fs.DirEntry, 0, 32)
	for i := range 32 {
		module := benchmarkName("module", i)
		projectEntries = append(projectEntries, dirEntry(module))

		modulePath := path.Join("/workspace/project", module)
		nodes[modulePath] = dirNode(dirEntry("cmd"))

		cmdPath := path.Join(modulePath, "cmd")
		cmdEntries := make([]fs.DirEntry, 0, 8)
		for j := range 8 {
			cmdEntries = append(cmdEntries, fileEntry(benchmarkName("tool", j)+".sh"))
			nodes[path.Join(cmdPath, benchmarkName("tool", j)+".sh")] = fileNode()
		}
		nodes[cmdPath] = dirNode(cmdEntries...)
	}
	nodes["/workspace/project"] = dirNode(projectEntries...)
	return newGlobReadDirSpy(nodes)
}

func benchmarkGlobStarFS() *globReadDirSpy {
	nodes := map[string]globReadDirNode{
		"/":     dirNode(dirEntry("tree")),
		"/tree": dirNode(),
	}

	rootEntries := []fs.DirEntry{
		dirEntry("a"),
		fileEntry("a.txt"),
		dirEntry("a0"),
		fileEntry("a0.txt"),
		dirEntry("b"),
		fileEntry("b.txt"),
	}
	nodes["/tree"] = dirNode(rootEntries...)
	nodes["/tree/a.txt"] = fileNode()
	nodes["/tree/a0.txt"] = fileNode()
	nodes["/tree/b.txt"] = fileNode()

	for _, top := range []string{"a", "a0", "b"} {
		topPath := path.Join("/tree", top)
		topEntries := []fs.DirEntry{
			dirEntry("branch"),
			fileEntry(top + "-root.txt"),
			fileEntry(top + "0.txt"),
		}
		nodes[topPath] = dirNode(topEntries...)
		nodes[path.Join(topPath, top+"-root.txt")] = fileNode()
		nodes[path.Join(topPath, top+"0.txt")] = fileNode()

		branchPath := path.Join(topPath, "branch")
		branchEntries := make([]fs.DirEntry, 0, 12)
		for i := range 12 {
			dirName := benchmarkName("sub", i)
			fileName := benchmarkName("match", i) + ".txt"
			branchEntries = append(branchEntries, dirEntry(dirName), fileEntry(fileName))
			nodes[path.Join(branchPath, fileName)] = fileNode()

			subPath := path.Join(branchPath, dirName)
			subEntries := []fs.DirEntry{
				fileEntry("leaf.txt"),
				fileEntry("leaf0.txt"),
				dirEntry("deep"),
			}
			nodes[subPath] = dirNode(subEntries...)
			nodes[path.Join(subPath, "leaf.txt")] = fileNode()
			nodes[path.Join(subPath, "leaf0.txt")] = fileNode()

			deepPath := path.Join(subPath, "deep")
			deepEntries := []fs.DirEntry{
				fileEntry("deep-leaf.txt"),
				fileEntry("deep0.txt"),
			}
			nodes[deepPath] = dirNode(deepEntries...)
			nodes[path.Join(deepPath, "deep-leaf.txt")] = fileNode()
			nodes[path.Join(deepPath, "deep0.txt")] = fileNode()
		}
		nodes[branchPath] = dirNode(branchEntries...)
	}

	return newGlobReadDirSpy(nodes)
}

func benchmarkSymlinkDirFS() *globReadDirSpy {
	nodes := map[string]globReadDirNode{
		"/":      dirNode(dirEntry("links")),
		"/links": dirNode(),
	}

	linkEntries := make([]fs.DirEntry, 0, 24)
	for i := range 24 {
		app := benchmarkName("app", i)
		linkEntries = append(linkEntries, symlinkEntry(app))

		appPath := path.Join("/links", app)
		cmdEntries := make([]fs.DirEntry, 0, 3)
		for j := range 3 {
			cmd := benchmarkName("cmd", j)
			cmdEntries = append(cmdEntries, dirEntry(cmd))

			cmdPath := path.Join(appPath, cmd)
			shEntries := make([]fs.DirEntry, 0, 4)
			for k := range 4 {
				script := benchmarkName("script", k) + ".sh"
				shEntries = append(shEntries, fileEntry(script))
				nodes[path.Join(cmdPath, script)] = fileNode()
			}
			nodes[cmdPath] = dirNode(shEntries...)
		}
		nodes[appPath] = dirNode(cmdEntries...)
	}
	nodes["/links"] = dirNode(linkEntries...)
	return newGlobReadDirSpy(nodes)
}

func benchmarkLocaleFS() *globReadDirSpy {
	nodes := map[string]globReadDirNode{
		"/":       dirNode(dirEntry("locale")),
		"/locale": dirNode(),
	}

	rootEntries := make([]fs.DirEntry, 0, 6)
	for _, name := range []string{"hello", "hello-preamble", "hello_test"} {
		rootEntries = append(rootEntries, dirEntry(name), fileEntry(name+".txt"))
		nodes[path.Join("/locale", name+".txt")] = fileNode()

		dirPath := path.Join("/locale", name)
		dirEntries := make([]fs.DirEntry, 0, 15)
		for i := range 15 {
			fileName := benchmarkName("doc", i) + ".txt"
			dirEntries = append(dirEntries, fileEntry(fileName))
			nodes[path.Join(dirPath, fileName)] = fileNode()
		}
		nodes[dirPath] = dirNode(dirEntries...)
	}
	nodes["/locale"] = dirNode(rootEntries...)
	return newGlobReadDirSpy(nodes)
}

func benchmarkName(prefix string, i int) string {
	return prefix + string(rune('a'+(i/26))) + string(rune('a'+(i%26)))
}
